/*
Copyright 2024 Illumio, Inc. All Rights Reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-logr/zapr"
	"github.com/google/gops/agent"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/klog/v2"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/k8sclient"
	"github.com/illumio/cloud-operator/internal/controller/logging"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/controller/stream/config"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/cache"
	"github.com/illumio/cloud-operator/internal/controller/stream/logs"
	"github.com/illumio/cloud-operator/internal/controller/stream/resources"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

const (
	defaultPodNamespace = "illumio-cloud"

	defaultStatsLogPeriod = "30m"

	defaultStreamKeepalivePeriodConfiguration          = "10s"
	defaultStreamKeepalivePeriodKubernetesNetworkFlows = "10s"
	defaultStreamKeepalivePeriodKubernetesResources    = "10s"
	defaultStreamKeepalivePeriodLogs                   = "10s"

	defaultStreamSuccessPeriodAuth    = "2h"
	defaultStreamSuccessPeriodConnect = "1h"

	defaultFlowCacheActiveTimeout   = "20s"
	defaultFlowCacheMaxSize         = 1000
	defaultFlowCacheChannelBuffSize = 100
)

// newHealthHandler returns an HTTP HandlerFunc that checks the health of the
// server by calling the given function and returns a status code accordingly.
func newHealthHandler(checkFunc func() bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if checkFunc() {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

// bindEnv is a helper function that binds an environment variable to a key and handles errors.
func bindEnv(logger *zap.Logger, key, envVar string) {
	if err := viper.BindEnv(key, envVar); err != nil {
		logger.Error("Error binding environment variable",
			zap.Error(errors.New("error binding environment variable")),
			zap.String("variable", envVar),
		)
	}
}

func main() {
	// Create a buffered grpc write syncer without a valid gRPC connection initially
	// Using nil for the `pb.KubernetesInfoService_KubernetesLogsClient`.
	bufferedGrpcSyncer := logging.NewBufferedGrpcWriteSyncer()

	logger := logging.NewProductionGRPCLogger(bufferedGrpcSyncer)
	defer logger.Sync() //nolint:errcheck

	// Convert zap.Logger to logr.Logger
	logrLogger := zapr.NewLogger(logger)

	// Set logrLogger as the global logger for klog
	klog.SetLoggerWithOptions(logrLogger)

	viper.AutomaticEnv()

	// Bind specific environment variables to keys
	bindEnv(logger, "cilium_namespaces", "CILIUM_NAMESPACES")
	bindEnv(logger, "cluster_creds", "CLUSTER_CREDS_SECRET")
	bindEnv(logger, "cluster_name", "CLUSTER_NAME")
	bindEnv(logger, "cluster_region", "CLUSTER_REGION")
	bindEnv(logger, "flow_cache_active_timeout", "FLOW_CACHE_ACTIVE_TIMEOUT")
	bindEnv(logger, "flow_cache_channel_buffer_size", "FLOW_CACHE_CHANNEL_BUFFER_SIZE")
	bindEnv(logger, "flow_cache_max_size", "FLOW_CACHE_MAX_SIZE")
	bindEnv(logger, "grpc_internal_logging", "GRPC_INTERNAL_LOGGING")
	bindEnv(logger, "https_proxy", "HTTPS_PROXY")
	bindEnv(logger, "ipfix_collector_port", "IPFIX_COLLECTOR_PORT")
	bindEnv(logger, "onboarding_client_id", "ONBOARDING_CLIENT_ID")
	bindEnv(logger, "onboarding_client_secret", "ONBOARDING_CLIENT_SECRET")
	bindEnv(logger, "onboarding_endpoint", "ONBOARDING_ENDPOINT")
	bindEnv(logger, "ovnk_namespace", "OVNK_NAMESPACE")
	bindEnv(logger, "pod_namespace", "POD_NAMESPACE")
	bindEnv(logger, "stats_log_period", "STATS_LOG_PERIOD")
	bindEnv(logger, "stream_keepalive_period_configuration", "STREAM_KEEPALIVE_PERIOD_CONFIGURATION")
	bindEnv(logger, "stream_keepalive_period_kubernetes_network_flows", "STREAM_KEEPALIVE_PERIOD_KUBERNETES_NETWORK_FLOWS")
	bindEnv(logger, "stream_keepalive_period_kubernetes_resources", "STREAM_KEEPALIVE_PERIOD_KUBERNETES_RESOURCES")
	bindEnv(logger, "stream_keepalive_period_logs", "STREAM_KEEPALIVE_PERIOD_LOGS")
	bindEnv(logger, "stream_success_period_auth", "STREAM_SUCCESS_PERIOD_AUTH")
	bindEnv(logger, "stream_success_period_connect", "STREAM_SUCCESS_PERIOD_CONNECT")
	bindEnv(logger, "tls_skip_verify", "TLS_SKIP_VERIFY")
	bindEnv(logger, "token_endpoint", "TOKEN_ENDPOINT")
	bindEnv(logger, "verbose_debugging", "VERBOSE_DEBUGGING")

	// Set default values
	viper.SetDefault("cilium_namespaces", []string{"kube-system", "gke-managed-dpv2-observability"})
	viper.SetDefault("cluster_creds", "clustercreds")
	viper.SetDefault("flow_cache_active_timeout", defaultFlowCacheActiveTimeout)
	viper.SetDefault("flow_cache_channel_buffer_size", defaultFlowCacheChannelBuffSize)
	viper.SetDefault("flow_cache_max_size", defaultFlowCacheMaxSize)
	viper.SetDefault("grpc_internal_logging", false)
	viper.SetDefault("https_proxy", "")
	viper.SetDefault("ipfix_collector_port", "4739")
	viper.SetDefault("onboarding_endpoint", "https://dev.cloud.ilabs.io/api/v1/k8s_cluster/onboard")
	viper.SetDefault("ovnk_namespace", "openshift-ovn-kubernetes")
	viper.SetDefault("pod_namespace", defaultPodNamespace)
	viper.SetDefault("stats_log_period", defaultStatsLogPeriod)
	viper.SetDefault("stream_keepalive_period_configuration", defaultStreamKeepalivePeriodConfiguration)
	viper.SetDefault("stream_keepalive_period_kubernetes_network_flows", defaultStreamKeepalivePeriodKubernetesNetworkFlows)
	viper.SetDefault("stream_keepalive_period_kubernetes_resources", defaultStreamKeepalivePeriodKubernetesResources)
	viper.SetDefault("stream_keepalive_period_logs", defaultStreamKeepalivePeriodLogs)
	viper.SetDefault("stream_success_period_auth", defaultStreamSuccessPeriodAuth)
	viper.SetDefault("stream_success_period_connect", defaultStreamSuccessPeriodConnect)
	viper.SetDefault("tls_skip_verify", false)
	viper.SetDefault("token_endpoint", "https://dev.cloud.ilabs.io/api/v1/k8s_cluster/authenticate")
	viper.SetDefault("verbose_debugging", false)

	if viper.GetBool("grpc_internal_logging") {
		logging.SetupGRPCInternalLogging(logger)
	}

	envConfig := stream.Config{
		ClusterCreds:           viper.GetString("cluster_creds"),
		ClusterName:            viper.GetString("cluster_name"),
		ClusterRegion:          viper.GetString("cluster_region"),
		HttpsProxy:             viper.GetString("https_proxy"),
		OnboardingClientID:     viper.GetString("onboarding_client_id"),
		OnboardingClientSecret: viper.GetString("onboarding_client_secret"),
		OnboardingEndpoint:     viper.GetString("onboarding_endpoint"),
		PodNamespace:           viper.GetString("pod_namespace"),
		StatsLogPeriod:         viper.GetDuration("stats_log_period"),
		SuccessPeriods: stream.SuccessPeriods{
			Auth:    viper.GetDuration("stream_success_period_auth"),
			Connect: viper.GetDuration("stream_success_period_connect"),
		},
		TlsSkipVerify: viper.GetBool("tls_skip_verify"),
		TokenEndpoint: viper.GetString("token_endpoint"),
	}

	logger.Info("Starting application",
		zap.Strings("cilium_namespaces", viper.GetStringSlice("cilium_namespaces")),
		zap.String("cluster_creds_secret", envConfig.ClusterCreds),
		zap.String("cluster_name", envConfig.ClusterName),
		zap.String("cluster_region", envConfig.ClusterRegion),
		zap.String("https_proxy", envConfig.HttpsProxy),
		zap.String("ipfix_collector_port", viper.GetString("ipfix_collector_port")),
		zap.String("onboarding_client_id", envConfig.OnboardingClientID),
		zap.String("onboarding_endpoint", envConfig.OnboardingEndpoint),
		zap.String("ovnk_namespace", viper.GetString("ovnk_namespace")),
		zap.String("pod_namespace", envConfig.PodNamespace),
		zap.Duration("stats_log_period", envConfig.StatsLogPeriod),
		zap.Duration("stream_keepalive_period_configuration", viper.GetDuration("stream_keepalive_period_configuration")),
		zap.Duration("stream_keepalive_period_kubernetes_network_flows", viper.GetDuration("stream_keepalive_period_kubernetes_network_flows")),
		zap.Duration("stream_keepalive_period_kubernetes_resources", viper.GetDuration("stream_keepalive_period_kubernetes_resources")),
		zap.Duration("stream_keepalive_period_logs", viper.GetDuration("stream_keepalive_period_logs")),
		zap.Duration("stream_success_period_auth", envConfig.SuccessPeriods.Auth),
		zap.Duration("stream_success_period_connect", envConfig.SuccessPeriods.Connect),
		zap.Bool("tls_skip_verify", envConfig.TlsSkipVerify),
		zap.String("token_endpoint", envConfig.TokenEndpoint),
		zap.Bool("verbose_debugging", viper.GetBool("verbose_debugging")),
	)

	// Start the gops agent
	if err := agent.Listen(agent.Options{}); err != nil {
		logger.Error("Failed to start gops agent", zap.Error(err))
	}

	http.HandleFunc("/healthz", newHealthHandler(stream.ServerIsHealthy))

	healthChecker := &http.Server{
		Addr:              ":8080",
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Second,
	}

	errChan := make(chan error, 1)

	go func() {
		errChan <- healthChecker.ListenAndServe()

		err := <-errChan
		logger.Fatal("healthz check server failed", zap.Error(err))
	}()

	ctx := context.Background()

	// Create shared components
	stats := stream.NewStats()
	flowCache := cache.NewFlowCache(
		viper.GetDuration("flow_cache_active_timeout"),
		viper.GetInt("flow_cache_max_size"),
		make(chan pb.Flow, viper.GetInt("flow_cache_channel_buffer_size")),
	)

	// Create TlsAuthProps once - persists DisableTLS/DisableALPN flags across reconnections
	tlsAuthProps := &tls.AuthProperties{}

	// Create Kubernetes client
	k8sClient, err := k8sclient.NewClient()
	if err != nil {
		logger.Fatal("Failed to create Kubernetes client", zap.Error(err))
	}

	// Detect flow collector type at startup
	flowCollectorType, flowCollectorName, flowCollectorFactory := flows.DetectFlowCollector(ctx, flows.CollectorConfig{
		Logger:             logger,
		FlowCache:          flowCache,
		Stats:              stats,
		K8sClient:          k8sClient,
		CiliumNamespaces:   viper.GetStringSlice("cilium_namespaces"),
		IPFIXCollectorPort: viper.GetString("ipfix_collector_port"),
		OVNKNamespace:      viper.GetString("ovnk_namespace"),
		TlsAuthProps:       tlsAuthProps,
	})

	// Create factory config with all stream factories
	factoryConfig := stream.FactoryConfig{
		Factories: []stream.ManagedFactory{
			{
				Factory: &config.Factory{
					Logger:             logger,
					VerboseDebugging:   viper.GetBool("verbose_debugging"),
					BufferedGrpcSyncer: bufferedGrpcSyncer,
				},
				KeepalivePeriod: viper.GetDuration("stream_keepalive_period_configuration"),
			},
			{
				Factory: &logs.Factory{
					Logger:             logger,
					BufferedGrpcSyncer: bufferedGrpcSyncer,
				},
				KeepalivePeriod: viper.GetDuration("stream_keepalive_period_logs"),
			},
			{
				Factory: &resources.Factory{
					Logger:            logger,
					Stats:             stats,
					K8sClient:         k8sClient,
					FlowCollectorType: flowCollectorType,
					ClusterName:       envConfig.ClusterName,
					ClusterRegion:     envConfig.ClusterRegion,
				},
				KeepalivePeriod: viper.GetDuration("stream_keepalive_period_kubernetes_resources"),
			},
			{
				Factory: &flows.NetworkFlowsFactory{
					Logger:    logger,
					FlowCache: flowCache,
					Stats:     stats,
				},
				KeepalivePeriod: viper.GetDuration("stream_keepalive_period_kubernetes_network_flows"),
			},
			{
				Factory: &flows.FlowCollectorStreamFactory{
					Factory:       flowCollectorFactory,
					CollectorName: flowCollectorName,
				},
				KeepalivePeriod: viper.GetDuration("stream_keepalive_period_kubernetes_network_flows"),
			},
		},
		Stats:          stats,
		SuccessPeriods: envConfig.SuccessPeriods,
		StatsLogPeriod: envConfig.StatsLogPeriod,
	}

	stream.ConnectStreams(ctx, logger, envConfig, factoryConfig)
}
