// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"

	"google.golang.org/grpc"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/cilium"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/falco"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/ovnk"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/vpccni"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

// Verify FlowCollectorStreamFactory implements stream.StreamClientFactory.
var _ stream.StreamClientFactory = (*FlowCollectorStreamFactory)(nil)

// FlowCollectorStreamFactory wraps a FlowCollectorFactory to implement StreamClientFactory.
// This allows flow collectors to be managed by the stream manager like other streams.
type FlowCollectorStreamFactory struct {
	Factory FlowCollectorFactory
}

// NewStreamClient creates a flow collector and wraps it as a StreamClient.
func (f *FlowCollectorStreamFactory) NewStreamClient(ctx context.Context, _ grpc.ClientConnInterface) (stream.StreamClient, error) {
	collector, err := f.Factory.NewFlowCollector(ctx)
	if err != nil {
		return nil, err
	}

	return &flowCollectorAdapter{collector: collector}, nil
}

// Name returns the stream name for logging.
func (f *FlowCollectorStreamFactory) Name() string {
	return "FlowCollector"
}

// flowCollectorAdapter wraps a FlowCollector to implement stream.StreamClient.
type flowCollectorAdapter struct {
	collector FlowCollector
}

func (a *flowCollectorAdapter) Run(ctx context.Context) error {
	return a.collector.Run(ctx)
}

func (a *flowCollectorAdapter) SendKeepalive(_ context.Context) error {
	return nil
}

func (a *flowCollectorAdapter) Close() error {
	return nil
}

// DetectFlowCollector determines which flow collector is available and returns its type and factory.
// Detection happens once at startup in main.go.
func DetectFlowCollector(ctx context.Context, config FlowCollectorConfig) (pb.FlowCollector, FlowCollectorFactory) {
	clientset := config.K8sClient.GetClientset()
	flowSink := NewFlowSinkAdapter(config.FlowCache, config.Stats)

	// Initialize TlsAuthProps if nil so DisableTLS/DisableALPN flags persist across retries
	tlsAuthProps := config.TlsAuthProps
	if tlsAuthProps == nil {
		tlsAuthProps = &tls.AuthProperties{}
	}

	// Check for Cilium/Hubble
	if collector.IsCiliumAvailable(ctx, config.Logger, clientset, config.CiliumNamespaces, *tlsAuthProps) {
		config.Logger.Info("Using Cilium flow collector")

		factory := &cilium.Factory{
			Logger:           config.Logger,
			FlowSink:         flowSink,
			CiliumNamespaces: config.CiliumNamespaces,
			TlsAuthProps:     tlsAuthProps,
			K8sClient:        config.K8sClient,
		}

		return pb.FlowCollector_FLOW_COLLECTOR_CILIUM, factory
	}

	// Check for OVN-Kubernetes
	if collector.IsOVNKDeployed(ctx, config.Logger, config.OVNKNamespace, clientset) {
		config.Logger.Info("Using OVN-Kubernetes flow collector")

		factory := &ovnk.Factory{
			Logger:             config.Logger,
			IPFIXCollectorPort: config.IPFIXCollectorPort,
			FlowSink:           flowSink,
		}

		return pb.FlowCollector_FLOW_COLLECTOR_OVNK, factory
	}

	// Check for AWS VPC CNI (EKS standard clusters)
	if config.EnableVPCCNI && collector.IsVPCCNIAvailable(ctx, config.Logger, clientset) {
		config.Logger.Info("Using AWS VPC CNI flow collector")

		factory := &vpccni.Factory{
			Logger:       config.Logger,
			FlowSink:     flowSink,
			K8sClient:    clientset,
			PollInterval: config.VPCCNIPollInterval,
		}

		return pb.FlowCollector_FLOW_COLLECTOR_VPC_CNI, factory
	}

	// Default to Falco
	config.Logger.Info("Using Falco flow collector")

	factory := &falco.Factory{
		Logger:   config.Logger,
		FlowSink: flowSink,
	}

	return pb.FlowCollector_FLOW_COLLECTOR_FALCO, factory
}
