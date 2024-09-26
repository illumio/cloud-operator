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
func bindEnv(logger zap.SugaredLogger, key, envVar string) {
	if err := viper.BindEnv(key, envVar); err != nil {
		logger.Errorw("Error binding environment variable",
			"error", errors.New("error binding environment variable"),
			"variable", envVar,
		)
	}
}

func main() {
	// Create a buffered grpc write syncer without a valid gRPC connection initially
	// Using nil for the `pb.KubernetesInfoService_KubernetesLogsClient`.
	bufferedGrpcSyncer := controller.NewBufferedGrpcWriteSyncer()
	logger := controller.NewGRPClogger(bufferedGrpcSyncer)
	defer logger.Sync() //nolint:errcheck

	viper.AutomaticEnv()

	// Bind specific environment variables to keys
	bindEnv(*logger, "cluster_creds", "CLUSTER_CREDS_SECRET")
	bindEnv(*logger, "cilium_namespace", "CILIUM_NAMESPACE")
	bindEnv(*logger, "onboarding_client_id", "ONBOARDING_CLIENT_ID")
	bindEnv(*logger, "onboarding_client_secret", "ONBOARDING_CLIENT_SECRET")
	bindEnv(*logger, "onboarding_endpoint", "ONBOARDING_ENDPOINT")
	bindEnv(*logger, "token_endpoint", "TOKEN_ENDPOINT")
	bindEnv(*logger, "tls_skip_verify", "TLS_SKIP_VERIFY")

	// Set default values
	viper.SetDefault("cluster_creds", "clustercreds")
	viper.SetDefault("cilium_namespace", "kube-system")
	viper.SetDefault("onboarding_endpoint", "https://dev.cloud.ilabs.io/api/v1/k8s_cluster/onboard")
	viper.SetDefault("token_endpoint", "https://dev.cloud.ilabs.io/api/v1/k8s_cluster/authenticate")
	viper.SetDefault("tls_skip_verify", false)

	envConfig := controller.EnvironmentConfig{
		ClusterCreds:           viper.GetString("cluster_creds"),
		CiliumNamespace:        viper.GetString("cilium_namespace"),
		OnboardingClientId:     viper.GetString("onboarding_client_id"),
		OnboardingClientSecret: viper.GetString("onboarding_client_secret"),
		OnboardingEndpoint:     viper.GetString("onboarding_endpoint"),
		TokenEndpoint:          viper.GetString("token_endpoint"),
		TlsSkipVerify:          viper.GetBool("tls_skip_verify"),
	}

	logger.Infow("Starting application",
		"cluster_creds_secret", envConfig.ClusterCreds,
		"cilium_namespace", envConfig.CiliumNamespace,
		"onboarding_client_id", envConfig.OnboardingClientId,
		"onboarding_endpoint", envConfig.OnboardingEndpoint,
		"token_endpoint", envConfig.TokenEndpoint,
		"tls_skip_verify", envConfig.TlsSkipVerify,
	)

	// Start the gops agent and listen on a specific address and port
	if err := agent.Listen(agent.Options{}); err != nil {
		logger.Errorw("Failed to start gops agent", "error", err)
	}
	http.HandleFunc("/healthz", newHealthHandler(controller.ServerIsHealthy))
	errChan := make(chan error, 1)

	go func() {
		errChan <- http.ListenAndServe(":8080", nil)
		err := <-errChan
		logger.Fatal("healthz check server failed", zap.Error(err))
	}()

	ctx := context.Background()
	controller.ExponentialStreamConnect(ctx, logger, envConfig, bufferedGrpcSyncer)
}
