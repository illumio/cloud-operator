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
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

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
				SecretFieldClientID:     []byte("test-client-id"),
				SecretFieldClientSecret: []byte("test-client-secret"),
			},
			expectedError:        false,
			expectedClientID:     "test-client-id",
			expectedClientSecret: "test-client-secret",
		},
		"secret-not-found": {
			secretName:     "non-existent-secret",
			expectedError:  true,
			expectedErrMsg: "failed to read cluster credentials from k8s secret: secrets \"non-existent-secret\" not found",
		},
		"client-id-not-found": {
			secretName: "test-secret-no-client-id",
			secretData: map[string][]byte{
				SecretFieldClientSecret: []byte("test-client-secret"),
			},
			expectedError:        false,
			expectedClientSecret: "test-client-secret",
		},
		"client-secret-not-found": {
			secretName: "test-secret-no-client-secret-id",
			secretData: map[string][]byte{
				SecretFieldClientID: []byte("test-client-id"),
			},
			expectedError:    false,
			expectedClientID: "test-client-id",
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			if tt.secretData != nil {
				secret := &v1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name: tt.secretName,
					},
					Data: tt.secretData,
				}

				_, err := suite.clientset.CoreV1().Secrets("illumio-cloud").Create(ctx, secret, metav1.CreateOptions{})
				if err != nil {
					suite.T().Fatal("Cannot create a secret for test " + err.Error())
				}
			}

			clientID, clientSecret, err := readClusterCredentialsFromK8sSecret(ctx, tt.secretName, "illumio-cloud")
			if tt.expectedError {
				suite.Require().EqualErrorf(err, tt.expectedErrMsg, "Error should be: %v, got: %v", tt.expectedErrMsg, err)
				suite.Empty(clientID)
				suite.Empty(clientSecret)
			} else {
				suite.Require().NoError(err)
				suite.Equal(tt.expectedClientID, clientID)
				suite.Equal(tt.expectedClientSecret, clientSecret)
			}
		})
	}
}

