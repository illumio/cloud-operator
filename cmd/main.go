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
	"flag"
	"os"
	"reflect"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	"github.com/google/gops/agent"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	controller "github.com/illumio/cloud-operator/internal/controller"
	//+kubebuilder:scaffold:imports
)

func main() {
	var TlsSkipVerify bool
	var OnboardingEndpoint string
	var TokenEndpoint string
	var OnboardingClientId string
	var OnboardingClientSecret string
	var ClusterCreds string

	flag.BoolVar(&TlsSkipVerify, "tls_skip_verify", true, "If set, TLS connections will verify the x.509 certificate")
	flag.StringVar(&OnboardingEndpoint, "onboarding_endpoint", "https://192.168.65.254:50053/api/v1/cluster/onboard", "The CloudSecure endpoint for onboarding this operator")
	flag.StringVar(&TokenEndpoint, "token_endpoint", "https://192.168.65.254:50053/api/v1/authenticate", "The CloudSecure endpoint to authenticate this operator")
	flag.StringVar(&OnboardingClientId, "onboarding_client_id", "client_id_1", "The client_id used to onboard this operator")
	flag.StringVar(&OnboardingClientSecret, "onboarding_client_secret", "client_secret_1", "The client_secret_id used to onboard this operator")
	flag.StringVar(&ClusterCreds, "cluster_creds_secret", "clustercreds", "The name of the Secret resource containing the OAuth 2 client credentials used to authenticate this operator after onboarding")
	flag.Parse()

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

	logger.Infow("Starting application",
		"tls_skip_verify", TlsSkipVerify,
		"onboarding_endpoint", OnboardingEndpoint,
		"token_endpoint", TokenEndpoint,
		"onboarding_client_id", OnboardingClientId,
		"cluster_creds_secret", ClusterCreds,
	)

	varMap := make(map[string]interface{})
	// Use reflection to get variable names and values
	v := reflect.ValueOf(map[string]interface{}{
		"TlsSkipVerify":          TlsSkipVerify,
		"OnboardingEndpoint":     OnboardingEndpoint,
		"TokenEndpoint":          TokenEndpoint,
		"OnboardingClientId":     OnboardingClientId,
		"OnboardingClientSecret": OnboardingClientSecret,
		"ClusterCreds":           ClusterCreds,
	})

	for _, key := range v.MapKeys() {
		varMap[key.String()] = v.MapIndex(key).Interface()
	}

	// Start the gops agent and listen on a specific address and port
	if err := agent.Listen(agent.Options{}); err != nil {
		logger.Errorw("Failed to start gops agent", "error", err)
	}

	ctx := context.Background()
	controller.ExponentialStreamConnect(ctx, logger, varMap)
}
