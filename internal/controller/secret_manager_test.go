// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"testing"

	testhelper "github.com/illumio/cloud-operator/internal/controller/testhelper"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestGetOnboardingCredentials(t *testing.T) {
	ctx := context.Background()
	err := testhelper.SetupTestCluster()
	zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
	logger := zapLogger.WithName("test")
	if err != nil {
		panic("Failed to set up test cluster " + err.Error())
	}

	tests := []struct {
		name          string
		clientID      string
		clientSecret  string
		expectedError bool
	}{
		{"Success", "test-client-id", "test-client-secret", false},
		{"Empty ClientID", "", "test-client-secret", true},
		{"Empty ClientSecret", "test-client-id", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := &SecretManager{Logger: logger}
			creds, err := sm.GetOnboardingCredentials(ctx, tt.clientID, tt.clientSecret)
			if tt.expectedError {
				assert.Error(t, err)
				assert.Equal(t, Credentials{}, creds)
			} else {
				assert.NoError(t, err)
				clusterId, _ := GetClusterID(ctx, logger)
				assert.Equal(t, clusterId, creds.ClusterID)
				assert.Equal(t, tt.clientID, creds.ClientID)
				assert.Equal(t, tt.clientSecret, creds.ClientSecret)
			}
		})
	}

	err = testhelper.TearDownTestCluster()
	if err != nil {
		panic("Failed to delete test cluster " + err.Error())
	}
}

func TestReadCredentialsK8sSecrets(t *testing.T) {
	ctx := context.Background()
	err := testhelper.SetupTestCluster()
	zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
	logger := zapLogger.WithName("test")
	if err != nil {
		panic("Failed to set up test cluster " + err.Error())
	}
	clientset, err := NewClientSet()
	if err != nil {
		panic("Failed to get client set " + err.Error())
	}
	namespaceObj := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "illumio-cloud",
		},
	}
	_, err = clientset.CoreV1().Namespaces().Create(context.TODO(), namespaceObj, metav1.CreateOptions{})
	if err != nil {
		panic("Cannot create a namespace for test " + err.Error())
	}

	tests := []struct {
		name                 string
		secretName           string
		secretData           map[string][]byte
		expectedError        bool
		expectedErrMsg       string
		expectedClientID     string
		expectedClientSecret string
	}{
		{
			name:       "Success",
			secretName: "test-secret",
			secretData: map[string][]byte{
				"client_id":     []byte("test-client-id"),
				"client_secret": []byte("test-client-secret"),
			},
			expectedError:        false,
			expectedClientID:     "test-client-id",
			expectedClientSecret: "test-client-secret",
		},
		{
			name:           "SecretNotFound",
			secretName:     "non-existent-secret",
			expectedError:  true,
			expectedErrMsg: "secrets \"non-existent-secret\" not found",
		},
		{
			name:       "ClientIDNotFound",
			secretName: "test-secret-no-client-id",
			secretData: map[string][]byte{
				"client_secret": []byte("test-client-secret"),
			},
			expectedError:  true,
			expectedErrMsg: "failed to get client_id from secret",
		},
		{
			name:       "ClientSecretNotFound",
			secretName: "test-secret-no-client-secret-id",
			secretData: map[string][]byte{
				"client_id": []byte("test-client-id"),
			},
			expectedError:  true,
			expectedErrMsg: "failed to get client_secret from secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := &SecretManager{Logger: logger}

			if tt.secretData != nil {
				secret := &v1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name: tt.secretName,
					},
					Data: tt.secretData,
				}
				_, err := clientset.CoreV1().Secrets("illumio-cloud").Create(context.TODO(), secret, metav1.CreateOptions{})
				if err != nil {
					panic("Cannot create a secret for test " + err.Error())
				}
			}

			clientID, clientSecret, err := sm.ReadCredentialsK8sSecrets(ctx, tt.secretName)
			if tt.expectedError {
				assert.Error(t, err)
				assert.EqualErrorf(t, err, tt.expectedErrMsg, "Error should be: %v, got: %v", tt.expectedErrMsg, err)
				assert.Empty(t, clientID)
				assert.Empty(t, clientSecret)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedClientID, clientID)
				assert.Equal(t, tt.expectedClientSecret, clientSecret)
			}
		})
	}

	err = testhelper.TearDownTestCluster()
	if err != nil {
		panic("Failed to delete test cluster " + err.Error())
	}
}

func TestWriteK8sSecret(t *testing.T) {
	ctx := context.Background()
	err := testhelper.SetupTestCluster()
	zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
	logger := zapLogger.WithName("test")
	if err != nil {
		panic("Failed to set up test cluster " + err.Error())
	}
	clientset, err := NewClientSet()
	if err != nil {
		panic("Failed to get client set " + err.Error())
	}

	t.Run("Failure", func(t *testing.T) {
		expectedErrorMsg := "namespaces \"illumio-cloud\" not found"
		sm := &SecretManager{
			Logger: logger,
		}
		err := sm.WriteK8sSecret(ctx, OnboardResponse{ClusterClientId: "test-client-id", ClusterClientSecret: "test-client-secret"}, "test-secret")
		assert.EqualErrorf(t, err, expectedErrorMsg, "Error should be: %v, got: %v", expectedErrorMsg, err)
	})

	t.Run("Success", func(t *testing.T) {
		namespaceObj := &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "illumio-cloud",
			},
		}
		_, err := clientset.CoreV1().Namespaces().Create(context.TODO(), namespaceObj, metav1.CreateOptions{})
		if err != nil {
			panic("Cannot create a namespace for test " + err.Error())
		}
		sm := &SecretManager{
			Logger: logger,
		}
		err = sm.WriteK8sSecret(ctx, OnboardResponse{ClusterClientId: "test-client-id", ClusterClientSecret: "test-client-secret"}, "test-secret")
		assert.NoError(t, err)
	})
	err = testhelper.TearDownTestCluster()
	if err != nil {
		panic("Failed to delete test cluster " + err.Error())
	}
}
