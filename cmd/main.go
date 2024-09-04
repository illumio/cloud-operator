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
	"net/http"
	"reflect"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	"github.com/google/gops/agent"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	controller "github.com/illumio/cloud-operator/internal/controller"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
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
	http.HandleFunc("/healthz", healthHandler)

	errChan := make(chan error, 1)

	go func() {
		errChan <- http.ListenAndServe(":8080", nil)
	}()

	if err := <-errChan; err != nil {
		logger.Error(err, "healthz check server failed")
	}

	ctx := context.Background()
	controller.ExponentialStreamConnect(ctx, logger, varMap)
}
