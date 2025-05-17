// Copyright 2025 Illumio, Inc. All Rights Reserved.

package controller

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"time"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	netflows "github.com/netsampler/goflow2/decoders/netflow"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IPFIXHeader represents the header of an IPFIX message.
type IPFIXHeader struct {
	Version        uint16
	Length         uint16
	ExportTime     uint32
	SequenceNumber uint32
	ObservationID  uint32
}

// FieldInfo represents the offset and size of a field in a template.
type FieldInfo struct {
	Offset int
	Size   int
}

// OVNKFlow represents a flow captured from OVN-Kubernetes.
type OVNKFlow struct {
	SourceIP        string
	DestinationIP   string
	SourcePort      uint16
	DestinationPort uint16
	Protocol        string
	IPVersion       string
}

// https://datatracker.ietf.org/doc/rfc7011/
// The IPFIX Message Header 16-bit Length field limits the length of an
// IPFIX Message to 65535 octets, including the header.  A Collecting
// Process MUST be able to handle IPFIX Message lengths of up to
// 65535 octets.
const ipfixMaxLength = 65535

// isOVNKDeployed checks for the presence of the `openshift-ovn-kubernetes` namespace, detection based on OVN-K namespace, this is configurable.
// https://ovn-kubernetes.io/installation/launching-ovn-kubernetes-on-kind/#run-the-kind-deployment-with-podman
func (sm *streamManager) isOVNKDeployed(logger *zap.Logger, ovnkNamespace string) bool {
	logger.Debug("Checking for OVN-K deployment")
	clientset, err := NewClientSet()
	if err != nil {
		logger.Error("Failed to create clientset", zap.Error(err))
		return false
	}
	_, err = clientset.CoreV1().Namespaces().Get(context.TODO(), ovnkNamespace, metav1.GetOptions{})
	if err != nil {
		logger.Debug("OVN-K namespace not found", zap.Error(err))
		return false
	}
	logger.Debug("OVN-K namespace found")
	return true
}

// startOVNKIPFIXCollector starts the OVN-K IPFIX collector.
// It creates a custom listener for OVN-K flows on port 4739 and processes
// the received flows in a separate goroutine.
func (sm *streamManager) startOVNKIPFIXCollector(ctx context.Context, logger *zap.Logger) error {
	logger.Info("Starting OVN-K IPFIX Collector")
	go func() {
		ctxProcessOVNKFlow, cancelProcessOVNKFlow := context.WithCancel(ctx)
		defer cancelProcessOVNKFlow()
		logger.Debug("Starting processOVNKFlow goroutine")
		if err := sm.processOVNKFlow(ctxProcessOVNKFlow, logger); err != nil {
			logger.Error("Error in processOVNKFlow", zap.Error(err))
		}
	}()
	select {
	case <-ctx.Done():
		logger.Info("Context canceled in startOVNKIPFIXCollector")
		return ctx.Err()
	default:
		logger.Info("Creating custom listener for OVN-K flows")
		listener, err := net.ListenPacket("udp", ":4739")
		if err != nil {
			logger.Fatal("Failed to listen on OVN-K port", zap.Error(err))
		}

		logger.Info("OVN-k server listening", zap.String("address", sm.streamClient.IPFIXCollectorPort))
		for {
			buf := make([]byte, ipfixMaxLength)
			select {
			case <-ctx.Done():
				logger.Info("Context canceled in OVN-K listener loop")
				return ctx.Err()
			default:
				n, _, err := listener.ReadFrom(buf)
				if err != nil {
					logger.Error("Failed to read from OVN-K listener", zap.Error(err))
					return err
				}
				sm.streamClient.ovnkEventChan <- buf[:n]
			}
		}
	}
}

// processOVNKFlow processes OVN-K IPFIX flows received from the ovnkEventChan.
// It decodes the IPFIX packets, extracts data records, and converts them into
// FiveTupleFlow objects, which are then cached for sending to CloudSecure.
func (sm *streamManager) processOVNKFlow(ctx context.Context, logger *zap.Logger) error {
	// Initialize the BasicTemplateSystem using the CreateTemplateSystem function
	templateSystem := netflows.CreateTemplateSystem()
	// Intialize the template set via the binary observed
	for {
		select {
		case <-ctx.Done():
			logger.Info("Context canceled in processOVNKFlow. Exiting.")
			return ctx.Err()
		case packet, ok := <-sm.streamClient.ovnkEventChan:
			if !ok {
				logger.Info("ovnkEventChan closed. Exiting processOVNKFlow.")
				return nil
			}

			if len(packet) < 16 { // Min IPFIX header size
				logger.Warn("Packet too short for IPFIX header", zap.Int("length", len(packet)))
				continue
			}
			decodedMessage, err := netflows.DecodeMessage(bytes.NewBuffer(packet), templateSystem)
			if err != nil {
				continue
			}
			ipFixPacket, ok := decodedMessage.(netflows.IPFIXPacket)
			if ok {
				for _, flowSet := range ipFixPacket.FlowSets {
					switch flowSet := flowSet.(type) {
					case netflows.DataFlowSet:
						for _, dataRecord := range flowSet.Records {
							ovnkFlow, err := processDataRecord(dataRecord)
							if err != nil {
								logger.Warn("Skipping data record due to parsing error", zap.Error(err))
								continue
							}
							layer3Message, err := createLayer3Message(ovnkFlow.SourceIP, ovnkFlow.DestinationIP, ovnkFlow.IPVersion)
							if err != nil {
								logger.Error("Failed to create layer3 message", zap.Error(err))
								continue
							}
							layer4Message, err := createLayer4Message(ovnkFlow.Protocol, uint32(ovnkFlow.SourcePort), uint32(ovnkFlow.DestinationPort), ovnkFlow.IPVersion)
							if err != nil {
								logger.Error("Failed to create layer4 message", zap.Error(err))
								continue
							}
							convertOvnkFlow := &pb.FiveTupleFlow{
								Layer3: layer3Message,
								Layer4: layer4Message,
								Ts: &pb.FiveTupleFlow_Timestamp{
									Timestamp: timestamppb.New(time.Now()),
								},
							}
							err = sm.FlowCache.CacheFlow(ctx, convertOvnkFlow)
							if err != nil {
								logger.Error("Failed to cache flow", zap.Error(err))
								return err
							}
						}
					}
				}
			} else {
				logger.Info("Not an IPFIX packet")
			}
		}
	}
}

