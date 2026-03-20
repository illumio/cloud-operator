// Copyright 2025 Illumio, Inc. All Rights Reserved.

package collector

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"time"

	netflows "github.com/netsampler/goflow2/decoders/netflow"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// OVNKFlow represents a flow captured from OVN-Kubernetes.
type OVNKFlow struct {
	SourceIP        string
	DestinationIP   string
	SourcePort      uint16
	DestinationPort uint16
	Protocol        string
	IPVersion       string
	StartTimestamp  *timestamppb.Timestamp
	EndTimestamp    *timestamppb.Timestamp
}

// OVNKCollector collects IPFIX flows from OVN-Kubernetes.
type OVNKCollector struct {
	logger             *zap.Logger
	ipfixCollectorPort string
	flowSink           FlowSink
}

// https://datatracker.ietf.org/doc/rfc7011/
// The IPFIX Message Header 16-bit Length field limits the length of an
// IPFIX Message to 65535 octets, including the header.  A Collecting
// Process MUST be able to handle IPFIX Message lengths of up to
// 65535 octets.
const ipfixMessageMaxLength = 65535

// NewOVNKCollector creates a new OVN-K IPFIX collector.
func NewOVNKCollector(logger *zap.Logger, ipfixCollectorPort string, flowSink FlowSink) *OVNKCollector {
	return &OVNKCollector{
		logger:             logger,
		ipfixCollectorPort: ipfixCollectorPort,
		flowSink:           flowSink,
	}
}

// IsOVNKDeployed checks for the presence of the OVN-Kubernetes namespace.
// https://ovn-kubernetes.io/installation/launching-ovn-kubernetes-on-kind/#run-the-kind-deployment-with-podman
func IsOVNKDeployed(ctx context.Context, logger *zap.Logger, ovnkNamespace string, clientset kubernetes.Interface) bool {
	logger.Debug("Checking for OVN-K deployment")

	_, err := clientset.CoreV1().Namespaces().Get(ctx, ovnkNamespace, metav1.GetOptions{})
	if err != nil {
		logger.Debug("OVN-K namespace not found", zap.Error(err))

		return false
	}

	logger.Debug("OVN-K namespace found")

	return true
}

