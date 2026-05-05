// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/awsvpccni"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/cilium"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/falco"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/ovnk"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

// Verify FlowCollectorStreamFactory implements stream.StreamClientFactory.
var _ stream.StreamClientFactory = (*FlowCollectorStreamFactory)(nil)

// FlowCollectorStreamFactory wraps a CollectorFactory to implement StreamClientFactory.
// This allows flow collectors to be managed by the stream manager like other streams.
type FlowCollectorStreamFactory struct {
	Factory       CollectorFactory
	CollectorName string // e.g., "Cilium", "OVN-K", "Falco", "VPC-CNI"
}

// NewStreamClient creates a flow collector and wraps it as a StreamClient.
func (f *FlowCollectorStreamFactory) NewStreamClient(ctx context.Context, _ grpc.ClientConnInterface) (stream.StreamClient, error) {
	collector, err := f.Factory.NewCollector(ctx)
	if err != nil {
		return nil, err
	}

	return &flowCollectorAdapter{collector: collector}, nil
}

// Name returns the stream name for logging.
func (f *FlowCollectorStreamFactory) Name() string {
	if f.CollectorName != "" {
		return "FlowCollector-" + f.CollectorName
	}

	return "FlowCollector"
}

// flowCollectorAdapter wraps a Collector to implement stream.StreamClient.
type flowCollectorAdapter struct {
	collector Collector
}

// collectorFactoryFunc wraps a function to implement CollectorFactory.
// This allows subpackage factories (cilium, falco, ovnk, awsvpccni) to be used as CollectorFactory
// without importing the flows package (which would create an import cycle).
type collectorFactoryFunc func(ctx context.Context) (Collector, error)

func (f collectorFactoryFunc) NewCollector(ctx context.Context) (Collector, error) {
	return f(ctx)
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

// DetectFlowCollector determines which flow collector is available and returns its type, name, and factory.
// Detection happens once at startup in main.go.
func DetectFlowCollector(ctx context.Context, config CollectorConfig) (pb.FlowCollector, string, CollectorFactory) {
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

		// Wrap using collectorFactoryFunc to avoid import cycle
		// (cilium can't import flows, but its client structurally satisfies Collector)
		return pb.FlowCollector_FLOW_COLLECTOR_CILIUM, "Cilium", collectorFactoryFunc(func(ctx context.Context) (Collector, error) {
			return factory.NewCollector(ctx)
		})
	}

	// Check for OVN-Kubernetes
	if collector.IsOVNKDeployed(ctx, config.Logger, config.OVNKNamespace, clientset) {
		config.Logger.Info("Using OVN-Kubernetes flow collector")

		factory := &ovnk.Factory{
			Logger:             config.Logger,
			IPFIXCollectorPort: config.IPFIXCollectorPort,
			FlowSink:           flowSink,
		}

		return pb.FlowCollector_FLOW_COLLECTOR_OVNK, "OVN-K", collectorFactoryFunc(func(ctx context.Context) (Collector, error) {
			return factory.NewCollector(ctx)
		})
	}

	// Check for AWS VPC CNI (EKS standard clusters)
	if collector.IsAWSVPCCNIAvailable(ctx, config.Logger, clientset) {
		config.Logger.Info("Using AWS VPC CNI flow collector")

		// Check CRD and create ClusterNetworkPolicy for comprehensive flow logging
		discoveryClient := config.K8sClient.GetDiscoveryClient()
		dynamicClient := config.K8sClient.GetDynamicClient()

		if awsvpccni.IsCRDAvailable(config.Logger, discoveryClient) {
			if err := awsvpccni.EnsureFlowLoggingPolicy(ctx, config.Logger, dynamicClient); err != nil {
				config.Logger.Warn("Failed to create ClusterNetworkPolicy, flow logging may be limited",
					zap.Error(err))
			}
		} else {
			config.Logger.Warn("ClusterNetworkPolicy CRD not found - enable network policy in VPC CNI addon for comprehensive flow logging")
		}

		factory := &awsvpccni.Factory{
			Logger:       config.Logger,
			FlowSink:     flowSink,
			K8sClient:    clientset,
			PollInterval: config.AWSVPCCNIPollingInterval,
		}

		return pb.FlowCollector_FLOW_COLLECTOR_AWS_VPC_CNI, "AWS-VPC-CNI", collectorFactoryFunc(func(ctx context.Context) (Collector, error) {
			return factory.NewCollector(ctx)
		})
	}

	// Default to Falco
	config.Logger.Info("Using Falco flow collector")

	factory := &falco.Factory{
		Logger:   config.Logger,
		FlowSink: flowSink,
	}

	return pb.FlowCollector_FLOW_COLLECTOR_FALCO, "Falco", collectorFactoryFunc(func(ctx context.Context) (Collector, error) {
		return factory.NewCollector(ctx)
	})
}