// parseIPv4Address converts a byte slice into an IPv4 address string.
// Returns an error if the slice is not the correct size.
func parseIPv4Address(decodedValue []byte) (string, error) {
	if len(decodedValue) < 4 {
		return "", errors.New("insufficient data to parse IPv4 address")
	}
	ip := net.IPv4(decodedValue[0], decodedValue[1], decodedValue[2], decodedValue[3])
	return ip.String(), nil
}

// parsePort converts a byte slice into a uint16 port number using BigEndian encoding.
// Returns an error if the slice is not the correct size.
func parsePort(decodedValue []byte) (uint16, error) {
	if len(decodedValue) < 2 {
		return 0, errors.New("insufficient data to parse port")
	}
	return binary.BigEndian.Uint16(decodedValue), nil
}

// parseProtocol converts a byte slice into a protocol string based on IANA protocol numbers.
// Returns an error if the slice is not the correct size or the protocol is unknown.
func parseProtocol(decodedValue []byte) (string, error) {
	if len(decodedValue) < 1 {
		return "", errors.New("insufficient data to parse protocol")
	}

	protocol := uint8(decodedValue[0])
	switch protocol {
	case 1:
		return "icmpt", nil
	case 6:
		return "tcp", nil
	case 17:
		return "udp", nil
	case 132:
		return "sctp", nil
	default:
		return "", errors.New("unknown protocol")
	}
}

// parseIPVersion converts a byte slice into an IP version string (e.g., "ipv4" or "ipv6").
// Returns an error if the slice is not the correct size or the IP version is unknown.
func parseIPVersion(decodedValue []byte) (string, error) {
	if len(decodedValue) < 1 {
		return "", errors.New("insufficient data to parse IP version")
	}

	ipVersion := uint8(decodedValue[0])
	switch ipVersion {
	case 4:
		return "ipv4", nil
	case 6:
		return "ipv6", nil
	default:
		return "", errors.New("unknown IP version")
	}
}

// processDataRecord processes a single data record and converts it into an OVNFlow.
// If any parsing step fails, it returns an error and skips the record.
func processDataRecord(dataRecord netflows.DataRecord) (OVNKFlow, error) {
	ovnkFlow := OVNKFlow{}
	for _, field := range dataRecord.Values {
		switch field.Type {
		case 8: // Assuming 8 corresponds to sourceIPv4Address
			sourceIP, err := parseIPv4Address(field.Value.([]byte))
			if err != nil {
				return OVNKFlow{}, err
			}
			ovnkFlow.SourceIP = sourceIP
		case 12: // Assuming 12 corresponds to destinationIPv4Address
			destinationIP, err := parseIPv4Address(field.Value.([]byte))
			if err != nil {
				return OVNKFlow{}, err
			}
			ovnkFlow.DestinationIP = destinationIP
		case 7: // Assuming 7 corresponds to sourcePort
			sourcePort, err := parsePort(field.Value.([]byte))
			if err != nil {
				return OVNKFlow{}, err
			}
			ovnkFlow.SourcePort = sourcePort
		case 11: // Assuming 11 corresponds to destinationPort
			destinationPort, err := parsePort(field.Value.([]byte))
			if err != nil {
				return OVNKFlow{}, err
			}
			ovnkFlow.DestinationPort = destinationPort
		case 4: // Assuming 4 corresponds to protocol
			protocol, err := parseProtocol(field.Value.([]byte))
			if err != nil {
				return OVNKFlow{}, err
			}
			ovnkFlow.Protocol = protocol
		case 60: // Assuming 60 corresponds to ipVersion
			ipVersion, err := parseIPVersion(field.Value.([]byte))
			if err != nil {
				return OVNKFlow{}, err
			}
			ovnkFlow.IPVersion = ipVersion
		default:
			// Ignore unknown field types
		}
	}
	return ovnkFlow, nil
}
