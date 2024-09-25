// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

func (suite *ControllerTestSuite) TestOnboard() {
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
		clientID         string
		clientSecret     string
		serverHandler    http.HandlerFunc
		requestURL       string
		expectedResponse OnboardResponse
		expectedError    bool
		expectedErrMsg   string
	}{
		"success": {
			clientID:     "test-client-id",
			clientSecret: "test-client-secret",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(suite.T(), "application/json", r.Header.Get("Content-Type"))

				var requestData map[string]string
				err := json.NewDecoder(r.Body).Decode(&requestData)
				assert.NoError(suite.T(), err)
				assert.Equal(suite.T(), "test-client-id", requestData["onboardingClientId"])
				assert.Equal(suite.T(), "test-client-secret", requestData["onboardingClientSecret"])

				w.Header().Set("Content-Type", "application/json")
				err = json.NewEncoder(w).Encode(OnboardResponse{
					ClusterClientId:     "test-client-id",
					ClusterClientSecret: "test-client-secret",
				})
				if err != nil {
					suite.T().Fatal("Failed to encode response in creds manager test " + err.Error())
				}
			},
			requestURL: "http://example.com",
			expectedResponse: OnboardResponse{
				ClusterClientId:     "test-client-id",
				ClusterClientSecret: "test-client-secret",
			},
			expectedError: false,
		},
		"request-url-error": {
			clientID:       "test-client-id",
			clientSecret:   "test-client-secret",
			serverHandler:  nil,
			requestURL:     "http://example.com/\x00",
			expectedError:  true,
			expectedErrMsg: "parse \"http://example.com/\\x00\": net/url: invalid control character in URL",
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			if tt.serverHandler != nil {
				server := httptest.NewServer(tt.serverHandler)
				defer server.Close()
				tt.requestURL = server.URL
			}

			am := &CredentialsManager{
				Credentials: Credentials{
					ClientID:     tt.clientID,
					ClientSecret: tt.clientSecret,
				},
				Logger: logger,
			}

			response, err := am.Onboard(ctx, true, tt.requestURL)
			if tt.expectedError {
				assert.Error(suite.T(), err)
				assert.EqualErrorf(suite.T(), err, tt.expectedErrMsg, "Error should be: %v, got: %v", tt.expectedErrMsg, err)
			} else {
				assert.NoError(suite.T(), err)
				assert.Equal(suite.T(), tt.expectedResponse, response)
			}
		})
	}
}

func (suite *ControllerTestSuite) TestGetFirstAudience() {
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
		claims         map[string]interface{}
		expected       string
		expectedError  bool
		expectedErrMsg string
	}{
		"audience claim not found": {
			claims:         map[string]interface{}{},
			expected:       "",
			expectedError:  true,
			expectedErrMsg: "audience claim not found",
		},
		"audience claim is not a slice": {
			claims: map[string]interface{}{
				"aud": "not a slice",
			},
			expected:       "",
			expectedError:  true,
			expectedErrMsg: "audience claim is not a slice",
		},
		"audience slice is empty": {
			claims: map[string]interface{}{
				"aud": []interface{}{},
			},
			expected:       "",
			expectedError:  true,
			expectedErrMsg: "audience slice is empty",
		},
		"first audience claim is not a string": {
			claims: map[string]interface{}{
				"aud": []interface{}{123},
			},
			expected:       "",
			expectedError:  true,
			expectedErrMsg: "first audience claim is not a string",
		},
		"valid audience claim": {
			claims: map[string]interface{}{
				"aud": []interface{}{"exampleAudience"},
			},
			expected:      "exampleAudience",
			expectedError: false,
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			got, err := getFirstAudience(logger, tt.claims)
			if tt.expectedError {
				assert.Error(suite.T(), err)
				assert.EqualErrorf(suite.T(), err, tt.expectedErrMsg, "Error should be: %v, got: %v", tt.expectedErrMsg, err)
			} else {
				assert.NoError(suite.T(), err)
				assert.Equal(suite.T(), tt.expected, got, "Expected audience: %v, got: %v", tt.expected, got)
			}
		})
	}
}

// MockTokenSource is a mock implementation of oauth2.TokenSource
type MockTokenSource struct {
	mock.Mock
}

func (m *MockTokenSource) Token() (*oauth2.Token, error) {
	args := m.Called()
	return args.Get(0).(*oauth2.Token), args.Error(1)
}

// MockGRPCClientConn is a mock implementation of grpc.ClientConnInterface
type MockGRPCClientConn struct {
	mock.Mock
}

// MockHTTPClient is a mock HTTP client for testing purposes.
type MockHTTPClient struct{}

func (m *MockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return nil, errors.New("mock error")
}

// TestGetTLSConfig tests the GetTLSConfig function.
func TestGetTLSConfig(t *testing.T) {
	tlsConfig := GetTLSConfig(true)
	assert.Equal(t, tls.VersionTLS12, tlsConfig.MinVersion)
	assert.True(t, tlsConfig.InsecureSkipVerify)

	tlsConfig = GetTLSConfig(false)
	assert.Equal(t, tls.VersionTLS12, tlsConfig.MinVersion)
	assert.False(t, tlsConfig.InsecureSkipVerify)
}

// TestGetTokenSource tests the GetTokenSource function.
func TestGetTokenSource(t *testing.T) {
	ctx := context.Background()
	config := clientcredentials.Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		TokenURL:     "https://example.com/token",
	}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Test successful token retrieval
	tokenSource := GetTokenSource(ctx, config, tlsConfig)
	_, err := tokenSource.Token()
	assert.NoError(t, err)

	// Test error scenario with mock HTTP client
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &MockHTTPClient{})
	tokenSource = GetTokenSource(ctx, config, tlsConfig)
	_, err = tokenSource.Token()
	assert.Error(t, err)
	assert.Equal(t, "mock error", err.Error())
}

// TestParseToken tests the ParseToken function.
func TestParseToken(t *testing.T) {
	// Test successful token parsing
	tokenString := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJhdWQiOlsiYXVkMSIsImF1ZDIiXX0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	claims, err := ParseToken(tokenString)
	assert.NoError(t, err)
	assert.NotNil(t, claims)
	assert.Contains(t, claims["aud"], "aud1")

	// Test error scenario with invalid token
	invalidTokenString := "invalid.token.string"
	claims, err = ParseToken(invalidTokenString)
	assert.Error(t, err)
	assert.Nil(t, claims)
}
