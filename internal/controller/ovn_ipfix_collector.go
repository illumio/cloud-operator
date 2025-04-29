package controller

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"time"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IsOVNDeployed Checks for the presence of the `ovn-kubernetes` namespace, detection based on OVN namespace.
// https://ovn-kubernetes.io/installation/launching-ovn-kubernetes-on-kind/#run-the-kind-deployment-with-podman
func (sm *streamManager) isOVNDeployed() bool {
	clientset, err := NewClientSet()
	if err != nil {
		return false
	}
	_, err = clientset.CoreV1().Namespaces().Get(context.TODO(), "ovn-kubernetes", metav1.GetOptions{})
	if err != nil {
		return false
	}
	return true
}

// Define hard-coded templates
var templates = map[uint16]string{
	1: "Template1", // Example template ID 1
	2: "Template2", // Example template ID 2
	// Add more templates as needed
}

func (sm *streamManager) startOVNIPFIXCollector(ctx context.Context, logger *zap.Logger) error {
	go sm.processOVNFlow(ctx, logger)
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		// Create a custom listener for OVN flows
		listener, err := net.ListenPacket("udp", sm.streamClient.IpfixCollectorPort)
		if err != nil {
			logger.Fatal("Failed to listen on OVN port", zap.String("address", sm.streamClient.IpfixCollectorPort), zap.Error(err))
		}

		logger.Info("OVN server listening", zap.String("address", sm.streamClient.IpfixCollectorPort))
		// Allocate a buffer to hold incoming UDP packets. The size is set to 65535 bytes, which is the maximum size of a UDP packet.
		buf := make([]byte, 65535)
		for {
			n, _, err := listener.ReadFrom(buf)
			if err != nil {
				logger.Error("Failed to read from OVN listener", zap.Error(err))
				return err
			}
			sm.streamClient.ovnEventChan <- buf[:n]
		}
	}
}

func (sm *streamManager) processOVNFlow(ctx context.Context, logger *zap.Logger) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case packet := <-sm.streamClient.ovnEventChan:
			// Extract template ID from the packet (assuming the first 2 bytes represent the template ID)
			if len(packet) < 2 {
				logger.Error("Packet too short to contain a template ID")
				continue
			}
			templateID := uint16(packet[0])<<8 | uint16(packet[1])

			logger.Info("Processing packet with template", zap.Uint16("templateID", templateID), zap.Int16("template", int16(templateID)))

			// Process the packet based on the template (this is a placeholder for actual processing logic)
			convertedStandardFlow, err := ParseIPFIXMessageWithTemplate(packet, templateID)
			if err != nil {
				logger.Error("Failed to parse IPFIX message", zap.Error(err))
				continue
			}

			// Cache the flow
			if err := sm.FlowCache.CacheFlow(ctx, convertedStandardFlow); err != nil {
				logger.Error("Failed to cache flow", zap.Error(err))
				return err
			}
		}
	}
}

// ParseIPFIXMessageWithTemplate parses incoming bytes into a single IPFIXFlowRecord using predefined templates.
func ParseIPFIXMessageWithTemplate(data []byte, templateID uint16) (*pb.StandardFlow, error) {
	template, exists := ipfixTemplates[templateID]
	if !exists {
		return nil, fmt.Errorf("unknown template ID: %d", templateID)
	}

	if len(data) < 16 {
		return nil, fmt.Errorf("data too short to contain IPFIX header")
	}

	// Parse the IPFIX header
	header := IPFIXHeader{
		Length: binary.BigEndian.Uint16(data[2:4]),
	}

	// Ensure the data length matches the header length
	if len(data) < int(header.Length) {
		return nil, fmt.Errorf("data length (%d) does not match header length (%d)", len(data), header.Length)
	}

	// Parse a single flow record using the template
	offset := 16
	if len(data[offset:]) < 20 {
		return nil, fmt.Errorf("data too short to contain a flow record")
	}

	standardFlow := &pb.StandardFlow{
		Layer3: &pb.IP{
			Source:      net.IP(data[offset+template.Fields["SourceIP"] : offset+template.Fields["SourceIP"]+4]).String(),
			Destination: net.IP(data[offset+template.Fields["DestinationIP"] : offset+template.Fields["DestinationIP"]+4]).String(),
		},
		Layer4: &pb.Layer4{},
		Ts: &pb.StandardFlow_Timestamp{
			Timestamp: timestamppb.New(time.Unix(int64(binary.BigEndian.Uint32(data[offset+template.Fields["Timestamp"]:offset+template.Fields["Timestamp"]+4])), 0)),
		},
	}

	// Check the protocol field and determine if it is TCP or UDP
	if protocolOffset, exists := template.Fields["Protocol"]; exists && len(data) > offset+protocolOffset {
		protocol := data[offset+protocolOffset]
		if protocol == 6 { // 6 is the protocol number for TCP
			standardFlow.Layer4.Protocol = &pb.Layer4_Tcp{
				Tcp: &pb.TCP{
					SourcePort:      uint32(binary.BigEndian.Uint16(data[offset+template.Fields["SourcePort"] : offset+template.Fields["SourcePort"]+2])),
					DestinationPort: uint32(binary.BigEndian.Uint16(data[offset+template.Fields["DestinationPort"] : offset+template.Fields["DestinationPort"]+2])),
				},
			}
		} else if protocol, ok := template.Fields["Protocol"]; ok && data[offset+protocol] == 17 { // 17 is the protocol number for UDP
			standardFlow.Layer4.Protocol = &pb.Layer4_Udp{
				Udp: &pb.UDP{
					SourcePort:      uint32(binary.BigEndian.Uint16(data[offset+template.Fields["SourcePort"] : offset+template.Fields["SourcePort"]+2])),
					DestinationPort: uint32(binary.BigEndian.Uint16(data[offset+template.Fields["DestinationPort"] : offset+template.Fields["DestinationPort"]+2])),
				},
			}
		}

		return standardFlow, nil
	}

	return nil, nil
}
