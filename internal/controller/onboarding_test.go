// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	logger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
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

			credentials := Credentials{
				ClientID:     tt.clientID,
				ClientSecret: tt.clientSecret,
			}

			response, err := Onboard(ctx, true, tt.requestURL, credentials, logger)
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
			got, err := getFirstAudience(suite.logger, tt.claims)
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

// TestGetClusterID tests the GetClusterID function.
func (suite *ControllerTestSuite) TestGetClusterID() {
	logger := suite.logger
	ctx := context.Background()

	// Manually retrieve the expected UID of the kube-system namespace
	clientset, err := NewClientSet()
	assert.NoError(suite.T(), err)

	namespace, err := clientset.CoreV1().Namespaces().Get(ctx, "kube-system", v1.GetOptions{})
	assert.NoError(suite.T(), err)
	expectedUID := string(namespace.UID)

	// Call the GetClusterID function
	clusterID, err := GetClusterID(ctx, logger)
	assert.NoError(suite.T(), err)
	assert.NotEmpty(suite.T(), clusterID)

	// Check if the returned UID matches the expected UID
	assert.Equal(suite.T(), expectedUID, clusterID)

	// Log the cluster ID for verification
	suite.T().Logf("Cluster ID: %s", clusterID)
}