func (suite *ControllerTestSuite) TestWriteK8sSecret() { //nolint:gocognit
	ctx := context.Background()

	tests := map[string]struct {
		namespaceExists     bool
		secretExists        bool
		clusterClientID     string
		clusterClientSecret string
		secretName          string
		expectedError       bool
		expectedErrMsg      string
	}{
		"failure": {
			namespaceExists:     false,
			secretExists:        false,
			clusterClientID:     "test-client-id",
			clusterClientSecret: "test-client-secret",
			secretName:          "test-secret",
			expectedError:       true,
			expectedErrMsg:      "failed to read cluster credentials from k8s secret for update: secrets \"test-secret\" not found",
		},
		"success": {
			namespaceExists:     true,
			secretExists:        true,
			clusterClientID:     "test-client-id",
			clusterClientSecret: "test-client-secret",
			secretName:          "test-secret",
			expectedError:       false,
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			// Since go test does not follow any order, always make sure namespace is deleted before each test
			_ = suite.clientset.CoreV1().Namespaces().Delete(ctx, "illumio-cloud", metav1.DeleteOptions{})

			// Add polling logic to ensure namespace deletion
			for {
				_, err := suite.clientset.CoreV1().Namespaces().Get(ctx, "illumio-cloud", metav1.GetOptions{})
				if k8serrors.IsNotFound(err) {
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

				_, err := suite.clientset.CoreV1().Namespaces().Create(ctx, namespaceObj, metav1.CreateOptions{})
				if err != nil && !k8serrors.IsAlreadyExists(err) {
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
				_, err := suite.clientset.CoreV1().Secrets("illumio-cloud").Create(ctx, secret, metav1.CreateOptions{})
				if err != nil {
					suite.T().Fatal("Failed to create secret for test " + err.Error())
				}
			}

			err := writeClusterCredentialsIntoK8sSecret(ctx, tt.clusterClientID, tt.clusterClientSecret, tt.secretName, "illumio-cloud")
			if tt.expectedError {
				suite.EqualErrorf(err, tt.expectedErrMsg, "Error should be: %v, got: %v", tt.expectedErrMsg, err)
			} else {
				suite.NoError(err)
			}
		})
	}
}

func TestIsRunningInCluster(t *testing.T) {
	t.Run("Running in cluster", func(t *testing.T) {
		t.Setenv("KUBERNETES_SERVICE_HOST", "localhost")

		assert.True(t, IsRunningInCluster())
	})

	t.Run("Not running in cluster", func(t *testing.T) {
		err := os.Unsetenv("KUBERNETES_SERVICE_HOST")
		require.NoError(t, err)

		assert.False(t, IsRunningInCluster())
	})
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
	suite.Require().NoError(err)
	suite.Equal("bar", claims["foo"])

	// Test invalid token
	_, err = ParseToken("invalid-token")
	suite.Error(err)
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
		_, err := w.Write([]byte("Target server reached"))
		suite.NoErrorf(err, "Failed to write response: %v", err)
	}))
	defer targetServer.Close()

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec
	}

	proxyURL, err := url.Parse(proxyServer.URL)
	suite.Require().NoError(err, "Failed to parse proxy URL")

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: tlsConfig,
		Proxy:           http.ProxyURL(proxyURL), // Explicitly set the proxy
	}}

	// Log the proxy URL for debugging
	suite.T().Logf("Proxy URL: %s", proxyURL.String())

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, targetServer.URL, nil)
	suite.Require().NoError(err)

	resp, err := client.Do(req)
	suite.Require().NoError(err)
	suite.NotNil(resp)

	// Log the response body for debugging
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	suite.T().Logf("Response Body: %s", string(body))

	// Verify that the response came from the proxy
	suite.Equal("Proxy used", string(body))
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
		"https://something.invalid/token",
		true, // Skip TLS verification for testing
		"test-client-id",
		"test-client-secret",
	)

	// Ensure the connection object is closed if initialized
	if conn != nil {
		defer func() {
			_ = conn.Close()
		}()
	}

	// Assert that an error is returned due to the proxy
	suite.Require().Error(err, "Expected an error from SetUpOAuthConnection due to proxy failure, but got nil")
	suite.ErrorContains(err, "Access Denied", "Error message should indicate a proxy failure")
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

func TestClientBypassesProxy(t *testing.T) {
	// 1. Mock Servers (no change here)
	var proxyRequests int32

	mockProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&proxyRequests, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer mockProxy.Close()

	mockApiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		_, err := w.Write([]byte(`{"kind": "PodList", "items":[]}`))
		assert.NoError(t, err, "Failed to write response")
	}))
	defer mockApiServer.Close()

	// 2. Configure Environment (no change here)
	t.Setenv("HTTP_PROXY", mockProxy.URL)
	t.Setenv("HTTPS_PROXY", mockProxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	// --- VERIFY K8S CLIENT (no change here) ---
	config := &rest.Config{Host: mockApiServer.URL}

	k8sClient, err := newClientForConfig(config)
	require.NoError(t, err)

	_, err = k8sClient.CoreV1().Pods("default").List(context.TODO(), metav1.ListOptions{})
	require.NoError(t, err)

	// --- DEBUGGING PROXY FOR STANDARD CLIENT ---

	// Create the standard client with our logging proxy function
	stdClient := &http.Client{
		Transport: &http.Transport{
			// Manually implement the proxy logic, bypassing the faulty function.
			Proxy: func(req *http.Request) (*url.URL, error) {
				return url.Parse(os.Getenv("HTTP_PROXY"))
			},
		},
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, mockApiServer.URL, nil)
	require.NoError(t, err)

	resp, err := stdClient.Do(req)
	require.NoError(t, err)

	err = resp.Body.Close()
	require.NoError(t, err)

	assert.Equal(t, int32(1), atomic.LoadInt32(&proxyRequests))
}
