package mtls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc/credentials"
	exp_credentials "google.golang.org/grpc/experimental/credentials"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	hubbleRelayExpectedServerName = "ui.hubble-relay.cilium.io"
)

var (
	ErrCertDataMissingInSecret = errors.New("required certificate data (ca.crt, tls.crt, or tls.key) not found in secret")
)

// getHubbleMTLSCertificatesFromSecret fetches ca.crt, tls.crt (client cert), and tls.key (client key)
// from the specified Kubernetes secret.
func getHubbleMTLSCertificatesFromSecret(
	ctx context.Context,
	clientset kubernetes.Interface,
	logger *zap.Logger,
	secretName string,
	secretNamespace string,
) (caData, clientCertData, clientKeyData []byte, err error) {

	logger.Info("Fetching mTLS certificates from Kubernetes secret",
		zap.String("secretName", secretName),
		zap.String("secretNamespace", secretNamespace))

	secret, err := clientset.CoreV1().Secrets(secretNamespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		logger.Error("Failed to get mTLS secret from Kubernetes API", zap.Error(err))
		return nil, nil, nil, fmt.Errorf("failed to get secret '%s' in namespace '%s': %w", secretName, secretNamespace, err)
	}

	caData, ok := secret.Data["ca.crt"]
	if !ok || len(caData) == 0 {
		return nil, nil, nil, fmt.Errorf("%w: 'ca.crt' key", ErrCertDataMissingInSecret)
	}
	logger.Debug("Successfully retrieved ca.crt from secret.")

	clientCertData, ok = secret.Data["tls.crt"]
	if !ok || len(clientCertData) == 0 {
		return nil, nil, nil, fmt.Errorf("%w: 'tls.crt' key (client certificate)", ErrCertDataMissingInSecret)
	}
	logger.Debug("Successfully retrieved tls.crt (client certificate) from secret.")

	clientKeyData, ok = secret.Data["tls.key"]
	if !ok || len(clientKeyData) == 0 {
		return nil, nil, nil, fmt.Errorf("%w: 'tls.key' key (client key)", ErrCertDataMissingInSecret)
	}
	logger.Debug("Successfully retrieved tls.key (client key) from secret.")

	return caData, clientCertData, clientKeyData, nil
}

// loadMTLSCredentialsFromData loads mTLS credentials from byte slice.
func loadMTLSCredentialsFromData(logger *zap.Logger, aksManaged bool, caCertData, clientCertData, clientKeyData []byte) (credentials.TransportCredentials, error) {
	// Validate PEM data
	if !isValidPEM(caCertData) || !isValidPEM(clientCertData) || !isValidPEM(clientKeyData) {
		return nil, errors.New("invalid PEM data: ensure proper headers and formatting")
	}

	// Load client certificate and key from byte slices
	cert, err := tls.X509KeyPair(clientCertData, clientKeyData)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate and key from data: %w", err)
	}
	logger.Debug("Client certificate and key loaded from data.")

	// Load CA certificate from byte slice
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caCertData) {
		return nil, errors.New("failed to append CA certificate to pool from data")
	}
	logger.Debug("CA certificate appended to pool from data.")

	tlsConfig := &tls.Config{
		ServerName:         hubbleRelayExpectedServerName,
		Certificates:       []tls.Certificate{cert},
		RootCAs:            certPool,
		InsecureSkipVerify: false,
		MinVersion:         tls.VersionTLS12,
	}

	var cred credentials.TransportCredentials
	if aksManaged {
		logger.Debug("AKS Managed: Using experimental NewTLSWithALPNDisabled")
		cred = exp_credentials.NewTLSWithALPNDisabled(tlsConfig)
	} else {
		// For non-AKS, or if experimental is not desired/available for some reason.
		logger.Debug("Non-AKS Managed (or fallback): Using standard NewTLS")
		cred = credentials.NewTLS(tlsConfig)
	}

	return cred, nil
}

// FetchAndLoadMTLSCredentials encapsulates the logic for fetching and loading mTLS credentials.
func FetchAndLoadMTLSCredentials(ctx context.Context, clientset kubernetes.Interface, logger *zap.Logger, secretName, namespace string, isAKSManaged bool) (credentials.TransportCredentials, error) {
	// Fetch mTLS certificates
	caData, clientCertData, clientKeyData, fetchErr := getHubbleMTLSCertificatesFromSecret(
		ctx, clientset, logger, secretName, namespace,
	)
	if fetchErr != nil {
		logger.Error("Failed to fetch mTLS certificates", zap.Error(fetchErr))
		return nil, fmt.Errorf("failed to fetch mTLS certificates: %w", fetchErr)
	}

	// Load credentials
	creds, loadErr := loadMTLSCredentialsFromData(logger, isAKSManaged, caData, clientCertData, clientKeyData)
	if loadErr != nil {
		logger.Error("Failed to load mTLS credentials", zap.Error(loadErr))
		return nil, fmt.Errorf("failed to load mTLS credentials: %w", loadErr)
	}

	return creds, nil
}

// isValidPEM validates if the given data is properly PEM-encoded.
func isValidPEM(data []byte) bool {
	block, _ := pem.Decode(data)
	return block != nil
}
