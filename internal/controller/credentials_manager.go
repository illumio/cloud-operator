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

	"github.com/golang-jwt/jwt/v4"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
)

// Credentials contains attributes that are needed for onboarding.
type Credentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// CredentialsManager holds credentials and a logger.
type CredentialsManager struct {
	Credentials Credentials
	Logger      *zap.SugaredLogger
}

type OnboardResponse struct {
	ClusterClientId     string `json:"cluster_client_id"`
	ClusterClientSecret string `json:"cluster_client_secret"`
}

// Onboard onboards this cluster with CloudSecure using the onboarding credentials and obtains OAuth 2 credentials for this cluster.
func (am *CredentialsManager) Onboard(ctx context.Context, TlsSkipVerify bool, OnboardingEndpoint string) (OnboardResponse, error) {
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: TlsSkipVerify,
	}
	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}
	client := &http.Client{
		Transport: transport,
	}
	// Define the URL to which the POST request will be made

	// Create the data to be sent in the POST request
	data := map[string]string{
		"onboardingClientId":     am.Credentials.ClientID,
		"onboardingClientSecret": am.Credentials.ClientSecret,
	}
	var responseData OnboardResponse
	// Convert the data to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		am.Logger.Errorw("Unable to marshal json data", "error", err)
		return responseData, err
	}

	// Create a new POST request with the JSON data
	req, err := http.NewRequest("POST", OnboardingEndpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		am.Logger.Errorw("Unable to structure post request", "error", err)
		return responseData, err
	}

	// Set the appropriate headers
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		am.Logger.Errorw("Unable to send post request", "error", err)
		return responseData, err
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// 200 OK - Continue processing the response
	case http.StatusUnauthorized:
		// 401 Unauthorized
		err := errors.New("unauthorized: invalid credentials")
		am.Logger.Errorw("Received 401 Unauthorized",
			"error", err,
			"status_code", 401,
			"description", "invalid credentials",
		)
		return responseData, err
	case http.StatusInternalServerError:
		// 500 Internal Server Error
		err := errors.New("internal server error: something went wrong on the server")
		am.Logger.Errorw("Received 500 Internal Server Error",
			"error", err,
			"status_code", http.StatusInternalServerError,
			"description", "something went wrong on the server",
		)
		return responseData, err
	default:
		// Handle other status codes
		err := errors.New("unexpected status code")
		am.Logger.Errorw("Received unexpected status code",
			"error", err,
			"status_code", resp.StatusCode,
		)
		return responseData, err
	}
	defer resp.Body.Close()
	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		am.Logger.Errorw("Unable to read response of onboard post request", "error", err)
		return responseData, err
	}
	if err := json.Unmarshal(body, &responseData); err != nil {
		am.Logger.Errorw("Unable to unmarshal json data", "error", err)
		return responseData, err
	}
	return responseData, nil
}

// SetUpOAuthConnection establishes a gRPC connection using OAuth credentials and logging the process.
func SetUpOAuthConnection(
	ctx context.Context,
	logger *zap.SugaredLogger,
	tokenURL string,
	tlsSkipVerify bool,
	clientID string,
	clientSecret string,
) (*grpc.ClientConn, error) {
	tlsConfig := GetTLSConfig(tlsSkipVerify)

	oauthConfig := clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     tokenURL,
		AuthStyle:    oauth2.AuthStyleInParams,
	}
	tokenSource := GetTokenSource(ctx, oauthConfig, tlsConfig)

	token, err := tokenSource.Token()
	if err != nil {
		logger.Errorw("Error retrieving a valid token", "error", err)
		return nil, err
	}

	claims, err := ParseToken(token.AccessToken)
	if err != nil {
		logger.Errorw("Error parsing token", "error", err)
		return nil, err
	}

	aud, err := getFirstAudience(logger, claims)
	if err != nil {
		logger.Errorw("Error pulling audience out of token", "error", err)
		return nil, err
	}

	creds := credentials.NewTLS(tlsConfig)
	conn, err := grpc.NewClient(
		aud,
		grpc.WithTransportCredentials(creds),
		grpc.WithPerRPCCredentials(oauth.TokenSource{TokenSource: tokenSource}),
	)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// getFirstAudience extracts the first audience from the claims map
func getFirstAudience(logger *zap.SugaredLogger, claims map[string]interface{}) (string, error) {
	aud, ok := claims["aud"]
	if !ok {
		err := errors.New("audience claim not found")
		logger.Errorw("Error extracting audience claim",
			"error", err,
		)
		return "", err
	}

	audSlice, ok := aud.([]interface{})
	if !ok {
		err := errors.New("audience claim is not a slice")
		logger.Errorw("Error extracting audience claim",
			"error", err,
			"aud", aud,
		)
		return "", err
	}

	if len(audSlice) == 0 {
		err := errors.New("audience slice is empty")
		logger.Errorw("Error extracting audience claim",
			"error", err,
		)
		return "", err
	}

	firstAud, ok := audSlice[0].(string)
	if !ok {
		err := errors.New("first audience claim is not a string")
		logger.Errorw("Error extracting audience claim",
			"error", err,
			"first_aud", audSlice[0],
		)
		return "", err
	}

	return firstAud, nil
}

// GetTLSConfig returns a TLS configuration.
func GetTLSConfig(skipVerify bool) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: skipVerify,
	}
}

// GetTokenSource returns an OAuth2 token source.
func GetTokenSource(ctx context.Context, config clientcredentials.Config, tlsConfig *tls.Config) oauth2.TokenSource {
	return config.TokenSource(context.WithValue(ctx, oauth2.HTTPClient, &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}))
}

// ParseToken parses the JWT token and returns the claims.
func ParseToken(tokenString string) (jwt.MapClaims, error) {
	claims := jwt.MapClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(tokenString, claims)
	return claims, err
}
