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
	"reflect"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	"github.com/google/gops/agent"
	"github.com/spf13/viper"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	controller "github.com/illumio/cloud-operator/internal/controller"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	//+kubebuilder:scaffold:imports
)

func main() {
	var TlsSkipVerify bool
	var OnboardingEndpoint string
	var TokenEndpoint string
	var OnboardingClientId string
	var OnboardingClientSecret string
	var ClusterCreds string
	viper.AutomaticEnv()

	// Bind specific environment variables to keys
	viper.BindEnv("tls_skip_verify", "TLS_SKIP_VERIFY")
	viper.BindEnv("onboarding_endpoint", "ONBOARDING_ENDPOINT")
	viper.BindEnv("token_endpoint", "TOKEN_ENDPOINT")
	viper.BindEnv("onboarding_client_id", "ONBOARDING_CLIENT_ID")
	viper.BindEnv("onboarding_client_secret", "ONBOARDING_CLIENT_SECRET")
	viper.BindEnv("cluster_creds", "CLUSTER_CREDS_SECRET")

	// Set default values
	viper.SetDefault("tls_skip_verify", "true")
	viper.SetDefault("onboarding_endpoint", "https://192.168.65.254:50053/api/v1/k8s_cluster/onboard")
	viper.SetDefault("token_endpoint", "https://192.168.65.254:50053/api/v1/authenticate")
	viper.SetDefault("onboarding_client_id", "client_id_1")
	viper.SetDefault("onboarding_client_secret", "client_secret_1")
	viper.SetDefault("cluster_creds", "clustercreds")

	// Read environment variables
	TlsSkipVerify = viper.GetBool("tls_skip_verify")
	OnboardingEndpoint = viper.GetString("onboarding_endpoint")
	TokenEndpoint = viper.GetString("token_endpoint")
	OnboardingClientId = viper.GetString("onboarding_client_id")
	OnboardingClientSecret = viper.GetString("onboarding_client_secret")
	ClusterCreds = viper.GetString("cluster_creds")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

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

	logger := log.Log

	// Start the gops agent and listen on a specific address and port
	if err := agent.Listen(agent.Options{}); err != nil {
		logger.Error(err, "Failed to start gops agent")
	}

	ctx := context.Background()
	controller.ExponentialStreamConnect(ctx, logger, varMap)
}
