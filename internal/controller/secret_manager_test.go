// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"log"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func (suite *ControllerTestSuite) TestGetOnboardingCredentials() {
	ctx := context.Background()
	zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
	logger := zapLogger.WithName("test")

	tests := map[string]struct {
		clientID      string
		clientSecret  string
		expectedError bool
	}{
		"success":             {"test-client-id", "test-client-secret", false},
		"empty-client-id":     {"", "test-client-secret", true},
		"empty-client-secret": {"test-client-id", "", true},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			sm := &SecretManager{Logger: logger}
			creds, err := sm.GetOnboardingCredentials(ctx, tt.clientID, tt.clientSecret)
			if tt.expectedError {
				assert.Error(suite.T(), err)
				assert.Equal(suite.T(), Credentials{}, creds)
			} else {
				assert.NoError(suite.T(), err)
				assert.Equal(suite.T(), tt.clientID, creds.ClientID)
				assert.Equal(suite.T(), tt.clientSecret, creds.ClientSecret)
			}
		})
	}
}

func (suite *ControllerTestSuite) TestReadCredentialsK8sSecrets() {
	namespaceObj := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "illumio-cloud",
		},
	}
	_, err := suite.clientset.CoreV1().Namespaces().Create(context.TODO(), namespaceObj, metav1.CreateOptions{})
	if err != nil {
		suite.T().Fatal("Cannot create the illumio-cloud namespace for test " + err.Error())
	}
	ctx := context.Background()
	zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
	logger := zapLogger.WithName("test")

	tests := map[string]struct {
		secretName           string
		secretData           map[string][]byte
		expectedError        bool
		expectedErrMsg       string
		expectedClientID     string
		expectedClientSecret string
	}{
		"success": {
			secretName: "test-secret",
			secretData: map[string][]byte{
				"client_id":     []byte("test-client-id"),
				"client_secret": []byte("test-client-secret"),
			},
			expectedError:        false,
			expectedClientID:     "test-client-id",
			expectedClientSecret: "test-client-secret",
		},
		"secret-not-found": {
			secretName:     "non-existent-secret",
			expectedError:  true,
			expectedErrMsg: "secrets \"non-existent-secret\" not found",
		},
		"client-id-not-found": {
			secretName: "test-secret-no-client-id",
			secretData: map[string][]byte{
				"client_secret": []byte("test-client-secret"),
			},
			expectedError:  true,
			expectedErrMsg: "failed to get client_id from secret",
		},
		"client-secret-not-found": {
			secretName: "test-secret-no-client-secret-id",
			secretData: map[string][]byte{
				"client_id": []byte("test-client-id"),
			},
			expectedError:  true,
			expectedErrMsg: "failed to get client_secret from secret",
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			sm := &SecretManager{Logger: logger}

			if tt.secretData != nil {
				secret := &v1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name: tt.secretName,
					},
					Data: tt.secretData,
				}
				_, err := suite.clientset.CoreV1().Secrets("illumio-cloud").Create(context.TODO(), secret, metav1.CreateOptions{})
				if err != nil {
					suite.T().Fatal("Cannot create a secret for test " + err.Error())
				}
			}

			clientID, clientSecret, err := sm.ReadCredentialsK8sSecrets(ctx, tt.secretName)
			if tt.expectedError {
				assert.Error(suite.T(), err)
				assert.EqualErrorf(suite.T(), err, tt.expectedErrMsg, "Error should be: %v, got: %v", tt.expectedErrMsg, err)
				assert.Empty(suite.T(), clientID)
				assert.Empty(suite.T(), clientSecret)
			} else {
				assert.NoError(suite.T(), err)
				assert.Equal(suite.T(), tt.expectedClientID, clientID)
				assert.Equal(suite.T(), tt.expectedClientSecret, clientSecret)
			}
		})
	}
}

func (suite *ControllerTestSuite) TestWriteK8sSecret() {
	ctx := context.Background()
	zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
	logger := zapLogger.WithName("test")

	tests := map[string]struct {
		namespaceExists bool
		secretExists    bool
		onboardResponse OnboardResponse
		secretName      string
		expectedError   bool
		expectedErrMsg  string
	}{
		"failure": {
			namespaceExists: false,
			secretExists:    false,
			onboardResponse: OnboardResponse{ClusterClientId: "test-client-id", ClusterClientSecret: "test-client-secret"},
			secretName:      "test-secret",
			expectedError:   true,
			expectedErrMsg:  "secrets \"test-secret\" not found",
		},
		"success": {
			namespaceExists: true,
			secretExists:    true,
			onboardResponse: OnboardResponse{ClusterClientId: "test-client-id", ClusterClientSecret: "test-client-secret"},
			secretName:      "test-secret",
			expectedError:   false,
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			sm := &SecretManager{Logger: logger}

			if tt.namespaceExists {
				namespaceObj := &v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "illumio-cloud",
					},
				}
				_, err := suite.clientset.CoreV1().Namespaces().Create(context.TODO(), namespaceObj, metav1.CreateOptions{})
				if err != nil && !errors.IsAlreadyExists(err) {
					suite.T().Fatal("Cannot create the illumio-cloud namespace for test " + err.Error())
				}
			}
			if tt.secretExists {
				secret := &v1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret",
						Namespace: "illumio-cloud",
					},
					Type: v1.SecretTypeOpaque,
				}

				// Create the secret in the specified namespace
				_, err := suite.clientset.CoreV1().Secrets("illumio-cloud").Create(context.TODO(), secret, metav1.CreateOptions{})
				if err != nil {
					log.Fatalf("Failed to create secret: %v", err)
				}
			}

			err := sm.WriteK8sSecret(ctx, tt.onboardResponse, tt.secretName)
			if tt.expectedError {
				assert.Error(suite.T(), err)
				assert.EqualErrorf(suite.T(), err, tt.expectedErrMsg, "Error should be: %v, got: %v", tt.expectedErrMsg, err)
			} else {
				assert.NoError(suite.T(), err)
			}
		})
	}
}
