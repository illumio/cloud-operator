// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
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
	logger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
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
	logger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
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
				string(ONBOARDING_CLIENT_ID):     []byte("test-client-id"),
				string(ONBOARDING_CLIENT_SECRET): []byte("test-client-secret"),
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
				string(ONBOARDING_CLIENT_SECRET): []byte("test-client-secret"),
			},
			expectedError:  true,
			expectedErrMsg: NewCredentialNotFoundInK8sSecretError(ONBOARDING_CLIENT_ID).Error(),
		},
		"client-secret-not-found": {
			secretName: "test-secret-no-client-secret-id",
			secretData: map[string][]byte{
				string(ONBOARDING_CLIENT_ID): []byte("test-client-id"),
			},
			expectedError:  true,
			expectedErrMsg: NewCredentialNotFoundInK8sSecretError(ONBOARDING_CLIENT_SECRET).Error(),
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

			clientID, clientSecret, err := authn.ReadCredentialsK8sSecrets(ctx, tt.secretName, "illumio-cloud")
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
			suite.SynchronousDeleteNamespace("illumio-cloud", 10*time.Second)

			// Add polling logic to ensure namespace deletion
			for {
				_, err := suite.clientset.CoreV1().Namespaces().Get(context.TODO(), "illumio-cloud", metav1.GetOptions{})
				if errors.IsNotFound(err) {
					break // Namespace is deleted
				}
				if err != nil {
					suite.T().Fatal("Error while checking namespace deletion: " + err.Error())
				}
				time.Sleep(100 * time.Millisecond) // Wait before retrying
			}

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

			err := authn.WriteK8sSecret(ctx, tt.onboardResponse, tt.secretName, "illumio-cloud")
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

			exists := sm.DoesK8sSecretExist(ctx, tt.secretName, "illumio-cloud")
			assert.Equal(suite.T(), tt.expectedExist, exists)
		})
	}
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

func (suite *ControllerTestSuite) TestHTTPProxySupport() {
	// Start a mock proxy server
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("Proxy used")); err != nil {
			suite.T().Errorf("Failed to write response: %v", err)
		}
	}))
	defer proxyServer.Close()

	// Use a mock target server
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("Target server reached")); err != nil {
			suite.T().Errorf("Failed to write response: %v", err)
		}
	}))
	defer targetServer.Close()

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	proxyURL, err := url.Parse(proxyServer.URL)
	if err != nil {
		suite.T().Fatal("Failed to parse proxy URL")
	}
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: tlsConfig,
		Proxy:           http.ProxyURL(proxyURL), // Explicitly set the proxy
	}}

	// Log the proxy URL for debugging
	suite.T().Logf("Proxy URL: %s", proxyURL.String())

	req, err := http.NewRequest("GET", targetServer.URL, nil)
	assert.NoError(suite.T(), err)

	resp, err := client.Do(req)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), resp)

	// Log the response body for debugging
	body, _ := io.ReadAll(resp.Body)
	suite.T().Logf("Response Body: %s", string(body))

	// Verify that the response came from the proxy
	assert.Equal(suite.T(), "Proxy used", string(body))
}

func (suite *ControllerTestSuite) TestGRPCProxySupport() {
	// Start a mock proxy server that returns a 403 Forbidden response
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		suite.T().Logf("Proxy server received request: %s %s", r.Method, r.URL.String())
		http.Error(w, "Access Denied", http.StatusForbidden)
	}))
	defer proxyServer.Close()

	// Log the proxy URL for debugging
	suite.T().Logf("Proxy URL set to: %s", proxyServer.URL)

	proxyURL, err := url.Parse(proxyServer.URL)
	if err != nil {
		suite.T().Fatal("Failed to parse proxy URL")

	}

	suite.T().Logf("Proxy URL: %s", proxyURL.String())

	// Attempt to set up an OAuth connection using the authenticator
	ctx := context.Background()
	conn, err := SetUpOAuthConnection(
		ctx,
		suite.logger,
		"https://example.com/token",
		true, // Skip TLS verification for testing
		"test-client-id",
		"test-client-secret",
	)

	// Ensure the connection object is closed if initialized
	if conn != nil {
		defer conn.Close()
	}

	// Assert that an error is returned due to the proxy
	if err == nil {
		suite.T().Log("No error returned from SetUpOAuthConnection")
		suite.T().Fatal("Expected an error due to proxy failure, but got nil")
	}
	assert.Contains(suite.T(), err.Error(), "Access Denied", "Error message should indicate a proxy failure")
}
func TestGetTLSConfig(t *testing.T) {
	tests := []struct {
		name          string
		skipVerify    bool
		expectedTLS12 uint16
	}{
		{
			name:          "SkipVerifyTrue",
			skipVerify:    true,
			expectedTLS12: tls.VersionTLS12,
		},
		{
			name:          "SkipVerifyFalse",
			skipVerify:    false,
			expectedTLS12: tls.VersionTLS12,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tlsConfig := GetTLSConfig(tt.skipVerify)

			// Test that the TLS version is set to 1.2 or higher
			assert.Equal(t, tt.expectedTLS12, tlsConfig.MinVersion)

			// Test the InsecureSkipVerify field
			assert.Equal(t, tt.skipVerify, tlsConfig.InsecureSkipVerify)
		})
	}
}

func TestCredentialNotFoundInK8sSecretError(t *testing.T) {
	tests := []struct {
		name          string
		field         onboardingCredentialRequiredField
		isTargetError bool
	}{
		{
			name:          "client id missing",
			field:         ONBOARDING_CLIENT_ID,
			isTargetError: true,
		},
		{
			name:          "client secret missing",
			field:         ONBOARDING_CLIENT_SECRET,
			isTargetError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewCredentialNotFoundInK8sSecretError(tt.field)

			// Test error type matching
			assert.Equal(t, tt.isTargetError, err.(*credentialNotFoundInK8sSecretError).Is(ErrCredentialNotFoundInK8sSecret))
		})
	}
}
