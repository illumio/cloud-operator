// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/golang-jwt/jwt"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/go-logr/logr"
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
	Logger      logr.Logger
}

type OnboardResponse struct {
	ClusterClientId     string `json:"clusterClientId"`
	ClusterClientSecret string `json:"clusterClientSecret"`
}

// Onboard onboards this cluster with CloudSecure using the onboarding credentials and obtains OAuth 2 credentials for this cluster.
func (am *CredentialsManager) Onboard(ctx context.Context, TlsSkipVerify string, OnboardingEndpoint string) (OnboardResponse, error) {
	TlsSkipVerifyBool, _ := strconv.ParseBool(TlsSkipVerify)
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: TlsSkipVerifyBool,
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
		"onboarding_client_id":     am.Credentials.ClientID,
		"onboarding_client_secret": am.Credentials.ClientSecret,
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
	defer resp.Body.Close()
	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		am.Logger.Error(err, "Unable to read response of onboard post request")
		return responseData, err
	}
	am.Logger.Info("body", "body", string(body))

	if err := json.Unmarshal(body, &responseData); err != nil {
		am.Logger.Error(err, "Unable to unmarshal json data")
		return responseData, err
	}
	return responseData, nil
}

// SetUpOAuthConnection establishes a gRPC connection using OAuth credentials and logging the process.
func SetUpOAuthConnection(ctx context.Context, logger logr.Logger, tokenURL string, TlsSkipVerify string, clientID string, clientSecret string) (*grpc.ClientConn, error) {
	TlsSkipVerifyBool, _ := strconv.ParseBool(TlsSkipVerify)
	// Configure TLS settings
	// nosemgrep: bypass-tls-verification
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: TlsSkipVerifyBool,
	}

	// Set up the OAuth2 config using the client credentials flow.
	oauthConfig := clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     tokenURL,
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
