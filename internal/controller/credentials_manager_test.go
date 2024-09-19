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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
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

// Mocked function to replace the real GetClusterID function for testing
func GetClusterIDWithClient(ctx context.Context, logger *zap.SugaredLogger, clientset *fake.Clientset) (string, error) {
	namespace, err := clientset.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
	if err != nil {
		logger.Errorw("Failed to get kube-system namespace", "error", err)
		return "", err
	}
	return string(namespace.UID), nil
}

func (suite *ControllerTestSuite) TestGetClusterID() {
	ctx := context.Background()
	logger := newCustomLogger(suite.T())

	tests := map[string]struct {
		setup     func() *fake.Clientset
		want      string
		expectErr bool
	}{
		"success": {
			setup: func() *fake.Clientset {
				client := fake.NewSimpleClientset(&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "kube-system",
						UID:  "test-uid",
					},
				})
				return client
			},
			want:      "test-uid",
			expectErr: false,
		},
		"namespace-not-found": {
			setup: func() *fake.Clientset {
				client := fake.NewSimpleClientset()
				return client
			},
			want:      "",
			expectErr: true,
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			client := tt.setup()
			got, err := GetClusterIDWithClient(ctx, logger, client)
			if (err != nil) != tt.expectErr {
				suite.T().Errorf("GetClusterIDWithClient() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if got != tt.want {
				suite.T().Errorf("GetClusterIDWithClient() got = %v, want %v", got, tt.want)
			}
		})
	}
}
