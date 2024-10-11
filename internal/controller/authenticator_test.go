// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"crypto/tls"
	"net/http"
	"os"
	"testing"

	"github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (suite *ControllerTestSuite) TestGetOnboardingCredentials() {
	ctx := context.Background()
	// Create a development encoder config
	encoderConfig := zap.NewDevelopmentEncoderConfig()
	// Create a JSON encoder
	encoder := zapcore.NewJSONEncoder(encoderConfig)
	// Create syncers for console output
	consoleSyncer := zapcore.AddSync(os.Stdout)
	// Create the core with the atomic level
	core := zapcore.NewTee(
		zapcore.NewCore(encoder, consoleSyncer, zapcore.InfoLevel),
	)
	logger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1)).Sugar()
	logger = logger.With(zap.String("name", "test"))

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
			authn := &Authenticator{Logger: logger}
			creds, err := authn.GetOnboardingCredentials(ctx, tt.clientID, tt.clientSecret)
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
	// Create a development encoder config
	encoderConfig := zap.NewDevelopmentEncoderConfig()
	// Create a JSON encoder
	encoder := zapcore.NewJSONEncoder(encoderConfig)
	// Create syncers for console output
	consoleSyncer := zapcore.AddSync(os.Stdout)
	// Create the core with the atomic level
	core := zapcore.NewTee(
		zapcore.NewCore(encoder, consoleSyncer, zapcore.InfoLevel),
	)
	logger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1)).Sugar()
	logger = logger.With(zap.String("name", "test"))

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
			authn := &Authenticator{Logger: logger}

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

			clientID, clientSecret, err := authn.ReadCredentialsK8sSecrets(ctx, tt.secretName)
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
			authn := &Authenticator{Logger: suite.logger}

			// Since go test does not follow any order, always make sure namespace is deleted before each test
			_ = suite.clientset.CoreV1().Namespaces().Delete(context.TODO(), "illumio-cloud", metav1.DeleteOptions{})
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
					suite.T().Fatal("Failed to create secret for test " + err.Error())
				}
			}

			err := authn.WriteK8sSecret(ctx, tt.onboardResponse, tt.secretName)
			if tt.expectedError {
				assert.Error(suite.T(), err)
				assert.EqualErrorf(suite.T(), err, tt.expectedErrMsg, "Error should be: %v, got: %v", tt.expectedErrMsg, err)
			} else {
				assert.NoError(suite.T(), err)
			}
		})
	}
}

func TestIsRunningInCluster(t *testing.T) {
	t.Run("Running in cluster", func(t *testing.T) {
		os.Setenv("KUBERNETES_SERVICE_HOST", "localhost")
		defer os.Unsetenv("KUBERNETES_SERVICE_HOST")

		assert.True(t, IsRunningInCluster())
	})

	t.Run("Not running in cluster", func(t *testing.T) {
		os.Unsetenv("KUBERNETES_SERVICE_HOST")

		assert.False(t, IsRunningInCluster())
	})
}

func (suite *ControllerTestSuite) TestDoesK8sSecretExist() {
	ctx := context.Background()
	namespaceObj := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "illumio-cloud",
		},
	}
	_, err := suite.clientset.CoreV1().Namespaces().Create(context.TODO(), namespaceObj, metav1.CreateOptions{})
	if err != nil {
		suite.T().Fatal("Cannot create the illumio-cloud namespace for test " + err.Error())
	}
	tests := map[string]struct {
		secretExists  bool
		secretName    string
		expectedExist bool
	}{
		"secret exists": {
			secretExists:  true,
			secretName:    "existing-secret",
			expectedExist: true,
		},
		"secret does not exist": {
			secretExists:  false,
			secretName:    "nonexistent-secret",
			expectedExist: false,
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			if tt.secretExists {
				secret := &v1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      tt.secretName,
						Namespace: "illumio-cloud",
					},
					Type: v1.SecretTypeOpaque,
				}
				_, err := suite.clientset.CoreV1().Secrets("illumio-cloud").Create(ctx, secret, metav1.CreateOptions{})
				if err != nil {
					suite.T().Fatal("Failed to create secret for test " + err.Error())
				}
			}

			sm := &Authenticator{
				Logger: suite.logger,
			}

			exists := sm.DoesK8sSecretExist(ctx, tt.secretName)
			assert.Equal(suite.T(), tt.expectedExist, exists)
		})
	}
}

// TestGetTLSConfig tests the GetTLSConfig function.
func (suite *ControllerTestSuite) TestGetTLSConfig() {
	tlsConfig := GetTLSConfig(true)
	assert.Equal(suite.T(), tls.VersionTLS12, tlsConfig.MinVersion)
	assert.True(suite.T(), tlsConfig.InsecureSkipVerify)

	tlsConfig = GetTLSConfig(false)
	assert.Equal(suite.T(), tls.VersionTLS12, tlsConfig.MinVersion)
	assert.False(suite.T(), tlsConfig.InsecureSkipVerify)
}

// TestGetTokenSource tests the GetTokenSource function.
func (suite *ControllerTestSuite) TestGetTokenSource() {
	ctx := context.Background()
	config := clientcredentials.Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		TokenURL:     "https://example.com/token",
	}

	tlsConfig := GetTLSConfig(true)
	tokenSource := GetTokenSource(ctx, config, tlsConfig)

	client := oauth2.NewClient(ctx, tokenSource)
	transport, ok := client.Transport.(*http.Transport)
	assert.True(suite.T(), ok)
	assert.Equal(suite.T(), tlsConfig, transport.TLSClientConfig)
}

// TestParseToken tests the ParseToken function.
func (suite *ControllerTestSuite) TestParseToken() {
	// Create a sample token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"foo": "bar",
	})
	tokenString, _ := token.SignedString([]byte("secret"))

	// Test valid token
	claims, err := ParseToken(tokenString)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), "bar", claims["foo"])

	// Test invalid token
	_, err = ParseToken("invalid-token")
	assert.Error(suite.T(), err)
}

