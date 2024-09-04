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

	"github.com/golang-jwt/jwt"
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
	ClusterClientId     string `json:"clusterClientId"`
	ClusterClientSecret string `json:"clusterClientSecret"`
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
		am.Logger.Error(err, "Unable to marshal json data")
		return responseData, err
	}

	// Create a new POST request with the JSON data
	req, err := http.NewRequest("POST", OnboardingEndpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		am.Logger.Error(err, "Unable to structure post request")
		return responseData, err
	}

	// Set the appropriate headers
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		am.Logger.Error(err, "Unable to send post request")
		return responseData, err
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// 200 OK - Continue processing the response
	case http.StatusUnauthorized:
		// 401 Unauthorized
		err := errors.New("unauthorized: invalid credentials")
		am.Logger.Error(err, "Received 401 Unauthorized")
		return responseData, err
	case http.StatusInternalServerError:
		// 500 Internal Server Error
		err := errors.New("internal server error: something went wrong on the server")
		am.Logger.Error(err, "Received 500 Internal Server Error")
		return responseData, err
	default:
		// Handle other status codes
		err := errors.New("unexpected status code")
		am.Logger.Error(err, "Received unexpected status code", "error code", resp.StatusCode)
		return responseData, err
	}
	defer resp.Body.Close()
	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		am.Logger.Error(err, "Unable to read response of onboard post request")
		return responseData, err
	}
	if err := json.Unmarshal(body, &responseData); err != nil {
		am.Logger.Error(err, "Unable to unmarshal json data")
		return responseData, err
	}
	return responseData, nil
}

// SetUpOAuthConnection establishes a gRPC connection using OAuth credentials and logging the process.
func SetUpOAuthConnection(ctx context.Context, logger *zap.SugaredLogger, tokenURL string, TlsSkipVerify bool, clientID string, clientSecret string) (*grpc.ClientConn, error) {
	// Configure TLS settings
	// nosemgrep: bypass-tls-verification
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: TlsSkipVerify,
	}

	// Set up the OAuth2 config using the client credentials flow.
	oauthConfig := clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     tokenURL,
		AuthStyle:    oauth2.AuthStyleInParams,
	}
	tokenSource := oauthConfig.TokenSource(context.WithValue(ctx, oauth2.HTTPClient, &http.Client{
		// nosemgrep: bypass-tls-verification
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}))

	// Retrieve the token, we need the AUD value for the enpoint.
	token, err := tokenSource.Token()
	if err != nil {
		logger.Error(err, "Error retrieving a valid token")
		return &grpc.ClientConn{}, err
	}
	// Parse the token.
	// nosemgrep: jwt-go-parse-unverified
	parsedJWT, _, err := new(jwt.Parser).ParseUnverified(token.AccessToken, jwt.MapClaims{})
	if err != nil {
		logger.Error(err, "Error parsing token")
		return &grpc.ClientConn{}, err
	}
	var aud string
	if claims, ok := parsedJWT.Claims.(jwt.MapClaims); ok {
		// Access custom claims to get aud value.
		aud, _ = claims["aud"].(string)

	} else {
		logger.Error(err, "Error retrieving aud value from JWT")
		return &grpc.ClientConn{}, err
	}

	// Establish gRPC connection with TLS config.
	creds := credentials.NewTLS(tlsConfig)
	conn, err := grpc.NewClient(
		aud,
		grpc.WithTransportCredentials(creds),
		grpc.WithPerRPCCredentials(oauth.TokenSource{
			TokenSource: tokenSource,
		}))
	if err != nil {
		return nil, err
	}
	return conn, nil
}
