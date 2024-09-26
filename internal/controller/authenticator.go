// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"crypto/tls"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/golang-jwt/jwt/v4"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Authenticator keeps a logger for its own methods.
type Authenticator struct {
	Logger *zap.SugaredLogger
}

// GetOnboardingCredentials returns credentials to onboard this cluster with CloudSecure.
func (authn *Authenticator) GetOnboardingCredentials(ctx context.Context, clientID string, clientSecret string) (Credentials, error) {
	if clientID == "" || clientSecret == "" {
		return Credentials{}, errors.New("incomplete credentials found")
	}
	return Credentials{ClientID: clientID, ClientSecret: clientSecret}, nil
}

// ReadK8sSecret takes a secretName and reads the file.
func (authn *Authenticator) ReadCredentialsK8sSecrets(ctx context.Context, secretName string) (string, string, error) {
	// Create a new clientset
	clientset, err := NewClientSet()
	if err != nil {
		authn.Logger.Errorw("Failed to create clientSet", "error", err)
		return "", "", err
	}

	// Get the secret
	secret, err := clientset.CoreV1().Secrets("illumio-cloud").Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		authn.Logger.Errorw("Failed to get secret", "error", err)
		return "", "", err
	}

	// Assuming your secret data has a "client_id" and "client_secret" key.
	clientID := string(secret.Data["client_id"])
	if clientID == "" {
		authn.Logger.Errorw("Cannot get client_id", "error", err)
		return "", "", errors.New("failed to get client_id from secret")
	}
	clientSecret := string(secret.Data["client_secret"])
	if clientSecret == "" {
		authn.Logger.Errorw("Cannot get client_secret", "error", err)
		return "", "", errors.New("failed to get client_secret from secret")
	}
	return clientID, clientSecret, nil
}

func (authn *Authenticator) DoesK8sSecretExist(ctx context.Context, secretName string) bool {
	clientset, err := NewClientSet()
	if err != nil {
		authn.Logger.Errorw("Failed to create clientSet", "error", err)
	}

	// Get the secret -> illumio-cloud will need to be configurable
	_, err = clientset.CoreV1().Secrets("illumio-cloud").Get(ctx, secretName, metav1.GetOptions{})
	return err == nil
}

// WriteK8sSecret takes an OnboardResponse and writes it to a locally kept secret.
func (authn *Authenticator) WriteK8sSecret(ctx context.Context, keyData OnboardResponse, ClusterCreds string) error {
	clientset, err := NewClientSet()
	if err != nil {
		authn.Logger.Errorw("Failed to create clientSet", "error", err)
		return err
	}
	secretData := map[string]string{
		"client_id":     keyData.ClusterClientId,
		"client_secret": keyData.ClusterClientSecret,
	}
	namespace := "illumio-cloud" // Will be made configurable.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ClusterCreds,
			Namespace: namespace,
		},
		StringData: secretData,
	}

	_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
	if err != nil {
		authn.Logger.Errorw("Failed to update secret", "error", err)
		return err
	}
	return nil
}

// NewClientSet returns a new Kubernetes clientset based on the execution environment.
func NewClientSet() (*kubernetes.Clientset, error) {
	var clusterConfig *rest.Config
	var err error

	if os.Getenv("KUBECONFIG") != "" || !IsRunningInCluster() {
		var kubeconfig string
		if home := homedir.HomeDir(); home != "" {
			kubeconfig = filepath.Join(home, ".kube", "config")
		}
		clusterConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		clusterConfig, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, err
	}

	return kubernetes.NewForConfig(clusterConfig)
}

// IsRunningInCluster helps determine if the application is running inside a Kubernetes cluster.
func IsRunningInCluster() bool {
	// This can be based on the existence of a service account token, environment variables, or similar.
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
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
		logger.Errorw("Error retrieving a valid token", "error", err)
		return &grpc.ClientConn{}, err
	}
	claims := jwt.MapClaims{}
	// Parse the token.
	// nosemgrep: jwt-go-parse-unverified
	_, _, err = jwt.NewParser().ParseUnverified(token.AccessToken, claims)
	if err != nil {
		logger.Errorw("Error parsing token", "error", err)
		return &grpc.ClientConn{}, err
	}
	aud, err := getFirstAudience(logger, claims)
	if err != nil {
		logger.Errorw("Error pulling audience out of token",
			"error", err,
		)
	}
	// Establish gRPC connection with TLS config.
	creds := credentials.NewTLS(tlsConfig)
	conn, err := grpc.NewClient(
		aud,
		grpc.WithTransportCredentials(creds),
		grpc.WithPerRPCCredentials(oauth.TokenSource{
			TokenSource: tokenSource,
		}),
		grpc.WithKeepaliveParams(kacp))
	if err != nil {
		return nil, err
	}
	return conn, nil
}
