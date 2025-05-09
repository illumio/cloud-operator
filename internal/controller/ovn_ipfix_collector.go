package controller

import (
	"bytes"
	"context"
	"encoding/binary"
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

// OVNFlow represents a flow captured from OVN.
type OVNFlow struct {
	SourceIP        string
	DestinationIP   string
	SourcePort      uint16
	DestinationPort uint16
	Protocol        string
	IPVersion       string
}

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

// startOVNIPFIXCollector starts the OVN IPFIX collector.
// It creates a custom listener for OVN flows on port 4739 and processes
// the received flows in a separate goroutine.
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
			logger.Fatal("Failed to listen on OVN port", zap.Error(err))
		}

		logger.Info("OVN server listening", zap.String("address", sm.streamClient.IpfixCollectorPort))
		for {
			buf := make([]byte, 65535)
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
				sm.streamClient.ovnEventChan <- buf[:n]
			}
		}
	}
}

// processOVNFlow processes OVN IPFIX flows received from the ovnEventChan.
// It decodes the IPFIX packets, extracts data records, and converts them into
// StandardFlow objects, which are then cached for sending to CloudSecure.
func (sm *streamManager) processOVNFlow(ctx context.Context, logger *zap.Logger) error {
	// Initialize the BasicTemplateSystem using the CreateTemplateSystem function
	templateSystem := netflows.CreateTemplateSystem()
	// Intialize the template set via the binary observed
	for {
		select {
		case <-ctx.Done():
			logger.Info("Context canceled in processOVNFlow. Exiting.")
			return ctx.Err()
		case packet, ok := <-sm.streamClient.ovnEventChan:
			if !ok {
				logger.Info("ovnEventChan closed. Exiting processOVNFlow.")
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
			forceType, ok := decodedMessage.(netflows.IPFIXPacket)
			if ok {
				for _, flowSet := range forceType.FlowSets {
					switch flowSet := flowSet.(type) {
					case netflows.DataFlowSet:
						for _, dataRecord := range flowSet.Records {
							ovnFlow, err := processDataRecord(dataRecord)
							if err != nil {
								logger.Error("Failed to process data record", zap.Error(err))
								continue
							}
							layer4, err := createLayer4Message(ovnFlow.Protocol, uint32(ovnFlow.SourcePort), uint32(ovnFlow.DestinationPort), ovnFlow.IPVersion)
							if err != nil {
								logger.Error("Failed to create layer4 message", zap.Error(err))
								continue
							}
							layer3, err := createLayer3Message(ovnFlow.SourceIP, ovnFlow.DestinationIP, ovnFlow.IPVersion)
							if err != nil {
								logger.Error("Failed to create layer3 message", zap.Error(err))
								continue
							}
							convertOvnFlow := &pb.StandardFlow{
								Layer3: layer3,
								Layer4: layer4,
								Ts: &pb.StandardFlow_Timestamp{
									Timestamp: timestamppb.New(time.Now()),
								},
							}
							logger.Info("Converted OVN flow", zap.Any("ovnFlow", convertOvnFlow))
							err = sm.FlowCache.CacheFlow(ctx, convertOvnFlow)
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
func parseIPv4Address(decodedValue []byte) string {
	ip := net.IPv4(decodedValue[0], decodedValue[1], decodedValue[2], decodedValue[3])
	return ip.String()
}

// parsePort converts a byte slice into a uint16 port number using BigEndian encoding.
func parsePort(decodedValue []byte) uint16 {
	return binary.BigEndian.Uint16(decodedValue)
}

// parseProtocol converts a byte slice into a protocol string based on IANA protocol numbers.
func parseProtocol(decodedValue []byte) string {
	return convertProtocol(decodedValue)
}

// parseIPVersion converts a byte slice into an IP version string (e.g., "ipv4" or "ipv6").
func parseIPVersion(decodedValue []byte) string {
	return convertIpVersion(decodedValue)
}

// processDataRecord processes a single data record and converts it into an OVNFlow.
func processDataRecord(dataRecord netflows.DataRecord) (OVNFlow, error) {
	ovnFlow := OVNFlow{}
	for _, field := range dataRecord.Values {
		switch field.Type {
		case 8: // Assuming 8 corresponds to sourceIPv4Address
			ovnFlow.SourceIP = parseIPv4Address(field.Value.([]byte))
		case 12: // Assuming 12 corresponds to destinationIPv4Address
			ovnFlow.DestinationIP = parseIPv4Address(field.Value.([]byte))
		case 7: // Assuming 7 corresponds to sourcePort
			ovnFlow.SourcePort = parsePort(field.Value.([]byte))
		case 11: // Assuming 11 corresponds to destinationPort
			ovnFlow.DestinationPort = parsePort(field.Value.([]byte))
		case 4: // Assuming 4 corresponds to protocol
			ovnFlow.Protocol = parseProtocol(field.Value.([]byte))
		case 60: // Assuming 60 corresponds to ipVersion
			ovnFlow.IPVersion = parseIPVersion(field.Value.([]byte))
		default:
			// Ignore unknown field types
		}
	}
	return ovnFlow, nil
}

// convertIpVersion converts a byte slice into a string representation of the IP version.
// It uses IANA IP version numbers to map the byte value to a version name (e.g., "ipv4", "ipv6").
func convertIpVersion(decodedValue []byte) string {
	ipVersion := uint8(decodedValue[0])
	switch ipVersion {
	case 4:
		return "ipv4"
	case 6:
		return "ipv6"
	default:
		return "Unknown"
	}
}

// convertProtocol converts a byte slice into a string representation of the protocol.
// It uses IANA protocol numbers to map the byte value to a protocol name (e.g., "tcp", "udp").
func convertProtocol(decodedValue []byte) string {
	protocol := uint8(decodedValue[0])
	switch protocol {
	case 1:
		return "icmpt"
	case 6:
		return "tcp"
	case 17:
		return "udp"
	case 132:
		return "sctp"
	default:
		return "Unknown"
	}
}
