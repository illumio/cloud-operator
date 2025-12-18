// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"

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
		onboardingClientID          string
		onboardingClientSecret      string
		serverHandler               http.HandlerFunc
		requestURL                  string
		expectedClusterClientID     string
		expectedClusterClientSecret string
		expectedError               bool
		expectedErrMsg              string
	}{
		"success": {
			onboardingClientID:     "test-client-id",
			onboardingClientSecret: "test-client-secret",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				suite.Equal("application/json", r.Header.Get("Content-Type"))

				var requestData map[string]string

				err := json.NewDecoder(r.Body).Decode(&requestData)
				suite.NoError(err)
				suite.Equal("test-client-id", requestData["onboardingClientId"])
				suite.Equal("test-client-secret", requestData["onboardingClientSecret"])

				w.Header().Set("Content-Type", "application/json")

				err = json.NewEncoder(w).Encode(OnboardResponse{
					ClusterClientID:     "test-client-id",
					ClusterClientSecret: "test-client-secret",
				})
				if err != nil {
					suite.T().Fatal("Failed to encode response in creds manager test " + err.Error())
				}
			},
			requestURL:                  "http://something.invalid",
			expectedClusterClientID:     "test-client-id",
			expectedClusterClientSecret: "test-client-secret",
			expectedError:               false,
		},
		"request-url-error": {
			onboardingClientID:     "test-client-id",
			onboardingClientSecret: "test-client-secret",
			serverHandler:          nil,
			requestURL:             "http://something.invalid/\x00",
			expectedError:          true,
			expectedErrMsg:         "parse \"http://something.invalid/\\x00\": net/url: invalid control character in URL",
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			if tt.serverHandler != nil {
				server := httptest.NewServer(tt.serverHandler)
				defer server.Close()

				tt.requestURL = server.URL
			}

			gotClusterClientID, gotClusterClientSecret, err := OnboardCluster(ctx, true, tt.requestURL, tt.onboardingClientID, tt.onboardingClientSecret, logger)
			if tt.expectedError {
				suite.EqualErrorf(err, tt.expectedErrMsg, "Error should be: %v, got: %v", tt.expectedErrMsg, err)
			} else {
				suite.Require().NoError(err)
				suite.Equal(tt.expectedClusterClientID, gotClusterClientID)
				suite.Equal(tt.expectedClusterClientSecret, gotClusterClientSecret)
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
				suite.Require().Error(err)
				suite.EqualErrorf(err, tt.expectedErrMsg, "Error should be: %v, got: %v", tt.expectedErrMsg, err)
			} else {
				suite.Require().NoError(err)
				suite.Equal(tt.expected, got, "Expected audience: %v, got: %v", tt.expected, got)
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
	suite.Require().NoError(err)

	namespace, err := clientset.CoreV1().Namespaces().Get(ctx, "kube-system", v1.GetOptions{})
	suite.Require().NoError(err)

	expectedUID := string(namespace.UID)

	// Call the GetClusterID function
	clusterID, err := GetClusterID(ctx, logger)
	suite.Require().NoError(err)
	suite.NotEmpty(clusterID)

	// Check if the returned UID matches the expected UID
	suite.Equal(expectedUID, clusterID)

	// Log the cluster ID for verification
	suite.T().Logf("Cluster ID: %s", clusterID)
}
