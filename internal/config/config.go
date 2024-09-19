// Copyright 2024 Illumio, Inc. All Rights Reserved.

package config

type EnvironmentConfig struct {
	// Whether to skip TLS certificate verification when starting a stream.
	TlsSkipVerify bool
	// URL of the onboarding endpoint.
	OnboardingEndpoint string
	// URL of the token endpoint.
	TokenEndpoint string
	// Client ID for onboarding. "" if not specified, i.e. if the operator is not meant to onboard itself.
	OnboardingClientId string
	// Client secret for onboarding. "" if not specified, i.e. if the operator is not meant to onboard itself.
	OnboardingClientSecret string
	// K8s cluster secret name.
	ClusterCreds string
}
