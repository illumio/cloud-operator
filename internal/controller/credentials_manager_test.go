// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func (suite *ControllerTestSuite) TestOnboard() {
	ctx := context.Background()
	zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
	logger := zapLogger.WithName("test")

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
				assert.Equal(suite.T(), "test-client-id", requestData["onboarding_client_id"])
				assert.Equal(suite.T(), "test-client-secret", requestData["onboarding_client_secret"])

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

			response, err := am.Onboard(ctx, "true", tt.requestURL)
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
