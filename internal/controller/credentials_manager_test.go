// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestOnboard(t *testing.T) {
	ctx := context.Background()
	zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
	logger := zapLogger.WithName("test")

	tests := []struct {
		name             string
		clientID         string
		clientSecret     string
		serverHandler    http.HandlerFunc
		requestURL       string
		expectedResponse OnboardResponse
		expectedError    bool
		expectedErrMsg   string
	}{
		{
			name:         "Success",
			clientID:     "test-client-id",
			clientSecret: "test-client-secret",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

				var requestData map[string]string
				err := json.NewDecoder(r.Body).Decode(&requestData)
				assert.NoError(t, err)
				assert.Equal(t, "test-client-id", requestData["cluster_client_id"])
				assert.Equal(t, "test-client-secret", requestData["cluster_client_secret"])

				w.Header().Set("Content-Type", "application/json")
				err = json.NewEncoder(w).Encode(OnboardResponse{
					ClusterClientId:     "test-client-id",
					ClusterClientSecret: "test-client-secret",
				})
				if err != nil {
					panic("Failed to encode response in creds manager test " + err.Error())
				}
			},
			requestURL: "http://example.com",
			expectedResponse: OnboardResponse{
				ClusterClientId:     "test-client-id",
				ClusterClientSecret: "test-client-secret",
			},
			expectedError: false,
		},
		{
			name:           "Request URL Error",
			clientID:       "test-client-id",
			clientSecret:   "test-client-secret",
			serverHandler:  nil,
			requestURL:     "http://example.com/\x00",
			expectedError:  true,
			expectedErrMsg: "parse \"http://example.com/\\x00\": net/url: invalid control character in URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
				assert.Error(t, err)
				assert.EqualErrorf(t, err, tt.expectedErrMsg, "Error should be: %v, got: %v", tt.expectedErrMsg, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResponse, response)
			}
		})
	}
}
