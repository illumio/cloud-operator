// Copyright 2025 Illumio, Inc. All Rights Reserved.

package hubble

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	exp_credentials "github.com/illumio/cloud-operator/internal/tls"
	"go.uber.org/zap"
	"google.golang.org/grpc/credentials"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	hubbleRelayExpectedServerName        = "ui.hubble-relay.cilium.io"
	ciliumHubbleRelayServiceName  string = "hubble-relay"
)

var (
	ErrCertDataMissingInSecret = errors.New("required certificate data (ca.crt, tls.crt, or tls.key) not found in secret")
	ErrHubbleNotFound          = errors.New("hubble Relay service not found; disabling Cilium flow collection")
	ErrNoPortsAvailable        = errors.New("hubble Relay service has no ports; disabling Cilium flow collection")
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
		cred = exp_credentials.NewTLSWithALPNDisabled(tlsConfig, logger)
	}

	return cred, nil
}

// GetHubbleTransportCredentials read TLS transport credentials to connect to Hubble from the given Secret.
func GetHubbleCTransportCredentials(ctx context.Context, clientset kubernetes.Interface,
	logger *zap.Logger, secretName, namespace string, isAKSManaged bool) (credentials.TransportCredentials, error) {
	// Fetch mTLS certificates
	caData, clientCertData, clientKeyData, fetchErr := getHubbleMTLSCertificatesFromSecret(
		ctx, clientset, logger, secretName, namespace,
	)
	if fetchErr != nil {
		return nil, fmt.Errorf("failed to fetch mTLS certificates: %w", fetchErr)
	}

	// Load credentials
	creds, loadErr := loadMTLSCredentialsFromData(logger, isAKSManaged, caData, clientCertData, clientKeyData)
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

// DiscoverCiliumHubbleRelayAddress uses a kubernetes clientset in order to discover the address of the hubble-relay se…
func DiscoverCiliumHubbleRelay(ctx context.Context, ciliumNamespace string, clientset kubernetes.Interface) (*v1.Service, error) {
	service, err := clientset.CoreV1().Services(ciliumNamespace).Get(ctx, ciliumHubbleRelayServiceName, metav1.GetOptions{})
	if err != nil {
		return nil, ErrHubbleNotFound
	}

	return service, nil
}

// GetHubbleRelayAddress returns the address of the hubble-relay service to connect a gRPC client to.
func GetHubbleRelayAddress(service *v1.Service) (string, error) {
	if len(service.Spec.Ports) == 0 {
		return "", ErrNoPortsAvailable
	}

	address := fmt.Sprintf("%s:%d", service.Spec.ClusterIP, service.Spec.Ports[0].Port)
	return address, nil
}
