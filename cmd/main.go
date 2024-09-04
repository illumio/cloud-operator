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

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	"github.com/go-logr/logr"
	"github.com/google/gops/agent"
	"github.com/spf13/viper"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	controller "github.com/illumio/cloud-operator/internal/controller"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	//+kubebuilder:scaffold:imports
)

// bindEnv is a helper function that binds an environment variable to a key and handles errors.
func bindEnv(logger logr.Logger, key, envVar string) {
	if err := viper.BindEnv(key, envVar); err != nil {
		logger.Error(errors.New("error binding environment variable"), "variable:", envVar)
	}
}

func main() {
	opts := zap.Options{
		Development: true,
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	logger := log.Log

	viper.AutomaticEnv()

	// Bind specific environment variables to keys
	bindEnv(logger, "tls_skip_verify", "TLS_SKIP_VERIFY")
	bindEnv(logger, "onboarding_endpoint", "ONBOARDING_ENDPOINT")
	bindEnv(logger, "token_endpoint", "TOKEN_ENDPOINT")
	bindEnv(logger, "onboarding_client_id", "ONBOARDING_CLIENT_ID")
	bindEnv(logger, "onboarding_client_secret", "ONBOARDING_CLIENT_SECRET")
	bindEnv(logger, "cluster_creds", "CLUSTER_CREDS_SECRET")

	// Set default values
	viper.SetDefault("tls_skip_verify", false)
	viper.SetDefault("onboarding_endpoint", "https://dev.cloud.ilabs.io/api/v1/k8s_cluster/onboard")
	viper.SetDefault("token_endpoint", "https://dev.cloud.ilabs.io/api/v1/authenticate")
	viper.SetDefault("cluster_creds", "clustercreds")

	envConfig := controller.EnvironmentConfig{
		TlsSkipVerify:          viper.GetBool("tls_skip_verify"),
		OnboardingEndpoint:     viper.GetString("onboarding_endpoint"),
		TokenEndpoint:          viper.GetString("token_endpoint"),
		OnboardingClientId:     viper.GetString("onboarding_client_id"),
		OnboardingClientSecret: viper.GetString("onboarding_client_secret"),
		ClusterCreds:           viper.GetString("cluster_creds"),
	}
	// Start the gops agent and listen on a specific address and port
	if err := agent.Listen(agent.Options{}); err != nil {
		logger.Error(err, "Failed to start gops agent")
	}

	ctx := context.Background()
	controller.ExponentialStreamConnect(ctx, logger, envConfig)
}