// StartIPFIXCollector starts a UDP listener for OVN-K IPFIX flows.
// It processes packets directly and handles context cancellation gracefully.
func (c *OVNKCollector) StartIPFIXCollector(ctx context.Context) error {
	logger := c.logger.With(
		zap.String("address", c.ipfixCollectorPort),
	)
	logger.Info("Starting OVN-K IPFIX collector")
	logger.Debug("Listening on IPFIX port")

	var listenerConfig net.ListenConfig

	listener, err := listenerConfig.ListenPacket(ctx, "udp", ":"+c.ipfixCollectorPort)
	if err != nil {
		logger.Fatal("Failed to listen on IPFIX port", zap.Error(err))
	}

	defer func() {
		err := listener.Close()
		if err != nil {
			logger.Fatal("Failed to close listener on IPFIX port", zap.Error(err))
		}
	}()

	logger.Info("OVN-K IPFIX collector listening")

	templateSystem, err := NewTemplateSystem(logger)
	if err != nil {
		logger.Error("Failed to create template set", zap.Error(err))

		return err
	}

	// Start a goroutine for blocking reads
	go func() {
		for {
			select {
			case <-ctx.Done():
				logger.Info("Context canceled in OVN-K IPFIX collector")

				return
			default:
			}

			buf := make([]byte, ipfixMessageMaxLength)

			n, _, err := listener.ReadFrom(buf)
			if err != nil {
				logger.Error("Failed to read from OVN-K listener", zap.Error(err))

				return
			}

			err = c.processIPFIXMessage(ctx, logger, buf[:n], templateSystem)
			if err != nil {
				logger.Error("Failed to process IPFIX message", zap.Error(err))
			}
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()
	logger.Info("Context canceled in OVN-K IPFIX collector")

	return ctx.Err()
}

// NewTemplateSystem creates a template system for IPFIX message.
// It reads the template set from a binary file and adds it to the template system.
func NewTemplateSystem(logger *zap.Logger) (*netflows.BasicTemplateSystem, error) {
	// Decode the template set and add it to the template system
	templateSystem := netflows.CreateTemplateSystem()

	packet, err := os.ReadFile("/ipfix-template-sets/openvswitch.bin")
	if err != nil {
		logger.Error("Failed to decode IPFIX template sets file", zap.Error(err))

		return nil, err
	}
	// By decoding this packet netflows package will add the template set to the template system
	_, err = netflows.DecodeMessage(bytes.NewBuffer(packet), templateSystem)
	if err != nil {
		return nil, err
	}

	return templateSystem, nil
}

// processIPFIXMessage decodes and processes a single IPFIX packet.
// Extracts data records, converts them into FiveTupleFlow objects, and caches them.
func (c *OVNKCollector) processIPFIXMessage(ctx context.Context, logger *zap.Logger, packet []byte, templateSystem *netflows.BasicTemplateSystem) error {
	decodedMessage, err := netflows.DecodeMessage(bytes.NewBuffer(packet), templateSystem)
	if err != nil {
		return err
	}

	ipFixPacket, ok := decodedMessage.(netflows.IPFIXPacket)
	if !ok {
		logger.Debug("Received a message that is not an IPFIX packet")

		return nil
	}

	for _, flowSet := range ipFixPacket.FlowSets {
		switch flowSet := flowSet.(type) {
		case netflows.DataFlowSet:
			for _, dataRecord := range flowSet.Records {
				ovnkFlow, err := ProcessDataRecord(dataRecord, ipFixPacket.ExportTime)
				if err != nil {
					logger.Debug("Skipping data record due to parsing error", zap.Error(err))

					continue
				}

				layer3Message, err := CreateLayer3Message(ovnkFlow.SourceIP, ovnkFlow.DestinationIP, ovnkFlow.IPVersion)
				if err != nil {
					logger.Debug("Failed to create layer3 message from OVN-K flow", zap.Error(err))

					continue
				}

				layer4Message, err := CreateLayer4Message(ovnkFlow.Protocol, uint32(ovnkFlow.SourcePort), uint32(ovnkFlow.DestinationPort), ovnkFlow.IPVersion)
				if err != nil {
					logger.Debug("Failed to create layer4 message from OVN-K flow", zap.Error(err))

					continue
				}

				convertOvnkFlow := &pb.FiveTupleFlow{
					Layer3: layer3Message,
					Layer4: layer4Message,
					Ts: &pb.FiveTupleFlow_Timestamp{
						Timestamp: ovnkFlow.StartTimestamp,
					},
				}

				err = c.flowSink.CacheFlow(ctx, convertOvnkFlow)
				if err != nil {
					logger.Error("Failed to cache flow from OVN-K", zap.Error(err))

					return err
				}

				c.flowSink.IncrementFlowsReceived()
			}
		default:
			// Ignore other types.
		}
	}

	return nil
}

// ParseIPv4Address converts a byte slice into an IPv4 address string.
// Returns an error if the slice is not the correct size.
func ParseIPv4Address(b []byte) (string, error) {
	if len(b) < 4 {
		return "", errors.New("insufficient data to parse IPv4 address")
	}

	ip := net.IPv4(b[0], b[1], b[2], b[3])

	return ip.String(), nil
}

// ParseIPv6Address converts a byte slice into an IPv6 address string.
// Returns an error if the slice is not the correct size.
func ParseIPv6Address(b []byte) (string, error) {
	if len(b) < 16 {
		return "", errors.New("insufficient data to parse IPv6 address")
	}

	ip := net.IP(b)
	if ip.To16() == nil {
		return "", errors.New("invalid IPv6 address")
	}

	return ip.String(), nil
}

// ParsePort converts a byte slice into a uint16 port number using BigEndian encoding.
// Returns an error if the slice is not the correct size.
func ParsePort(decodedValue []byte) (uint16, error) {
	if len(decodedValue) < 2 {
		return 0, errors.New("insufficient data to parse port")
	}

	return binary.BigEndian.Uint16(decodedValue), nil
}

// ParseProtocol converts a byte slice into a protocol string based on IANA protocol numbers.
// Returns an error if the slice is not the correct size or the protocol is unknown.
func ParseProtocol(decodedValue []byte) (string, error) {
	if len(decodedValue) < 1 {
		return "", errors.New("insufficient data to parse protocol")
	}

	switch decodedValue[0] {
	case 1:
		return ICMP, nil
	case 6:
		return TCP, nil
	case 17:
		return UDP, nil
	case 132:
		return SCTP, nil
	default:
		return "", errors.New("unknown protocol")
	}
}

// ParseIPVersion converts a byte slice into an IP version string (e.g., "ipv4" or "ipv6").
// Returns an error if the slice is not the correct size or the IP version is unknown.
func ParseIPVersion(decodedValue []byte) (string, error) {
	if len(decodedValue) < 1 {
		return "", errors.New("insufficient data to parse IP version")
	}

	switch decodedValue[0] {
	case 4:
		return IPv4, nil
	case 6:
		return IPv6, nil
	default:
		return "", errors.New("unknown IP version")
	}
}

// ProcessDataRecord processes a single data record and converts it into an OVNFlow.
// If any parsing step fails, it returns an error and skips the record.
func ProcessDataRecord(dataRecord netflows.DataRecord, exportTime uint32) (OVNKFlow, error) {
	ovnkFlow := OVNKFlow{}

	for _, field := range dataRecord.Values {
		fieldBytes, ok := field.Value.([]byte)
		if !ok {
			return OVNKFlow{}, errors.New("field value is not an array of bytes")
		}

		switch field.Type {
		case 8: // sourceIPv4Address
			sourceIP, err := ParseIPv4Address(fieldBytes)
			if err != nil {
				return OVNKFlow{}, err
			}

			ovnkFlow.SourceIP = sourceIP
		case 12: // destinationIPv4Address
			destinationIP, err := ParseIPv4Address(fieldBytes)
			if err != nil {
				return OVNKFlow{}, err
			}

			ovnkFlow.DestinationIP = destinationIP
		case 27: // sourceIPv6Address
			sourceIP, err := ParseIPv6Address(fieldBytes)
			if err != nil {
				return OVNKFlow{}, err
			}

			ovnkFlow.SourceIP = sourceIP
		case 28: // destinationIPv6Address
			destinationIP, err := ParseIPv6Address(fieldBytes)
			if err != nil {
				return OVNKFlow{}, err
			}

			ovnkFlow.DestinationIP = destinationIP
		case 7: // sourcePort
			sourcePort, err := ParsePort(fieldBytes)
			if err != nil {
				return OVNKFlow{}, err
			}

			ovnkFlow.SourcePort = sourcePort
		case 11: // destinationPort
			destinationPort, err := ParsePort(fieldBytes)
			if err != nil {
				return OVNKFlow{}, err
			}

			ovnkFlow.DestinationPort = destinationPort
		case 4: // protocol
			protocol, err := ParseProtocol(fieldBytes)
			if err != nil {
				return OVNKFlow{}, err
			}

			ovnkFlow.Protocol = protocol
		case 60: // ipVersion
			ipVersion, err := ParseIPVersion(fieldBytes)
			if err != nil {
				return OVNKFlow{}, err
			}

			ovnkFlow.IPVersion = ipVersion
		case 158: // flowStartDeltaMicroseconds
			startTimeDelta := binary.BigEndian.Uint32(fieldBytes)
			ovnkFlow.StartTimestamp = timestamppb.New(time.Unix(int64(exportTime), 0).Add(-time.Duration(startTimeDelta) * time.Microsecond))
		case 159: // flowEndDeltaMicroseconds
			endTimeDelta := binary.BigEndian.Uint32(fieldBytes)
			ovnkFlow.EndTimestamp = timestamppb.New(time.Unix(int64(exportTime), 0).Add(-time.Duration(endTimeDelta) * time.Microsecond))
		}
	}

	return ovnkFlow, nil
}
