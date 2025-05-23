// Copyright 2024 Illumio, Inc. All Rights Reserved.

package main

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"time"

	"go.uber.org/zap"
)

// TokenRequest is a struct to hold the request parameters for the authenticateHandler
// following the OAuth2.0 specification.
type TokenRequest struct {
	GrantType    string
	ClientID     string // Client ID for authentication
	ClientSecret string // Client secret for authentication
}

// TokenResponse is a struct to hold the response parameters for the authenticateHandler
// following the OAuth2.0 specification.
type TokenResponse struct {
	AccessToken string `json:"access_token,omitempty"` //nolint:tagliatelle
}

type OnboardRequest struct {
	OnboardingClientId     string `json:"onboardingClientId"`
	OnboardingClientSecret string `json:"onboardingClientSecret"`
}
type OnboardResponse struct {
	ClusterClientId     string `json:"cluster_client_id"`
	ClusterClientSecret string `json:"cluster_client_secret"`
}

const (
	AllowedGrantType   = "client_credentials"
	InvalidGrantError  = "invalid_grant"
	UnauthorizedClient = "unauthorized_client"
)

// AuthService provides authentication services using client credentials and a token.
type AuthService struct {
	logger       *zap.Logger // Logger for logging authentication-related events
	clientID     string      // Client ID for authentication
	clientSecret string      // Client secret for authentication
	token        string      // Token used for authentication
}

// jsonResponse sends a JSON response with the specified HTTP status code and data.
func jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if encodeError := json.NewEncoder(w).Encode(data); encodeError != nil {
		http.Error(w, encodeError.Error(), http.StatusInternalServerError)

		return
	}
}

// startHTTPServer initializes and starts an HTTP server with TLS and logging, using the provided credentials and token.
func startHTTPServer(address string, cert tls.Certificate, authService *AuthService) (*http.Server, error) {
	// Set up the server with desired TLS configuration
	server := &http.Server{
		Addr:         address,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
	}
	http.DefaultServeMux = http.NewServeMux()
	// Register routes with the default HTTP multiplexer
	http.HandleFunc("/api/v1/k8s_cluster/authenticate", authService.authenticateHandler)
	http.HandleFunc("/api/v1/k8s_cluster/onboard", authService.onboardCluster)

	// Start the server in a goroutine
	go func() {
		if err := server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			authService.logger.Fatal("Failed to start OAuth2 HTTP server", zap.Error(err))
		}
	}()

	return server, nil
}

// authenticateHandler handles authentication requests and writes the response.
func (a *AuthService) authenticateHandler(w http.ResponseWriter, r *http.Request) {
	a.logger.Info(
		"received request",
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
	)

	if r.Method != http.MethodPost {
		a.logger.Error("Invalid request method, method not allowed", zap.String("method", r.Method))
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var req TokenRequest

	err := r.ParseForm()
	if err != nil {
		a.logger.Error("Invalid request, unable to parse form", zap.Error(err))
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	req.GrantType = r.FormValue("grant_type")
	req.ClientID = r.FormValue("client_id")
	req.ClientSecret = r.FormValue("client_secret")

	a.logger.Info("Received credentials", zap.String("client_id", req.ClientID), zap.String("client_secret", req.ClientSecret))

	if req.GrantType != AllowedGrantType {
		a.logger.Error("Invalid grant type", zap.String("grant_type", req.GrantType))
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": InvalidGrantError})
		return
	}

	if req.ClientID == a.clientID && req.ClientSecret == a.clientSecret {
		response := TokenResponse{AccessToken: a.token}
		a.logger.Info("Authentication successful", zap.String("access_token", a.token))
		jsonResponse(w, http.StatusOK, response)
	} else {
		a.logger.Error("Authentication failed", zap.String("received_client_id", req.ClientID), zap.String("received_client_secret", req.ClientSecret), zap.String("expected_client_id", a.clientID), zap.String("expected_client_secret", a.clientSecret))
		jsonResponse(w, http.StatusUnauthorized, map[string]string{"error": UnauthorizedClient})
	}
}

// onboardCluster handles the onboard request and returns OAuth creds used for token.
func (a *AuthService) onboardCluster(w http.ResponseWriter, r *http.Request) {
	a.logger.Info(
		"received request",
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
	)
	if r.Method != http.MethodPost {
		a.logger.Error("Invalid request method, method not allowed", zap.String("method", r.Method))
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	// Unmarshal the JSON data into a struct
	var requestData OnboardRequest
	if err := json.Unmarshal(body, &requestData); err != nil {
		http.Error(w, "Error unmarshalling JSON", http.StatusBadRequest)
		return
	}
	a.logger.Info("Received onboarding data", zap.String("onboarding_client_id", requestData.OnboardingClientId), zap.String("onboarding_client_secret", requestData.OnboardingClientSecret))

	if !(reflect.TypeOf(requestData.OnboardingClientId).Kind() == reflect.String && reflect.TypeOf(requestData.OnboardingClientSecret).Kind() == reflect.String) {
		a.logger.Error("Bad format request", zap.Any("request_data", requestData))
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "Bad format request"})
		return
	}
	// Just pass back what client sent for testing purposes.
	resp := OnboardResponse{ClusterClientId: requestData.OnboardingClientId, ClusterClientSecret: requestData.OnboardingClientSecret}
	a.logger.Info("Onboarding successful", zap.Any("response", resp))
	jsonResponse(w, http.StatusOK, resp)
}
