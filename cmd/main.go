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

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	"github.com/google/gops/agent"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	controller "github.com/illumio/cloud-operator/internal/controller"
	//+kubebuilder:scaffold:imports
)

const (
	defaultStreamKeepalivePeriodKubernetesResources    = "10s"
	defaultStreamKeepalivePeriodKubernetesNetworkFlows = "10s"
	defaultStreamKeepalivePeriodLogs                   = "10s"
	defaultStreamKeepalivePeriodConfiguration          = "10s"
)

// newHealthHandler returns an HTTP HandlerFunc that checks the health of the server by calling the given function and returns a status code accordingly
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
	bufferedGrpcSyncer := controller.NewBufferedGrpcWriteSyncer()
	logger := controller.NewProductionGRPCLogger(bufferedGrpcSyncer)
	defer logger.Sync() //nolint:errcheck

	viper.AutomaticEnv()

	// Bind specific environment variables to keys
	bindEnv(logger, "cluster_creds", "CLUSTER_CREDS_SECRET")
	bindEnv(logger, "cilium_namespace", "CILIUM_NAMESPACE")
	bindEnv(logger, "onboarding_client_id", "ONBOARDING_CLIENT_ID")
	bindEnv(logger, "onboarding_client_secret", "ONBOARDING_CLIENT_SECRET")
	bindEnv(logger, "onboarding_endpoint", "ONBOARDING_ENDPOINT")
	bindEnv(logger, "token_endpoint", "TOKEN_ENDPOINT")
	bindEnv(logger, "tls_skip_verify", "TLS_SKIP_VERIFY")
	bindEnv(logger, "stream_keepalive_period_kubernetes_resources", "STREAM_KEEPALIVE_PERIOD_KUBERNETES_RESOURCES")
	bindEnv(logger, "stream_keepalive_period_kubernetes_network_flows", "STREAM_KEEPALIVE_PERIOD_KUBERNETES_NETWORK_FLOWS")
	bindEnv(logger, "stream_keepalive_period_logs", "STREAM_KEEPALIVE_PERIOD_LOGS")
	bindEnv(logger, "stream_keepalive_period_configuration", "STREAM_KEEPALIVE_PERIOD_CONFIGURATION")

	// Set default values
	viper.SetDefault("cluster_creds", "clustercreds")
	viper.SetDefault("cilium_namespace", "kube-system")
	viper.SetDefault("onboarding_endpoint", "https://dev.cloud.ilabs.io/api/v1/k8s_cluster/onboard")
	viper.SetDefault("token_endpoint", "https://dev.cloud.ilabs.io/api/v1/k8s_cluster/authenticate")
	viper.SetDefault("tls_skip_verify", false)
	viper.SetDefault("stream_keepalive_period_kubernetes_resources", defaultStreamKeepalivePeriodKubernetesResources)
	viper.SetDefault("stream_keepalive_period_kubernetes_network_flows", defaultStreamKeepalivePeriodKubernetesNetworkFlows)
	viper.SetDefault("stream_keepalive_period_logs", defaultStreamKeepalivePeriodLogs)
	viper.SetDefault("stream_keepalive_period_configuration", defaultStreamKeepalivePeriodConfiguration)

	envConfig := controller.EnvironmentConfig{
		ClusterCreds:           viper.GetString("cluster_creds"),
		CiliumNamespace:        viper.GetString("cilium_namespace"),
		OnboardingClientId:     viper.GetString("onboarding_client_id"),
		OnboardingClientSecret: viper.GetString("onboarding_client_secret"),
		OnboardingEndpoint:     viper.GetString("onboarding_endpoint"),
		TokenEndpoint:          viper.GetString("token_endpoint"),
		TlsSkipVerify:          viper.GetBool("tls_skip_verify"),
		KeepalivePeriods: controller.KeepalivePeriods{
			KubernetesResources:    viper.GetDuration("stream_keepalive_period_kubernetes_resources"),
			KubernetesNetworkFlows: viper.GetDuration("stream_keepalive_period_kubernetes_network_flows"),
			Logs:                   viper.GetDuration("stream_keepalive_period_logs"),
			Configuration:          viper.GetDuration("stream_keepalive_period_configuration"),
		},
	}

	logger.Info("Starting application",
		zap.String("cluster_creds_secret", envConfig.ClusterCreds),
		zap.String("cilium_namespace", envConfig.CiliumNamespace),
		zap.String("onboarding_client_id", envConfig.OnboardingClientId),
		zap.String("onboarding_endpoint", envConfig.OnboardingEndpoint),
		zap.String("token_endpoint", envConfig.TokenEndpoint),
		zap.Bool("tls_skip_verify", envConfig.TlsSkipVerify),
		zap.Duration("stream_keepalive_period_kubernetes_resources", envConfig.KeepalivePeriods.KubernetesResources),
		zap.Duration("stream_keepalive_period_kubernetes_network_flows", envConfig.KeepalivePeriods.KubernetesNetworkFlows),
		zap.Duration("stream_keepalive_period_logs", envConfig.KeepalivePeriods.Logs),
		zap.Duration("stream_keepalive_period_configuration", envConfig.KeepalivePeriods.Configuration),
	)

	// Start the gops agent and listen on a specific address and port
	if err := agent.Listen(agent.Options{}); err != nil {
		logger.Error("Failed to start gops agent", zap.Error(err))
	}
	http.HandleFunc("/healthz", newHealthHandler(controller.ServerIsHealthy))
	healthChecker := &http.Server{Addr: ":8080"}

	errChan := make(chan error, 1)

	go func() {
		errChan <- healthChecker.ListenAndServe()
		err := <-errChan
		logger.Fatal("healthz check server failed", zap.Error(err))
	}()

	ctx := context.Background()
	controller.ConnectStreams(ctx, logger, envConfig, bufferedGrpcSyncer)
}
