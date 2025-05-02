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
func (sm *streamManager) isOVNDeployed(logger *zap.Logger) bool {
	logger.Debug("Checking for OVN deployment")
	clientset, err := NewClientSet()
	if err != nil {
		logger.Error("Failed to create clientset", zap.Error(err))
		return false
	}
	_, err = clientset.CoreV1().Namespaces().Get(context.TODO(), "openshift-ovn-kubernetes", metav1.GetOptions{})
	if err != nil {
		logger.Debug("OVN namespace not found", zap.Error(err))
		return false
	}
	logger.Debug("OVN namespace found")
	return true
}

func (sm *streamManager) startOVNIPFIXCollector(ctx context.Context, logger *zap.Logger) error {
	logger.Info("Starting OVN IPFIX Collector")
	go func() {
		ctxProcessOVNFlow, cancelProcessOVNFlow := context.WithCancel(ctx)
		defer cancelProcessOVNFlow()
		logger.Debug("Starting processOVNFlow goroutine")
		if err := sm.processOVNFlow(ctxProcessOVNFlow, logger); err != nil {
			logger.Error("Error in processOVNFlow", zap.Error(err))
		}
	}()
	select {
	case <-ctx.Done():
		logger.Info("Context canceled in startOVNIPFIXCollector")
		return ctx.Err()
	default:
		logger.Info("Creating custom listener for OVN flows")
		listener, err := net.ListenPacket("udp", ":4739")
		if err != nil {
			logger.Fatal("Failed to listen on OVN port", zap.String("address", sm.streamClient.IpfixCollectorPort), zap.Error(err))
		}

		logger.Info("OVN server listening", zap.String("address", sm.streamClient.IpfixCollectorPort))
		buf := make([]byte, 65535)
		for {
			select {
			case <-ctx.Done():
				logger.Info("Context canceled in OVN listener loop")
				return ctx.Err()
			default:
				n, _, err := listener.ReadFrom(buf)
				if err != nil {
					logger.Error("Failed to read from OVN listener", zap.Error(err))
					return err
				}
				logger.Info("Received data from OVN listener", zap.Int("bytesRead", n))
				sm.streamClient.ovnEventChan <- buf[:n]
			}
		}
	}
}

func (sm *streamManager) processOVNFlow(ctx context.Context, logger *zap.Logger) error {
	logger.Info("Starting processOVNFlow")
	for {
		select {
		case <-ctx.Done():
			logger.Info("Context canceled in processOVNFlow")
			return ctx.Err()
		case packet := <-sm.streamClient.ovnEventChan:
			logger.Info("Processing packet from ovnEventChan", zap.Int("packetLength", len(packet)))

			// Decode the packet for debugging
			decodePacket(packet, logger)

			if len(packet) < 2 {
				logger.Error("Packet too short to contain a template ID")
				continue
			}

			templateID := binary.BigEndian.Uint16(packet[:2])
			logger.Info("Extracted template ID", zap.Uint16("templateID", templateID))

			if templateID < 256 || templateID > 65535 {
				logger.Error("Invalid template ID", zap.Uint16("templateID", templateID))
				continue
			}

			convertedStandardFlow, err := ParseIPFIXMessageWithTemplate(packet, templateID, logger)
			if err != nil {
				logger.Error("Failed to parse IPFIX message", zap.Error(err))
				continue
			}
			logger.Info("Converted standard flow", zap.Any("flow", convertedStandardFlow))
			if err := sm.FlowCache.CacheFlow(ctx, convertedStandardFlow); err != nil {
				logger.Error("Failed to cache flow", zap.Error(err))
				return err
			}
			logger.Info("Successfully cached flow")
		}
	}
}

