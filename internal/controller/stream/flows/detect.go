// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/awsautomode"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/awsvpccni"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/cilium"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/ovnk"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

// Verify FlowCollectorStreamFactory implements stream.StreamClientFactory.
var _ stream.StreamClientFactory = (*FlowCollectorStreamFactory)(nil)

// FlowCollectorStreamFactory wraps a CollectorFactory to implement StreamClientFactory.
// This allows flow collectors to be managed by the stream manager like other streams.
type FlowCollectorStreamFactory struct {
	Factory       CollectorFactory
	CollectorName string // e.g., "Cilium", "OVN-K", "Falco", "AWS-VPC-CNI"
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
				config.Logger.Warn("AWS VPC CNI failed to create ClusterNetworkPolicy, flow logging may be limited",
					zap.Error(err))
			}
		} else {
			config.Logger.Warn("ClusterNetworkPolicy CRD not found - enable network policy in AWS VPC CNI addon for comprehensive flow logging")
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
	// Check for EKS Auto Mode (node-proxy log polling).
	// Auto Mode has no aws-node DaemonSet, so IsAWSVPCCNIAvailable above is false;
	// the Network Policy Agent is AWS-managed and only its node-local log file is
	// reachable, through the kubelet node-proxy endpoint. This path activates only
	// when at least one node carries the eks.amazonaws.com/compute-type=auto label,
	// so we never infer Auto Mode from the mere absence of aws-node.
	if collector.IsEKSAutoModeAvailable(ctx, config.Logger, clientset) {
		config.Logger.Info("Using EKS Auto Mode flow collector (node-proxy log polling)")

		// Mark Auto Mode active so the eks_auto_mode_* stats are logged (they are
		// suppressed on other collectors, where they would always be zero).
		config.Stats.SetAutoModeActive()

		factory := &awsautomode.Factory{
			Logger:                   config.Logger,
			FlowSink:                 flowSink,
			K8sClient:                clientset,
			PollInterval:             config.AutoModePollInterval,
			MaxConcurrentNodePolls:   config.AutoModeMaxConcurrentNodePolls,
			LogPath:                  config.AutoModeLogPath,
			StatsAutoModeNodes:       config.Stats.SetAutoModeNodesObserved,
			StatsAutoModeErrors:      config.Stats.IncrementAutoModePollErrors,
			StatsRotationsDetected:   config.Stats.AddAutoModeRotationsDetected,
			StatsRotationRecovered:   config.Stats.IncrementAutoModeRotationRecoveries,
			StatsRotationRecoveryErr: config.Stats.IncrementAutoModeRotationRecoveryErrors,
			StatsRotationGap:         config.Stats.IncrementAutoModeRotationGaps,
		}

		return pb.FlowCollector_FLOW_COLLECTOR_AWS_VPC_CNI, "EKS-Auto-Mode", collectorFactoryFunc(func(ctx context.Context) (Collector, error) {
			return factory.NewCollector(ctx)
		})
	}

	// No supported flow exporter detected. Flow collection is disabled: the operator does not
	// fall back to any collector. A nil factory signals the caller not to register a
	// flow-collector stream. The reported type is FLOW_COLLECTOR_DISABLED so the
	// backend can surface the lack of flow visibility.
	config.Logger.Warn("No supported flow exporter detected; network flow collection is disabled. " +
		"Configure a supported CNI plugin (Cilium+Hubble, OVN-Kubernetes, or AWS VPC CNI) to enable flow visibility.")

	return pb.FlowCollector_FLOW_COLLECTOR_DISABLED, "None", nil
}
