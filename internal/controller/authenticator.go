// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"go.uber.org/zap"
	"golang.org/x/net/proxy"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
	"google.golang.org/grpc/keepalive"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var kacp = keepalive.ClientParameters{
	Time:                10 * time.Second, // send pings every 10 seconds if there is no activity
	Timeout:             10 * time.Second, // wait 10s for ping ack before considering the connection dead
	PermitWithoutStream: true,             // send pings even without active streams
}

// Authenticator keeps a logger for its own methods.
type Authenticator struct {
	Logger *zap.Logger
}

// GetOnboardingCredentials returns credentials to onboard this cluster with CloudSecure.
func (authn *Authenticator) GetOnboardingCredentials(ctx context.Context, clientID string, clientSecret string) (Credentials, error) {
	if clientID == "" || clientSecret == "" {
		return Credentials{}, errors.New("incomplete credentials found")
	}
	return Credentials{ClientID: clientID, ClientSecret: clientSecret}, nil
}

// ReadK8sSecret takes a secretName and reads the file.
func (authn *Authenticator) ReadCredentialsK8sSecrets(ctx context.Context, secretName string, podNamespace string) (string, string, error) {
	// Create a new clientset
	clientset, err := NewClientSet()
	if err != nil {
		authn.Logger.Error("Failed to create clientSet", zap.Error(err))
		return "", "", err
	}

	// Get the secret
	secret, err := clientset.CoreV1().Secrets(podNamespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		authn.Logger.Error("Failed to get secret", zap.Error(err))
		return "", "", err
	}

	// Assuming your secret data has a "client_id" and "client_secret" key.
	clientID := string(secret.Data[string(ONBOARDING_CLIENT_ID)])
	if clientID == "" {
		return "", "", NewCredentialNotFoundInK8sSecretError(ONBOARDING_CLIENT_ID)
	}
	clientSecret := string(secret.Data[string(ONBOARDING_CLIENT_SECRET)])
	if clientSecret == "" {
		return "", "", NewCredentialNotFoundInK8sSecretError(ONBOARDING_CLIENT_SECRET)
	}
	return clientID, clientSecret, nil
}

func (authn *Authenticator) DoesK8sSecretExist(ctx context.Context, secretName string, podNamespace string) bool {
	clientset, err := NewClientSet()
	if err != nil {
		authn.Logger.Error("Failed to create clientSet", zap.Error(err))
	}

	_, err = clientset.CoreV1().Secrets(podNamespace).Get(ctx, secretName, metav1.GetOptions{})
	return err == nil
}

// WriteK8sSecret updates the data in an existing Kubernetes Secret without overwriting annotations or labels.
func (authn *Authenticator) WriteK8sSecret(ctx context.Context, keyData OnboardResponse, ClusterCreds string, podNamespace string) error {
	clientset, err := NewClientSet()
	if err != nil {
		authn.Logger.Error("Failed to create clientSet", zap.Error(err))
		return err
	}

	secretsClient := clientset.CoreV1().Secrets(podNamespace)
	// Fetch the existing Secret to preserve metadata
	existingSecret, err := secretsClient.Get(ctx, ClusterCreds, metav1.GetOptions{})
	if err != nil {
		authn.Logger.Error("Failed to get existing secret", zap.Error(err))
		return err
	}

	// Update only the Secret data
	existingSecret.Data = map[string][]byte{
		string(ONBOARDING_CLIENT_ID):     []byte(keyData.ClusterClientId),
		string(ONBOARDING_CLIENT_SECRET): []byte(keyData.ClusterClientSecret),
	}

	// Apply the update
	_, err = secretsClient.Update(ctx, existingSecret, metav1.UpdateOptions{})
	if err != nil {
		authn.Logger.Error("Failed to update secret", zap.Error(err))
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

	// clusterConfig.Proxy = http.ProxyURL(nil)

	return kubernetes.NewForConfig(clusterConfig)
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

	// Log proxy settings
	logger.Info(
		"Configuring proxy from environment variables",
		zap.String("http_proxy", os.Getenv("HTTP_PROXY")),
		zap.String("https_proxy", os.Getenv("HTTPS_PROXY")),
		zap.String("no_proxy", os.Getenv("NO_PROXY")),
	)

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

	aud, err := getFirstAudience(logger, claims)
	if err != nil {
		logger.Error("Error pulling audience out of token", zap.Error(err))
		return nil, err
	}
	tokenSource = GetTokenSource(ctx, oauthConfig, tlsConfig)
	creds := credentials.NewTLS(tlsConfig)

	proxyDialer := func(ctx context.Context, addr string) (net.Conn, error) {
		proxyFunc := http.ProxyFromEnvironment
		if proxyFunc == nil {
			return net.Dial("tcp", addr)
		}
		proxyURL, err := proxyFunc(&http.Request{URL: &url.URL{Host: addr}})
		if err != nil {
			logger.Warn("Invalid HTTPS proxy configured; ignoring proxy settings", zap.Error(err))
			return net.Dial("tcp", addr)
		}
		if proxyURL == nil { // No proxy configured
			return net.Dial("tcp", addr)
		}
		conn, err := proxy.Dial(ctx, "tcp", addr)
		if err != nil {
			logger.Warn("Failed to connect via proxy; falling back to direct connection", zap.Error(err))
			return net.Dial("tcp", addr)
		}
		return conn, nil
	}

	conn, err := grpc.NewClient(
		aud,
		grpc.WithTransportCredentials(creds),
		grpc.WithPerRPCCredentials(oauth.TokenSource{TokenSource: tokenSource}),
		grpc.WithKeepaliveParams(kacp),
		grpc.WithContextDialer(proxyDialer),
	)
	if err != nil {
		return nil, err
	}
	return conn, nil
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
