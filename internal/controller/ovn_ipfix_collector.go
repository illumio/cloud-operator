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

// OVNFlow represents a flow captured from OVN.
type OVNFlow struct {
	SourceIP        string
	DestinationIP   string
	SourcePort      uint16
	DestinationPort uint16
	Protocol        string
	IPVersion       string
}

const ovnNamespace = "openshift-ovn-kubernetes"

// IsOVNDeployed Checks for the presence of the `ovn-kubernetes` namespace, detection based on OVN namespace.
// https://ovn-kubernetes.io/installation/launching-ovn-kubernetes-on-kind/#run-the-kind-deployment-with-podman
func (sm *streamManager) isOVNDeployed(logger *zap.Logger) bool {
	logger.Debug("Checking for OVN deployment")
	clientset, err := NewClientSet()
	if err != nil {
		logger.Error("Failed to create clientset", zap.Error(err))
		return false
	}
	_, err = clientset.CoreV1().Namespaces().Get(context.TODO(), ovnNamespace, metav1.GetOptions{})
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
			ipFixPacket, ok := decodedMessage.(netflows.IPFIXPacket)
			if ok {
				for _, flowSet := range ipFixPacket.FlowSets {
					switch flowSet := flowSet.(type) {
					case netflows.DataFlowSet:
						for _, dataRecord := range flowSet.Records {
							ovnFlow, err := processDataRecord(dataRecord)
							if err != nil {
								logger.Warn("Skipping data record due to parsing error", zap.Error(err))
								continue
							}
							layer3Message, err := createLayer3Message(ovnFlow.SourceIP, ovnFlow.DestinationIP, ovnFlow.IPVersion)
							if err != nil {
								logger.Error("Failed to create layer3 message", zap.Error(err))
								continue
							}
							layer4Message, err := createLayer4Message(ovnFlow.Protocol, uint32(ovnFlow.SourcePort), uint32(ovnFlow.DestinationPort), ovnFlow.IPVersion)
							if err != nil {
								logger.Error("Failed to create layer4 message", zap.Error(err))
								continue
							}
							convertOvnFlow := &pb.StandardFlow{
								Layer3: layer3Message,
								Layer4: layer4Message,
								Ts: &pb.StandardFlow_Timestamp{
									Timestamp: timestamppb.New(time.Now()),
								},
							}
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
func processDataRecord(dataRecord netflows.DataRecord) (OVNFlow, error) {
	ovnFlow := OVNFlow{}
	for _, field := range dataRecord.Values {
		switch field.Type {
		case 8: // Assuming 8 corresponds to sourceIPv4Address
			sourceIP, err := parseIPv4Address(field.Value.([]byte))
			if err != nil {
				return OVNFlow{}, err
			}
			ovnFlow.SourceIP = sourceIP
		case 12: // Assuming 12 corresponds to destinationIPv4Address
			destinationIP, err := parseIPv4Address(field.Value.([]byte))
			if err != nil {
				return OVNFlow{}, err
			}
			ovnFlow.DestinationIP = destinationIP
		case 7: // Assuming 7 corresponds to sourcePort
			sourcePort, err := parsePort(field.Value.([]byte))
			if err != nil {
				return OVNFlow{}, err
			}
			ovnFlow.SourcePort = sourcePort
		case 11: // Assuming 11 corresponds to destinationPort
			destinationPort, err := parsePort(field.Value.([]byte))
			if err != nil {
				return OVNFlow{}, err
			}
			ovnFlow.DestinationPort = destinationPort
		case 4: // Assuming 4 corresponds to protocol
			protocol, err := parseProtocol(field.Value.([]byte))
			if err != nil {
				return OVNFlow{}, err
			}
			ovnFlow.Protocol = protocol
		case 60: // Assuming 60 corresponds to ipVersion
			ipVersion, err := parseIPVersion(field.Value.([]byte))
			if err != nil {
				return OVNFlow{}, err
			}
			ovnFlow.IPVersion = ipVersion
		default:
			// Ignore unknown field types
		}
	}
	return ovnFlow, nil
}
