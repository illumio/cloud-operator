// Copyright 2024 Illumio, Inc. All Rights Reserved.

package auth

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
	"google.golang.org/grpc/keepalive"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	SecretFieldClientID     = "client_id"
	SecretFieldClientSecret = "client_secret"
)

// AuthConfig holds configuration for authentication operations.
type AuthConfig struct {
	ClusterCreds           string
	PodNamespace           string
	OnboardingClientID     string
	OnboardingClientSecret string
	OnboardingEndpoint     string
	TlsSkipVerify          bool
}

var kacp = keepalive.ClientParameters{
	Time:                10 * time.Second, // send pings every 10 seconds if there is no activity
	Timeout:             10 * time.Second, // wait 10s for ping ack before considering the connection dead
	PermitWithoutStream: true,             // send pings even without active streams
}

// GetClusterCredentials retrieves cluster credentials from a Kubernetes secret,
// or onboards the cluster if credentials are not found.
func GetClusterCredentials(ctx context.Context, logger *zap.Logger, clientset kubernetes.Interface, config AuthConfig) (string, string, error) {
	clientID, clientSecret, err := readClusterCredentialsFromK8sSecret(ctx, clientset, config.ClusterCreds, config.PodNamespace)
	if err != nil {
		return "", "", err
	}

	if clientID == "" || clientSecret == "" {
		if config.OnboardingClientID == "" || config.OnboardingClientSecret == "" {
			return "", "", errors.New("onboarding credentials are not configured")
		}

		clientID, clientSecret, err = OnboardCluster(ctx, config.TlsSkipVerify, config.OnboardingEndpoint,
			config.OnboardingClientID, config.OnboardingClientSecret, logger)
		if err != nil {
			return "", "", fmt.Errorf("failed to onboard cluster: %w", err)
		}

		err = writeClusterCredentialsIntoK8sSecret(ctx, clientset, clientID, clientSecret, config.ClusterCreds, config.PodNamespace)
		if err != nil {
			return "", "", fmt.Errorf("failed to write cluster credentials into k8s secret: %w", err)
		}
	}

	return clientID, clientSecret, err
}

// readClusterCredentialsFromK8sSecret takes a secretName and reads the file.
func readClusterCredentialsFromK8sSecret(ctx context.Context, clientset kubernetes.Interface, secretName string, podNamespace string) (string, string, error) {
	secret, err := clientset.CoreV1().Secrets(podNamespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return "", "", fmt.Errorf("failed to read cluster credentials from k8s secret: %w", err)
	}

	clientID := string(secret.Data[SecretFieldClientID])
	clientSecret := string(secret.Data[SecretFieldClientSecret])

	return clientID, clientSecret, nil
}

// writeClusterCredentialsIntoK8sSecret updates the data in an existing Kubernetes Secret without overwriting annotations or labels.
func writeClusterCredentialsIntoK8sSecret(ctx context.Context, clientset kubernetes.Interface, clusterClientID, clusterClusterSecret, clusterCreds string, podNamespace string) error {
	secretsClient := clientset.CoreV1().Secrets(podNamespace)
	// Fetch the existing Secret to preserve metadata
	existingSecret, err := secretsClient.Get(ctx, clusterCreds, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to read cluster credentials from k8s secret for update: %w", err)
	}

	// Update only the Secret data
	existingSecret.Data = map[string][]byte{
		SecretFieldClientID:     []byte(clusterClientID),
		SecretFieldClientSecret: []byte(clusterClusterSecret),
	}

	// Apply the update
	_, err = secretsClient.Update(ctx, existingSecret, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to write cluster credentials into k8s secret: %w", err)
	}

	return nil
}

// IsRunningInCluster helps determine if the application is running inside a Kubernetes cluster.
func IsRunningInCluster() bool {
	// This can be based on the existence of a service account token, environment variables, or similar.
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

// SetUpOAuthConnection establishes a gRPC connection using OAuth credentials and logging the process.
func SetUpOAuthConnection(
	ctx context.Context,
	logger *zap.Logger,
	tokenURL string,
	tlsSkipVerify bool,
	clientID string,
	clientSecret string,
) (*grpc.ClientConn, error) {
	tlsConfig := GetTLSConfig(tlsSkipVerify)

	contextWithTimeout, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	oauthConfig := clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     tokenURL,
		AuthStyle:    oauth2.AuthStyleInParams,
	}
	tokenSource := GetTokenSource(contextWithTimeout, oauthConfig, tlsConfig)

	token, err := tokenSource.Token()
	if err != nil {
		logger.Error("Error retrieving a valid token", zap.Error(err))

		return nil, err
	}

	claims, err := ParseToken(token.AccessToken)
	if err != nil {
		logger.Error("Error parsing token", zap.Error(err))

		return nil, err
	}

	aud, err := GetFirstAudience(logger, claims)
	if err != nil {
		logger.Error("Error pulling audience out of token", zap.Error(err))

		return nil, err
	}

	tokenSource = GetTokenSource(ctx, oauthConfig, tlsConfig)
	creds := credentials.NewTLS(tlsConfig)

	conn, err := grpc.NewClient(
		aud,
		grpc.WithTransportCredentials(creds),
		grpc.WithPerRPCCredentials(oauth.TokenSource{TokenSource: tokenSource}),
		grpc.WithKeepaliveParams(kacp),
	)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

// GetTLSConfig returns a TLS configuration.
func GetTLSConfig(skipVerify bool) *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: skipVerify, //nolint:gosec
		MinVersion:         tls.VersionTLS12,
	}
}

// GetTokenSource returns an OAuth2 token source.
func GetTokenSource(ctx context.Context, config clientcredentials.Config, tlsConfig *tls.Config) oauth2.TokenSource {
	return config.TokenSource(context.WithValue(ctx, oauth2.HTTPClient, &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
			Proxy:           http.ProxyFromEnvironment,
		},
	}))
}

// ParseToken parses the JWT token and returns the claims.
func ParseToken(tokenString string) (jwt.MapClaims, error) {
	claims := jwt.MapClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(tokenString, claims)

	return claims, err
}
