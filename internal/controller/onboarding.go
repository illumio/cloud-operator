// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type OnboardResponse struct {
	ClusterClientID     string `json:"cluster_client_id"`
	ClusterClientSecret string `json:"cluster_client_secret"`
}

// OnboardCluster onboards this cluster with CloudSecure using the onboarding credentials and obtains the OAuth 2 client ID and client secret for this cluster.
func OnboardCluster(ctx context.Context, tlsSkipVerify bool, onboardingEndpoint, onboardingClientID, onboardingClientSecret string, logger *zap.Logger) (string, string, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: tlsSkipVerify, //nolint:gosec
		MinVersion:         tls.VersionTLS12,
	}
	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
		Proxy:           http.ProxyFromEnvironment,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
	}
	// Define the URL to which the POST request will be made

	// Create the data to be sent in the POST request
	data := map[string]string{
		"onboardingClientId":     onboardingClientID,
		"onboardingClientSecret": onboardingClientSecret,
	}

	var responseData OnboardResponse
	// Convert the data to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		logger.Error("Unable to marshal json data", zap.Error(err))

		return "", "", err
	}

	// Create a new POST request with the JSON data
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, onboardingEndpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		logger.Error("Unable to structure post request", zap.Error(err))

		return "", "", err
	}

	// Set the appropriate headers
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		logger.Error("Unable to send post request", zap.Error(err))

		return "", "", err
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// 200 OK - Continue processing the response
	case http.StatusUnauthorized:
		// 401 Unauthorized
		err := errors.New("unauthorized: invalid credentials")
		logger.Error("Received 401 Unauthorized",
			zap.Error(err),
			zap.Int("status_code", http.StatusUnauthorized),
			zap.String("description", "invalid credentials"),
		)

		return "", "", err
	case http.StatusInternalServerError:
		// 500 Internal Server Error
		err := errors.New("internal server error: something went wrong on the server")
		logger.Error("Received 500 Internal Server Error",
			zap.Error(err),
			zap.Int("status_code", http.StatusInternalServerError),
			zap.String("description", "something went wrong on the server"),
		)

		return "", "", err
	default:
		// Handle other status codes
		err := errors.New("unexpected status code")
		logger.Error("Received unexpected status code",
			zap.Error(err),
			zap.Int("status_code", resp.StatusCode),
		)

		return "", "", err
	}
	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Unable to read response of onboard POST request", zap.Error(err))

		return "", "", err
	}

	if err := resp.Body.Close(); err != nil {
		logger.Error("Error closing POST response body", zap.Error(err))
	}

	if err := json.Unmarshal(body, &responseData); err != nil {
		logger.Error("Unable to unmarshal json data", zap.Error(err))

		return "", "", err
	}

	return responseData.ClusterClientID, responseData.ClusterClientSecret, nil
}

// getFirstAudience extracts the first audience from the claims map.
func getFirstAudience(logger *zap.Logger, claims map[string]interface{}) (string, error) {
	aud, ok := claims["aud"]
	if !ok {
		err := errors.New("audience claim not found")
		logger.Error("Error extracting audience claim",
			zap.Error(err),
		)

		return "", err
	}

	audSlice, ok := aud.([]interface{})
	if !ok {
		err := errors.New("audience claim is not a slice")
		logger.Error("Error extracting audience claim",
			zap.Error(err),
			zap.Any("aud", aud),
		)

		return "", err
	}

	if len(audSlice) == 0 {
		err := errors.New("audience slice is empty")
		logger.Error("Error extracting audience claim",
			zap.Error(err),
		)

		return "", err
	}

	firstAud, ok := audSlice[0].(string)
	if !ok {
		err := errors.New("first audience claim is not a string")
		logger.Error("Error extracting audience claim",
			zap.Error(err),
			zap.Any("first_aud", audSlice[0]),
		)

		return "", err
	}

	return firstAud, nil
}

// GetClusterID returns the uid of the k8s cluster's kube-system namespace, which is used as the cluster's globally unique ID.
func GetClusterID(ctx context.Context, logger *zap.Logger) (string, error) {
	clientset, err := NewClientSet()
	if err != nil {
		logger.Error("Error creating clientset", zap.Error(err))
	}

	namespace, err := clientset.CoreV1().Namespaces().Get(ctx, "kube-system", v1.GetOptions{})
	if err != nil {
		logger.Error("Could not find kube-system namespace", zap.Error(err))
	}

	return string(namespace.UID), nil
}
