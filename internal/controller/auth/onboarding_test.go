// Copyright 2024 Illumio, Inc. All Rights Reserved.

package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func (suite *AuthTestSuite) TestOnboard() {
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

func (suite *AuthTestSuite) TestGetFirstAudience() {
	tests := map[string]struct {
		claims         map[string]any
		expected       string
		expectedError  bool
		expectedErrMsg string
	}{
		"audience claim not found": {
			claims:         map[string]any{},
			expected:       "",
			expectedError:  true,
			expectedErrMsg: "audience claim not found",
		},
		"audience claim is not a slice": {
			claims: map[string]any{
				"aud": "not a slice",
			},
			expected:       "",
			expectedError:  true,
			expectedErrMsg: "audience claim is not a slice",
		},
		"audience slice is empty": {
			claims: map[string]any{
				"aud": []any{},
			},
			expected:       "",
			expectedError:  true,
			expectedErrMsg: "audience slice is empty",
		},
		"first audience claim is not a string": {
			claims: map[string]any{
				"aud": []any{123},
			},
			expected:       "",
			expectedError:  true,
			expectedErrMsg: "first audience claim is not a string",
		},
		"valid audience claim": {
			claims: map[string]any{
				"aud": []any{"exampleAudience"},
			},
			expected:      "exampleAudience",
			expectedError: false,
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			got, err := GetFirstAudience(suite.logger, tt.claims)
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
func TestGetClusterID(t *testing.T) {
	logger := zap.NewNop()
	ctx := context.Background()

	// Create a fake clientset with kube-system namespace
	fakeClientset := fake.NewSimpleClientset(&v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kube-system",
			UID:  "test-cluster-uid-12345",
		},
	})

	// Call the GetClusterID function
	clusterID, err := GetClusterID(ctx, logger, fakeClientset)
	require.NoError(t, err)
	assert.NotEmpty(t, clusterID)

	// Check if the returned UID matches the expected UID
	assert.Equal(t, "test-cluster-uid-12345", clusterID)

	// Log the cluster ID for verification
	t.Logf("Cluster ID: %s", clusterID)
}
