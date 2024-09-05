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
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	"github.com/google/gops/agent"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	controller "github.com/illumio/cloud-operator/internal/controller"
	//+kubebuilder:scaffold:imports
)

// healthHandler checks the health of the server and returns a status code accordingly
func healthHandler(w http.ResponseWriter, r *http.Request) {
	if controller.ServerIsHealthy() {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
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
	// Create a development encoder config
	encoderConfig := zap.NewProductionEncoderConfig()

	// Create a JSON encoder
	encoder := zapcore.NewJSONEncoder(encoderConfig)

	// Create syncers for console output
	consoleSyncer := zapcore.AddSync(os.Stdout)

	// Initialize the atomic level
	atomicLevel := zap.NewAtomicLevelAt(zapcore.InfoLevel)

	// Create the core with the atomic level
	core := zapcore.NewTee(
		zapcore.NewCore(encoder, consoleSyncer, atomicLevel),
	)

	// Create a zap logger with the core
	logger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1)).Sugar()
	defer logger.Sync() //nolint:errcheck

	viper.AutomaticEnv()

	// Bind specific environment variables to keys
	bindEnv(*logger, "tls_skip_verify", "TLS_SKIP_VERIFY")
	bindEnv(*logger, "onboarding_endpoint", "ONBOARDING_ENDPOINT")
	bindEnv(*logger, "token_endpoint", "TOKEN_ENDPOINT")
	bindEnv(*logger, "onboarding_client_id", "ONBOARDING_CLIENT_ID")
	bindEnv(*logger, "onboarding_client_secret", "ONBOARDING_CLIENT_SECRET")
	bindEnv(*logger, "cluster_creds", "CLUSTER_CREDS_SECRET")

	// Set default values
	viper.SetDefault("tls_skip_verify", false)
	viper.SetDefault("onboarding_endpoint", "https://dev.cloud.ilabs.io/api/v1/k8s_cluster/onboard")
	viper.SetDefault("token_endpoint", "https://dev.cloud.ilabs.io/api/v1/k8s_cluster/authenticate")
	viper.SetDefault("cluster_creds", "clustercreds")

	envConfig := controller.EnvironmentConfig{
		TlsSkipVerify:          viper.GetBool("tls_skip_verify"),
		OnboardingEndpoint:     viper.GetString("onboarding_endpoint"),
		TokenEndpoint:          viper.GetString("token_endpoint"),
		OnboardingClientId:     viper.GetString("onboarding_client_id"),
		OnboardingClientSecret: viper.GetString("onboarding_client_secret"),
		ClusterCreds:           viper.GetString("cluster_creds"),
	}

	logger.Infow("Starting application",
		"tls_skip_verify", envConfig.TlsSkipVerify,
		"onboarding_endpoint", envConfig.OnboardingEndpoint,
		"token_endpoint", envConfig.TokenEndpoint,
		"onboarding_client_id", envConfig.OnboardingClientId,
		"cluster_creds_secret", envConfig.ClusterCreds,
	)

	// Start the gops agent and listen on a specific address and port
	if err := agent.Listen(agent.Options{}); err != nil {
		logger.Errorw("Failed to start gops agent", "error", err)
	}
	http.HandleFunc("/healthz", healthHandler)

	errChan := make(chan error, 1)

	go func() {
		errChan <- http.ListenAndServe(":8080", nil)
		logger.Error(errChan, "healthz check server failed")
	}()

	ctx := context.Background()
	controller.ExponentialStreamConnect(ctx, logger, envConfig)
}
