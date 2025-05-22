// Copyright 2025 Illumio, Inc. All Rights Reserved.

package hubble

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	exp_credentials "github.com/illumio/cloud-operator/internal/pkg/tls"
	"go.uber.org/zap"
	"google.golang.org/grpc/credentials"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	ciliumHubbleRelayExpectedServerName = "ui.hubble-relay.cilium.io"
	ciliumHubbleRelayServiceName        = "hubble-relay"
)

var (
	ErrCertDataMissingInSecret = errors.New("required certificate data (ca.crt, tls.crt, or tls.key) not found in secret")
	ErrHubbleNotFound          = errors.New("Cilium Hubble Relay service not found; disabling Cilium flow collection")
	ErrNoPortsAvailable        = errors.New("Cilium Hubble Relay service has no ports; disabling Cilium flow collection")
)

// getMTLSCertificatesFromSecret fetches ca.crt, tls.crt (client cert), and tls.key (client key)
// from the specified Kubernetes secret.
func getMTLSCertificatesFromSecret(
	ctx context.Context,
	clientset kubernetes.Interface,
	logger *zap.Logger,
	secretName string,
	secretNamespace string,
) (caData, clientCertData, clientKeyData []byte, err error) {

	logger.Debug("Fetching mTLS certificates from Kubernetes secret",
		zap.String("name", secretName),
		zap.String("namespace", secretNamespace))

	secret, err := clientset.CoreV1().Secrets(secretNamespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
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
func loadMTLSCredentialsFromData(logger *zap.Logger, disableALPN bool, caCertData, clientCertData, clientKeyData []byte) (credentials.TransportCredentials, error) {
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
		ServerName:         ciliumHubbleRelayExpectedServerName,
		Certificates:       []tls.Certificate{cert},
		RootCAs:            certPool,
		InsecureSkipVerify: false,
		MinVersion:         tls.VersionTLS12,
	}

	var cred credentials.TransportCredentials
	if disableALPN {
		logger.Info("Disabling ALPN for MTLS connections to Cilium Hubble Relay")
		cred = exp_credentials.NewTLSWithALPNDisabled(tlsConfig, logger)
	}

	return cred, nil
}

// GetTransportCredentials read TLS transport credentials to connect to Hubble from the given Secret.
func GetTransportCredentials(ctx context.Context, clientset kubernetes.Interface,
	logger *zap.Logger, secretName, namespace string, disableALPN bool) (credentials.TransportCredentials, error) {
	// Fetch mTLS certificates
	caData, clientCertData, clientKeyData, fetchErr := getMTLSCertificatesFromSecret(
		ctx, clientset, logger, secretName, namespace,
	)
	if fetchErr != nil {
		return nil, fmt.Errorf("failed to fetch mTLS certificates: %w", fetchErr)
	}

	// Load credentials
	creds, loadErr := loadMTLSCredentialsFromData(logger, disableALPN, caData, clientCertData, clientKeyData)
	if loadErr != nil {
		return nil, fmt.Errorf("failed to load mTLS credentials: %w", loadErr)
	}

	return creds, nil
}

// isValidPEM validates if the given data is properly PEM-encoded.
func isValidPEM(data []byte) bool {
	block, _ := pem.Decode(data)
	return block != nil
}

// DiscoverCiliumHubbleRelay uses a kubernetes clientset in order to discover the address of the hubble-relay service.
func DiscoverCiliumHubbleRelay(ctx context.Context, ciliumNamespace string, clientset kubernetes.Interface) (*v1.Service, error) {
	service, err := clientset.CoreV1().Services(ciliumNamespace).Get(ctx, ciliumHubbleRelayServiceName, metav1.GetOptions{})
	if err != nil {
		return nil, ErrHubbleNotFound
	}

	return service, nil
}

// GetAddressFromService returns the address of the given service to connect a gRPC client to.
func GetAddressFromService(service *v1.Service) (string, error) {
	if len(service.Spec.Ports) == 0 {
		return "", ErrNoPortsAvailable
	}

	address := fmt.Sprintf("%s:%d", service.Spec.ClusterIP, service.Spec.Ports[0].Port)
	return address, nil
}
