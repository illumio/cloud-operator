package controller

import (
	"context"
	"net"

	"go.uber.org/zap"
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
		case <-sm.streamClient.ovnEventChan:
			// Process OVN flows here
			convertedStandardFlow, err := parsePodNetworkInfo("dummy_data") // Replace with actual logic
			if err != nil {
				logger.Error("Failed to parse OVN event into flow", zap.Error(err))
				return err
			}
			err = sm.FlowCache.CacheFlow(ctx, convertedStandardFlow)
			if err != nil {
				logger.Error("Failed to cache flow", zap.Error(err))
				return err
			}
		}
	}
}
