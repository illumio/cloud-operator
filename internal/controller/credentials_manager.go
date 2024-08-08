// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
)

// Credentials contains attributes that are needed for pairing profiles.
type Credentials struct {
	cluster_id    string
	client_id     string
	client_secret string
}

// CredentialsManager holds credentials and a logger.
type CredentialsManager struct {
	Credentials Credentials
	Logger      logr.Logger
}

type PairResponse struct {
	ClusterClientId     string
	ClusterClientSecret string
}

// Pair Attempts to make a request with CloudSecure using a pairing profile and it will get back the oauth2 credentials.
// It then will store those credentials in the namespaces k8s secret.
func (am *CredentialsManager) Pair(ctx context.Context, TlsSkipVerify bool, PairingAddress string, OAuthSecret string) error {
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
		"cluster_client_id":     am.Credentials.client_id,
		"cluster_client_secret": am.Credentials.client_secret,
	}

	// Convert the data to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		am.Logger.Error(err, "Unable to marshal json data")
		return err
	}

	// Create a new POST request with the JSON data
	req, err := http.NewRequest("POST", PairingAddress, bytes.NewBuffer(jsonData))
	if err != nil {
		am.Logger.Error(err, "Unable to structure post request")
		return err
	}

	// Set the appropriate headers
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		am.Logger.Error(err, "Unable to send post request")
		return err
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		am.Logger.Error(err, "Unable to read response of Pair post request")
		return err
	}
	var responseData PairResponse
	if err := json.Unmarshal(body, &responseData); err != nil {
		am.Logger.Error(err, "Unable to unmarshal json data")
		return err
	}
	sm := &SecretManager{Logger: am.Logger}
	err = sm.WriteK8sSecret(ctx, responseData, OAuthSecret)
	time.Sleep(1 * time.Second)
	if err != nil {
		am.Logger.Error(err, "Failed to write secret to Kubernetes")
	}
	return nil
}

// SetUpOAuthConnection establishes a gRPC connection using OAuth credentials and logging the process.
func SetUpOAuthConnection(ctx context.Context, logger logr.Logger, tokenURL string, TlsSkipVerify bool, clientID string, clientSecret string) (*grpc.ClientConn, error) {
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
