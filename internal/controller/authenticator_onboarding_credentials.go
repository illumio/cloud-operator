package controller

import "fmt"

type credentialNotFoundInK8sSecretError struct {
	RequiredFieldName onboardingCredentialRequiredField
}

type onboardingCredentialRequiredField string

// Credentials contains attributes that are needed for onboarding.
type Credentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

const (
	ONBOARDING_CLIENT_ID     = onboardingCredentialRequiredField("client_id")
	ONBOARDING_CLIENT_SECRET = onboardingCredentialRequiredField("client_secret")
)

var (
	// In the onboarding flow, an administrator gives cloud-operator credentials
	// via helm's value.yaml mechanism. For the sake of operability,
	// cloud-operator then persists these credentials into a k8s secret, so
	// subsequent installs on the same cluster do not require the administrator to
	// repeat the credentials every time. There are multiple specific fields in
	// this secret
	//
	// This error type indicates that at least one of the required fields is
	// missing from the secret.
	ErrCredentialNotFoundInK8sSecret error = &credentialNotFoundInK8sSecretError{}
)

func NewCredentialNotFoundInK8sSecretError(requiredField onboardingCredentialRequiredField) error {
	return &credentialNotFoundInK8sSecretError{
		RequiredFieldName: requiredField,
	}
}

func (e *credentialNotFoundInK8sSecretError) Error() string {
	return fmt.Sprintf("Required field not found in k8s Secret | field='%s'", e.RequiredFieldName)
}

func (e *credentialNotFoundInK8sSecretError) Is(target error) bool {
	return target == ErrCredentialNotFoundInK8sSecret
}
