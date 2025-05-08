package controller

import (
	"bytes"
	"context"
	"net"

	netflows "github.com/netsampler/goflow2/decoders/netflow"
	"go.uber.org/zap"
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
			logger.Fatal("Failed to listen on OVN port", zap.Error(err))
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
				// logger.Info("Received data from OVN listener", zap.Int("bytesRead", n))
				sm.streamClient.ovnEventChan <- buf[:n]
			}
		}
	}
}

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
				logger.Error("Failed to decode message", zap.Error(err))
				continue
			}
			forceType, ok := decodedMessage.(netflows.IPFIXPacket)
			if ok {
				for _, flowSet := range forceType.FlowSets {
					switch flowSet := flowSet.(type) {
					case netflows.DataFlowSet:
						for _, dataRecord := range flowSet.Records {
							for _, field := range dataRecord.Values {
								switch field.Type {
								// All of these assumptions are based on the IPFIX spec
								// https://github.com/netsampler/goflow2/blob/v1.3.7/decoders/netflow/ipfix.go#L8
								case 8: // Assuming 8 corresponds to sourceIPv4Address
									if srcIP, ok := field.Value.(net.IP); ok {
										logger.Info("Extracted source IP", zap.String("srcIP", srcIP.String()))
									}
								case 12: // Assuming 12 corresponds to destinationIPv4Address
									if dstIP, ok := field.Value.(net.IP); ok {
										logger.Info("Extracted destination IP", zap.String("dstIP", dstIP.String()))
									}
								case 7: // Assuming 7 corresponds to sourcePort
									if srcPort, ok := field.Value.(uint16); ok {
										logger.Info("Extracted source port", zap.Uint16("srcPort", srcPort))
									}
								case 11: // Assuming 11 corresponds to destinationPort
									if dstPort, ok := field.Value.(uint16); ok {
										logger.Info("Extracted destination port", zap.Uint16("dstPort", dstPort))
									}
								case 4: // Assuming 4 corresponds to protocol
									if protocol, ok := field.Value.(uint8); ok {
										logger.Info("Extracted protocol", zap.Uint8("protocol", protocol))
									}
								}
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
