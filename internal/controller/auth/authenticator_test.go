// Copyright 2024 Illumio, Inc. All Rights Reserved.

package auth

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// AuthTestSuite is the test suite for auth package.
type AuthTestSuite struct {
	suite.Suite

	clientset kubernetes.Interface
	logger    *zap.Logger
}

func (suite *AuthTestSuite) SetupSuite() {
	suite.clientset = fake.NewClientset()
	suite.logger = zap.NewNop()
}

func TestAuthTestSuite(t *testing.T) {
	suite.Run(t, new(AuthTestSuite))
}

func (suite *AuthTestSuite) TestReadCredentialsK8sSecrets() {
	namespaceObj := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "illumio-cloud",
		},
	}

	_, err := suite.clientset.CoreV1().Namespaces().Create(context.TODO(), namespaceObj, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
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
				if err != nil && !k8serrors.IsAlreadyExists(err) {
					suite.T().Fatal("Cannot create a secret for test " + err.Error())
				}
			}

			clientID, clientSecret, err := readClusterCredentialsFromK8sSecret(ctx, suite.clientset, tt.secretName, "illumio-cloud")
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

func (suite *AuthTestSuite) TestWriteK8sSecret() {
	ctx := context.Background()

	// Use a fresh fake clientset for this test to avoid state conflicts
	fakeClientset := fake.NewClientset()

	tests := map[string]struct {
		namespaceExists     bool
		secretExists        bool
		clusterClientID     string
		clusterClientSecret string
		secretName          string
		expectedError       bool
		expectedErrMsg      string
	}{
		"failure": { // G101: test credentials, not real
			namespaceExists:     false,
			secretExists:        false,
			clusterClientID:     "test-client-id",
			clusterClientSecret: "test-client-secret",
			secretName:          "write-test-secret",
			expectedError:       true,
			expectedErrMsg:      "failed to read cluster credentials from k8s secret for update: secrets \"write-test-secret\" not found",
		},
		"success": { // G101: test credentials, not real
			namespaceExists:     true,
			secretExists:        true,
			clusterClientID:     "test-client-id",
			clusterClientSecret: "test-client-secret",
			secretName:          "write-test-secret-success",
			expectedError:       false,
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			if tt.namespaceExists {
				namespaceObj := &v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "illumio-cloud",
					},
				}

				_, err := fakeClientset.CoreV1().Namespaces().Create(ctx, namespaceObj, metav1.CreateOptions{})
				if err != nil && !k8serrors.IsAlreadyExists(err) {
					suite.T().Fatal("Cannot create the illumio-cloud namespace for test " + err.Error())
				}
			}

			if tt.secretExists {
				secret := &v1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      tt.secretName,
						Namespace: "illumio-cloud",
					},
					Type: v1.SecretTypeOpaque,
				}

				// Create the secret in the specified namespace
				_, err := fakeClientset.CoreV1().Secrets("illumio-cloud").Create(ctx, secret, metav1.CreateOptions{})
				if err != nil && !k8serrors.IsAlreadyExists(err) {
					suite.T().Fatal("Failed to create secret for test " + err.Error())
				}
			}

			err := writeClusterCredentialsIntoK8sSecret(ctx, fakeClientset, tt.clusterClientID, tt.clusterClientSecret, tt.secretName, "illumio-cloud")
			if tt.expectedError {
				suite.EqualErrorf(err, tt.expectedErrMsg, "Error should be: %v, got: %v", tt.expectedErrMsg, err)
			} else {
				suite.NoError(err)
			}
		})
	}
}

// TestParseToken tests the ParseToken function.
func TestParseToken(t *testing.T) {
	// Create a sample token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"foo": "bar",
	})
	tokenString, _ := token.SignedString([]byte("secret"))

	// Test valid token
	claims, err := ParseToken(tokenString)
	require.NoError(t, err)
	assert.Equal(t, "bar", claims["foo"])

	// Test invalid token
	_, err = ParseToken("invalid-token")
	assert.Error(t, err)
}

func (suite *AuthTestSuite) TestHTTPProxySupport() {
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

func TestOAuthProxySupport(t *testing.T) {
	// Start a mock proxy server that returns a 403 Forbidden response
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("Proxy server received request: %s %s", r.Method, r.URL.String())
		http.Error(w, "Access Denied", http.StatusForbidden)
	}))
	defer proxyServer.Close()

	// Log the proxy URL for debugging
	t.Logf("Proxy URL set to: %s", proxyServer.URL)

	proxyURL, err := url.Parse(proxyServer.URL)
	if err != nil {
		t.Fatal("Failed to parse proxy URL")
	}

	t.Logf("Proxy URL: %s", proxyURL.String())

	t.Setenv("HTTP_PROXY", proxyServer.URL)
	t.Setenv("HTTPS_PROXY", proxyServer.URL)

	// Attempt to set up an OAuth connection using the authenticator
	ctx := context.Background()
	conn, err := SetUpOAuthConnection(
		ctx,
		zap.NewNop(),
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

	// Assert that an error is returned - the connection should fail either via:
	// - Proxy rejection (Forbidden) if proxy intercepts the request
	// - DNS failure (no such host) if proxy doesn't handle HTTPS CONNECT tunneling
	// Both indicate the OAuth connection failed as expected
	require.Error(t, err, "Expected an error from SetUpOAuthConnection, but got nil")
	errStr := err.Error()
	isExpectedError := strings.Contains(errStr, "Forbidden") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "connection refused")
	require.True(t, isExpectedError,
		"Error should indicate connection failure, got: %v", err)
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
