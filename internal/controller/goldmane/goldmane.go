// Copyright 2025 Illumio, Inc. All Rights Reserved.

package goldmane

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// GoldmaneServiceName is the name of the Goldmane service in Kubernetes.
	GoldmaneServiceName = "goldmane"
	// GoldmaneServicePort is the port of the Goldmane service.
	GoldmaneServicePort = 7443
	// GoldmaneMTLSSecretName is the name of the secret containing the mTLS certificates.
	GoldmaneMTLSSecretName = "goldmane-key-pair"
)

var (
	// ErrGoldmaneNotFound indicates that the Goldmane service was not found in the cluster.
	ErrGoldmaneNotFound = errors.New("goldmane service not found; disabling Calico flow collection")
	// ErrCertDataMissingInSecret indicates required certificate data is missing from the secret.
	ErrCertDataMissingInSecret = errors.New("required certificate data (tls.crt or tls.key) not found in secret")
)

// DiscoverGoldmane discovers the Goldmane service in the given namespace.
func DiscoverGoldmane(ctx context.Context, calicoNamespace string, clientset kubernetes.Interface, logger *zap.Logger) (*v1.Service, error) {
	logger.Debug("Discovering Goldmane service", zap.String("namespace", calicoNamespace))

	service, err := clientset.CoreV1().Services(calicoNamespace).Get(ctx, GoldmaneServiceName, metav1.GetOptions{})
	if err != nil {
		logger.Debug("Goldmane service not found", zap.String("namespace", calicoNamespace), zap.Error(err))

		return nil, ErrGoldmaneNotFound
	}

	logger.Debug("Goldmane service discovered",
		zap.String("name", service.Name),
		zap.String("namespace", service.Namespace))

	return service, nil
}

// GetAddressFromService returns the address of the Goldmane service to connect a gRPC client to.
func GetAddressFromService(service *v1.Service) string {
	return fmt.Sprintf("%s.%s.svc:%d", service.Name, service.Namespace, GoldmaneServicePort)
}

// GetTLSConfig retrieves the TLS configuration from the Goldmane secret.
func GetTLSConfig(ctx context.Context, clientset kubernetes.Interface, logger *zap.Logger, calicoNamespace string) (*tls.Config, error) {
	logger.Debug("Getting Goldmane TLS config", zap.String("namespace", calicoNamespace))

	secret, err := clientset.CoreV1().Secrets(calicoNamespace).Get(ctx, GoldmaneMTLSSecretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get Goldmane secret '%s' in namespace '%s': %w", GoldmaneMTLSSecretName, calicoNamespace, err)
	}

	// Extract certificate and key from secret
	certPEM, ok := secret.Data["tls.crt"]
	if !ok || len(certPEM) == 0 {
		return nil, fmt.Errorf("%w: 'tls.crt' key", ErrCertDataMissingInSecret)
	}

	logger.Debug("Successfully retrieved tls.crt from secret")

	keyPEM, ok := secret.Data["tls.key"]
	if !ok || len(keyPEM) == 0 {
		return nil, fmt.Errorf("%w: 'tls.key' key", ErrCertDataMissingInSecret)
	}

	logger.Debug("Successfully retrieved tls.key from secret")

	// Parse the certificate and key
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate and key: %w", err)
	}

	// Create a CA cert pool using the same certificate as the CA
	// (Goldmane uses self-signed certificates)
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(certPEM) {
		return nil, errors.New("failed to add CA certificate to pool")
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS12,
	}

	return tlsConfig, nil
}

// ConnectToGoldmane establishes a gRPC connection to the Goldmane service.
func ConnectToGoldmane(logger *zap.Logger, address string, tlsConfig *tls.Config) (*grpc.ClientConn, error) {
	logger.Debug("Connecting to Goldmane", zap.String("address", address))

	creds := credentials.NewTLS(tlsConfig)

	conn, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(creds),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Goldmane: %w", err)
	}

	logger.Info("Successfully connected to Calico Goldmane", zap.String("address", address))

	return conn, nil
}
