// Copyright 2024 Illumio, Inc. All Rights Reserved.

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
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

// OnboardRequestAuth is specific to authservice to avoid conflict if main has a different one.
type OnboardRequestAuth struct {
	OnboardingClientId     string `json:"onboardingClientId"`
	OnboardingClientSecret string `json:"onboardingClientSecret"`
}

// OnboardResponseAuth is specific to authservice.
type OnboardResponseAuth struct {
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
	// This token is what the auth service will issue upon successful authentication.
	// It should match the token the gRPC server expects.
	issuedAccessToken string
	httpServer        *http.Server
	listener          net.Listener
}

// newAuthService creates a new AuthService.
// The `issuedAccessToken` is the token that this service will hand out
// upon successful authentication and should be the one the gRPC server validates.
func newAuthService(logger *zap.Logger, configuredClientID, configuredClientSecret, issuedAccessToken string) *AuthService {
	return &AuthService{
		logger:            logger.Named("AuthService"),
		clientID:          configuredClientID,
		clientSecret:      configuredClientSecret,
		issuedAccessToken: issuedAccessToken,
	}
}

// jsonResponse sends a JSON response with the specified HTTP status code and data.
func jsonResponse(w http.ResponseWriter, status int, data any, logger *zap.Logger) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if encodeError := json.NewEncoder(w).Encode(data); encodeError != nil {
		// Log error, but don't try to write to w again as headers are already sent.
		logger.Error("Failed to encode JSON response", zap.Error(encodeError))
	}
}

// startHTTPServer initializes and starts an HTTP server with TLS and logging for the AuthService.
// It's called by FakeServer.start().
func (as *AuthService) startHTTPServer(address string, cert tls.Certificate) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/k8s_cluster/authenticate", as.authenticateHandler)
	mux.HandleFunc("/api/v1/k8s_cluster/onboard", as.onboardClusterHandler)

	as.httpServer = &http.Server{
		Addr:         address,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
	}

	var err error
	as.listener, err = net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("authService HTTP server failed to listen on %s: %w", address, err)
	}
	as.logger.Info("AuthService HTTP server listening", zap.String("address", as.listener.Addr().String()))

	// Start the server in a goroutine so Start() is non-blocking
	go func() {
		as.logger.Info("Starting AuthService HTTP server (HTTPS)", zap.String("address", address))
		if err := as.httpServer.ServeTLS(as.listener, "", ""); err != nil && err != http.ErrServerClosed {
			as.logger.Error("AuthService HTTP server failed", zap.Error(err))
			// Consider a way to signal this failure to the main application if it's critical
		}
		as.logger.Info("AuthService HTTP server stopped serving.")
	}()

	return nil
}

// stopHTTPServer gracefully shuts down the AuthService's HTTP server.
func (as *AuthService) stopHTTPServer(ctx context.Context) error {
	if as.httpServer == nil {
		as.logger.Info("AuthService HTTP server was not running or already stopped.")
		return nil
	}
	as.logger.Info("Shutting down AuthService HTTP server...")
	err := as.httpServer.Shutdown(ctx)
	if err != nil {
		return fmt.Errorf("authService HTTP server shutdown error: %w", err)
	}
	as.logger.Info("AuthService HTTP server shut down successfully.")
	return nil
}

// authenticateHandler handles authentication requests and writes the response.
func (as *AuthService) authenticateHandler(w http.ResponseWriter, r *http.Request) {
	as.logger.Info(
		"received authenticate request",
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
	)

	if r.Method != http.MethodPost {
		as.logger.Error("Invalid request method, method not allowed", zap.String("method", r.Method))
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var req TokenRequest
	if err := r.ParseForm(); err != nil {
		as.logger.Error("Invalid request, unable to parse form", zap.Error(err))
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	req.GrantType = r.FormValue("grant_type")
	req.ClientID = r.FormValue("client_id")
	req.ClientSecret = r.FormValue("client_secret")

	as.logger.Info("Received credentials for authenticate", zap.String("client_id", req.ClientID)) // Avoid logging secret

	if req.GrantType != AllowedGrantType {
		as.logger.Warn("Invalid grant type", zap.String("grant_type", req.GrantType))
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": InvalidGrantError}, as.logger)
		return
	}

	if req.ClientID == as.clientID && req.ClientSecret == as.clientSecret {
		response := TokenResponse{AccessToken: as.issuedAccessToken}
		as.logger.Info("Authentication successful")
		jsonResponse(w, http.StatusOK, response, as.logger)
	} else {
		as.logger.Warn("Authentication failed: invalid client credentials", zap.String("received_client_id", req.ClientID))
		jsonResponse(w, http.StatusUnauthorized, map[string]string{"error": UnauthorizedClient}, as.logger)
	}
}

// onboardClusterHandler handles the onboard request and returns OAuth creds used for token.
func (as *AuthService) onboardClusterHandler(w http.ResponseWriter, r *http.Request) {
	as.logger.Info(
		"received onboard request",
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
	)
	if r.Method != http.MethodPost {
		as.logger.Error("Invalid request method for onboard, method not allowed", zap.String("method", r.Method))
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		as.logger.Error("Error reading onboard request body", zap.Error(err))
		http.Error(w, "Error reading request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	var requestData OnboardRequestAuth
	if err := json.Unmarshal(body, &requestData); err != nil {
		as.logger.Error("Error unmarshalling onboard JSON", zap.Error(err))
		http.Error(w, "Error unmarshalling JSON", http.StatusBadRequest)
		return
	}
	as.logger.Info("Received onboarding data", zap.String("onboarding_client_id", requestData.OnboardingClientId))

	if !(reflect.TypeOf(requestData.OnboardingClientId).Kind() == reflect.String && reflect.TypeOf(requestData.OnboardingClientSecret).Kind() == reflect.String) {
		as.logger.Error("Bad format for onboard request", zap.Any("request_data_type_id", reflect.TypeOf(requestData.OnboardingClientId)), zap.Any("request_data_type_secret", reflect.TypeOf(requestData.OnboardingClientSecret)))
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "Bad format request"}, as.logger)
		return
	}
	// For testing, echo back the client_id and client_secret that were sent.
	// In a real scenario, you might generate new credentials or use pre-configured ones.
	resp := OnboardResponseAuth{ClusterClientId: requestData.OnboardingClientId, ClusterClientSecret: requestData.OnboardingClientSecret}
	as.logger.Info("Onboarding successful", zap.Any("response", resp))
	jsonResponse(w, http.StatusOK, resp, as.logger)
}