// ParseIPFIXMessageWithTemplate parses incoming bytes into a single IPFIXFlowRecord using predefined templates.
func ParseIPFIXMessageWithTemplate(data []byte, templateID uint16, logger *zap.Logger) (*pb.StandardFlow, error) {
	logger.Info("Parsing IPFIX message", zap.Int("dataLength", len(data)), zap.Uint16("templateID", templateID))
	template, exists := ipfixTemplates[templateID]
	if !exists {
		logger.Error("Unknown template ID", zap.Uint16("templateID", templateID))
		return nil, fmt.Errorf("unknown template ID: %d", templateID)
	}

	if len(data) < 16 {
		logger.Error("Data too short to contain IPFIX header", zap.Int("dataLength", len(data)))
		return nil, fmt.Errorf("data too short to contain IPFIX header")
	}

	header := IPFIXHeader{
		Length: binary.BigEndian.Uint16(data[2:4]),
	}

	if len(data) < int(header.Length) {
		logger.Error("Data length does not match header length", zap.Int("dataLength", len(data)), zap.Uint16("headerLength", header.Length))
		return nil, fmt.Errorf("data length (%d) does not match header length (%d)", len(data), header.Length)
	}

	offset := 16
	if len(data[offset:]) < 20 {
		logger.Error("Data too short to contain a flow record", zap.Int("remainingDataLength", len(data[offset:])))
		return nil, fmt.Errorf("data too short to contain a flow record")
	}

	logger.Info("Parsing flow record", zap.Int("offset", offset))
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

	if protocolOffset, exists := template.Fields["Protocol"]; exists && len(data) > offset+protocolOffset {
		protocol := data[offset+protocolOffset]
		logger.Info("Detected protocol", zap.Uint8("protocol", protocol))
		if protocol == 6 {
			standardFlow.Layer4.Protocol = &pb.Layer4_Tcp{
				Tcp: &pb.TCP{
					SourcePort:      uint32(binary.BigEndian.Uint16(data[offset+template.Fields["SourcePort"] : offset+template.Fields["SourcePort"]+2])),
					DestinationPort: uint32(binary.BigEndian.Uint16(data[offset+template.Fields["DestinationPort"] : offset+template.Fields["DestinationPort"]+2])),
				},
			}
		} else if protocol == 17 {
			standardFlow.Layer4.Protocol = &pb.Layer4_Udp{
				Udp: &pb.UDP{
					SourcePort:      uint32(binary.BigEndian.Uint16(data[offset+template.Fields["SourcePort"] : offset+template.Fields["SourcePort"]+2])),
					DestinationPort: uint32(binary.BigEndian.Uint16(data[offset+template.Fields["DestinationPort"] : offset+template.Fields["DestinationPort"]+2])),
				},
			}
		}
		logger.Info("Parsed flow record successfully")
		return standardFlow, nil
	}

	logger.Error("Protocol field not found in template")
	return nil, nil
}

func decodePacket(packet []byte, logger *zap.Logger) {
	logger.Info("Decoding packet", zap.Int("packetLength", len(packet)))

	// Log raw packet data in hexadecimal format
	logger.Info("Raw packet data (hex)", zap.String("hex", fmt.Sprintf("%x", packet)))

	if len(packet) < 16 {
		logger.Error("Packet too short to contain IPFIX header")
		return
	}

	// Decode IPFIX header
	version := binary.BigEndian.Uint16(packet[0:2])
	length := binary.BigEndian.Uint16(packet[2:4])
	exportTime := binary.BigEndian.Uint32(packet[4:8])
	sequenceNumber := binary.BigEndian.Uint32(packet[8:12])
	observationDomainID := binary.BigEndian.Uint32(packet[12:16])

	logger.Info("IPFIX Header",
		zap.Uint16("version", version),
		zap.Uint16("length", length),
		zap.Uint32("exportTime", exportTime),
		zap.Uint32("sequenceNumber", sequenceNumber),
		zap.Uint32("observationDomainID", observationDomainID),
	)

	// Ensure the packet length matches the header length
	if len(packet) < int(length) {
		logger.Error("Packet length does not match header length",
			zap.Int("packetLength", len(packet)),
			zap.Uint16("headerLength", length),
		)
		return
	}

	// Decode flow records
	offset := 16
	for offset < len(packet) {
		if len(packet[offset:]) < 4 {
			logger.Error("Remaining data too short to contain a flow record")
			break
		}

		// Example: Extract template ID (first 2 bytes of each record)
		templateID := binary.BigEndian.Uint16(packet[offset : offset+2])
		logger.Info("Flow Record",
			zap.Int("offset", offset),
			zap.Uint16("templateID", templateID),
		)

		// Move to the next record (adjust based on your template structure)
		offset += 4 // Adjust this based on the actual record size
	}
}
