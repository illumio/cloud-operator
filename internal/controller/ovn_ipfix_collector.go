package controller

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"

	"github.com/bio-routing/flowhouse/pkg/packet/ipfix"
	"go.uber.org/zap"
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

			if len(packet) < 2 {
				logger.Error("Packet too short to contain a template ID")
				continue
			}

			packetDecoded, err := ipfix.Decode(packet)
			if err != nil {
				logger.Error("Failed to decode IPFIX packet", zap.Error(err))
				logger.Debug("Raw packet data", zap.String("rawPacket", fmt.Sprintf("%x", packet)))
				continue
			}

			ipfix.PrintHeader(packetDecoded)
			logger.Info("Decoded IPFIX packet", zap.Any("packet", packetDecoded))

			for _, flowSet := range packetDecoded.DataFlowSets() {
				templateID := flowSet.Header.SetID
				template, exists := ipfixTemplates[templateID]
				if !exists {
					logger.Error("Template not found for SetID", zap.Uint16("templateID", templateID))
					continue
				}

				logger.Info("Using template to decode flow records", zap.Uint16("templateID", templateID))
				records := decodeFlowRecords(flowSet.Records, template, logger)
				for _, record := range records {
					logger.Info("Decoded flow record", zap.Any("record", record))
				}
			}
		}
	}
}

// decodeFlowRecords decodes raw flow records using the provided template.
func decodeFlowRecords(records []byte, template Template, logger *zap.Logger) []map[string]interface{} {
	var decodedRecords []map[string]interface{}
	offset := 0

	for offset < len(records) {
		record := make(map[string]interface{})
		for fieldName, fieldOffset := range template.Fields {
			fieldEnd := fieldOffset + 4 // Assuming all fields are 4 bytes for simplicity
			if fieldEnd > len(records) {
				logger.Error("Field exceeds record length", zap.String("fieldName", fieldName))
				break
			}

			// Interpret the field based on its name or type
			switch fieldName {
			case "SourceIP", "DestinationIP":
				record[fieldName] = net.IP(records[fieldOffset:fieldEnd]).String()
			case "SourcePort", "DestinationPort":
				record[fieldName] = binary.BigEndian.Uint16(records[fieldOffset:fieldEnd])
			default:
				// Default to base64 encoding for unknown fields
				record[fieldName] = fmt.Sprintf("%x", records[fieldOffset:fieldEnd])
			}
		}
		decodedRecords = append(decodedRecords, record)
		offset += len(template.Fields) * 4 // Assuming fixed field size
	}

	return decodedRecords
}
