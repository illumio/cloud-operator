// Copyright 2025 Illumio, Inc. All Rights Reserved.

package controller

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"time"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	netflows "github.com/netsampler/goflow2/decoders/netflow"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
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
}

// https://datatracker.ietf.org/doc/rfc7011/
// The IPFIX Message Header 16-bit Length field limits the length of an
// IPFIX Message to 65535 octets, including the header.  A Collecting
// Process MUST be able to handle IPFIX Message lengths of up to
// 65535 octets.
const ipfixMessageMaxLength = 65535

// isOVNKDeployed checks for the presence of the `openshift-ovn-kubernetes` namespace, detection based on OVN-K namespace, this is configurable.
// https://ovn-kubernetes.io/installation/launching-ovn-kubernetes-on-kind/#run-the-kind-deployment-with-podman
func (sm *streamManager) isOVNKDeployed(ctx context.Context, logger *zap.Logger, ovnkNamespace string, clientset *kubernetes.Clientset) bool {
	logger.Debug("Checking for OVN-K deployment")
	_, err := clientset.CoreV1().Namespaces().Get(ctx, ovnkNamespace, metav1.GetOptions{})
	if err != nil {
		logger.Debug("OVN-K namespace not found", zap.Error(err))
		return false
	}
	logger.Debug("OVN-K namespace found")
	return true
}

// startOVNKIPFIXCollector starts a UDP listener for OVN-K IPFIX flows.
// It processes packets directly and handles context cancellation gracefully.
func (sm *streamManager) startOVNKIPFIXCollector(ctx context.Context, logger *zap.Logger) error {
	logger = logger.With(
		zap.String("address", sm.streamClient.IPFIXCollectorPort),
	)
	logger.Info("Starting OVN-K IPFIX collector")

	select {
	case <-ctx.Done():
		logger.Info("Context canceled in IPFIX collector")
		return ctx.Err()
	default:
		logger.Debug("Listening on IPFIX port")
		listener, err := net.ListenPacket("udp", sm.streamClient.IPFIXCollectorPort)
		if err != nil {
			logger.Fatal("Failed to listen on IPFIX port", zap.Error(err))
		}

		logger.Info("OVN-K IPFIX collector listening")
		templateSystem, err := createTemplateSystem(logger)
		if err != nil {
			logger.Error("Failed to create template set", zap.Error(err))
			return err
		}

		// Start a goroutine for blocking reads
		go func() {
			buf := make([]byte, ipfixMessageMaxLength)
			for {
				select {
				case <-ctx.Done():
					logger.Info("Context canceled in OVN-K IPFIX collector")
					return
				default:
				}
				n, _, err := listener.ReadFrom(buf)
				if err != nil {
					logger.Error("Failed to read from OVN-K listener", zap.Error(err))
					return
				}

				err = sm.processIPFIXMessages(ctx, logger, buf[:n], templateSystem)
				if err != nil {
					logger.Error("Failed to process IPFIX message", zap.Error(err))
					return
				}
			}
		}()

		// Wait for context cancellation
		<-ctx.Done()
		logger.Info("Context canceled in OVN-K IPFIX collector")
		listener.Close()
		return ctx.Err()
	}
}

// createTemplateSystem creates a template system for IPFIX messages.
// It reads the template set from a binary file and adds it to the template system.
func createTemplateSystem(logger *zap.Logger) (*netflows.BasicTemplateSystem, error) {
	// This segment of code is used to decode the template set and add it to the template system
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

// processIPFIXMessages decodes and processes a single IPFIX packet.
// Extracts data records, converts them into FiveTupleFlow objects, and caches them.
func (sm *streamManager) processIPFIXMessages(ctx context.Context, logger *zap.Logger, packet []byte, templateSystem *netflows.BasicTemplateSystem) error {
	decodedMessage, err := netflows.DecodeMessage(bytes.NewBuffer(packet), templateSystem)
	if err != nil {
		return err
	}

	ipFixPacket, ok := decodedMessage.(netflows.IPFIXPacket)
	if ok {
		for _, flowSet := range ipFixPacket.FlowSets {
			switch flowSet := flowSet.(type) {
			case netflows.DataFlowSet:
				for _, dataRecord := range flowSet.Records {
					ovnkFlow, err := processDataRecord(dataRecord, ipFixPacket.ExportTime)
					if err != nil {
						logger.Debug("Skipping data record due to parsing error", zap.Error(err))
						continue
					}
					layer3Message, err := createLayer3Message(ovnkFlow.SourceIP, ovnkFlow.DestinationIP, ovnkFlow.IPVersion)
					if err != nil {
						logger.Debug("Failed to create layer3 message from OVN-K flow", zap.Error(err))
						continue
					}
					layer4Message, err := createLayer4Message(ovnkFlow.Protocol, uint32(ovnkFlow.SourcePort), uint32(ovnkFlow.DestinationPort), ovnkFlow.IPVersion)
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
					err = sm.FlowCache.CacheFlow(ctx, convertOvnkFlow)
					if err != nil {
						logger.Error("Failed to cache flow from OVN-K flow", zap.Error(err))
						return err
					}
				}
			}
		}
	} else {
		logger.Debug("Received a message that is not an IPFIX packet")
	}
	return nil
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
func processDataRecord(dataRecord netflows.DataRecord, exportTime uint32) (OVNKFlow, error) {
	ovnkFlow := OVNKFlow{}
	for _, field := range dataRecord.Values {
		switch field.Type {
		case 8: // sourceIPv4Address
			sourceIP, err := parseIPv4Address(field.Value.([]byte))
			if err != nil {
				return OVNKFlow{}, err
			}
			ovnkFlow.SourceIP = sourceIP
		case 12: // destinationIPv4Address
			destinationIP, err := parseIPv4Address(field.Value.([]byte))
			if err != nil {
				return OVNKFlow{}, err
			}
			ovnkFlow.DestinationIP = destinationIP
		case 7: // sourcePort
			sourcePort, err := parsePort(field.Value.([]byte))
			if err != nil {
				return OVNKFlow{}, err
			}
			ovnkFlow.SourcePort = sourcePort
		case 11: // destinationPort
			destinationPort, err := parsePort(field.Value.([]byte))
			if err != nil {
				return OVNKFlow{}, err
			}
			ovnkFlow.DestinationPort = destinationPort
		case 4: // protocol
			protocol, err := parseProtocol(field.Value.([]byte))
			if err != nil {
				return OVNKFlow{}, err
			}
			ovnkFlow.Protocol = protocol
		case 60: // ipVersion
			ipVersion, err := parseIPVersion(field.Value.([]byte))
			if err != nil {
				return OVNKFlow{}, err
			}
			ovnkFlow.IPVersion = ipVersion
		case 158: // flowStartDeltaMicroseconds
			startTimeDelta := binary.BigEndian.Uint32(field.Value.([]byte))
			ovnkFlow.StartTimestamp = timestamppb.New(time.Unix(int64(exportTime), 0).Add(-time.Duration(startTimeDelta) * time.Microsecond))
			// Ignore unknown field types
		}
	}
	return ovnkFlow, nil
}
