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

func TestPair(t *testing.T) {
	ctx := context.Background()
	zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
	logger := zapLogger.WithName("test")
	t.Run("Success", func(t *testing.T) {
		expectedResponse := PairResponse{
			ClusterClientId:     "test-client-id",
			ClusterClientSecret: "test-client-secret",
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			var requestData map[string]string
			err := json.NewDecoder(r.Body).Decode(&requestData)
			assert.NoError(t, err)
			assert.Equal(t, "test-client-id", requestData["cluster_client_id"])
			assert.Equal(t, "test-client-secret", requestData["cluster_client_secret"])

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(expectedResponse)
		}))
		defer server.Close()

		am := &CredentialsManager{
			Credentials: Credentials{
				ClientID:     "test-client-id",
				ClientSecret: "test-client-secret",
				ClusterID:    "test-cluster-id",
			},
			Logger: logger,
		}

		response, err := am.Pair(ctx, true, server.URL)
		assert.NoError(t, err)
		assert.Equal(t, expectedResponse, response)
	})

	t.Run("Request URL Error", func(t *testing.T) {

		am := &CredentialsManager{
			Credentials: Credentials{
				ClientID:     "test-client-id",
				ClientSecret: "test-client-secret",
				ClusterID:    "test-cluster-id",
			},
			Logger: logger,
		}
		expectedErrorMsg := "parse \"http://example.com/\\x00\": net/url: invalid control character in URL"

		_, err := am.Pair(ctx, true, "http://example.com/\x00")
		assert.EqualErrorf(t, err, expectedErrorMsg, "Error should be: %v, got: %v", expectedErrorMsg, err)
		assert.Error(t, err)
	})
}
