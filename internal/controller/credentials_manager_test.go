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
