// Copyright 2025 Illumio, Inc. All Rights Reserved.

package hubble

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	exp_credentials "github.com/illumio/cloud-operator/internal/pkg/tls"
)

const (
	ciliumHubbleRelayExpectedServerName = "ui.hubble-relay.cilium.io"
	ciliumHubbleRelayServiceName        = "hubble-relay"
)

// hubbleKeepaliveParams probes the long-lived Relay connection so a dead or
// half-open operator<->Relay TCP connection is detected instead of leaving the
// flow stream's Recv() blocked forever. Time is >= Relay's default server-side
// MinTime (5m) so pings are not rejected with GOAWAY "too_many_pings" on an idle
// flow stream.
var hubbleKeepaliveParams = keepalive.ClientParameters{
	Time:                5 * time.Minute,  // ping after 5m of no activity (== server MinTime)
	Timeout:             20 * time.Second, // declare the connection dead if no ack within 20s
	PermitWithoutStream: true,             // keep probing across reconnect gaps
}

// expectedServerName returns the TLS ServerName to use when connecting to Hubble Relay.
// For the default Cilium deployment in kube-system, the certificate uses a special DNS name.
// For all other namespaces, use the standard Kubernetes service FQDN.
func expectedServerName(namespace, serviceName string) string {
	if namespace == "kube-system" {
		return ciliumHubbleRelayExpectedServerName
	}

	return fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace)
}

var (
	ErrCertDataMissingInSecret = errors.New("required certificate data (ca.crt, tls.crt, or tls.key) not found in secret")
	ErrHubbleNotFound          = errors.New("cilium Hubble Relay service not found; disabling Cilium flow collection")
	ErrNoPortsAvailable        = errors.New("cilium Hubble Relay service has no ports; disabling Cilium flow collection")
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

// loadMTLSConfigFromData loads mTLS credentials from byte slice.
func loadMTLSConfigFromData(logger *zap.Logger, caCertData, clientCertData, clientKeyData []byte, namespace string) (*tls.Config, error) {
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

	serverName := expectedServerName(namespace, ciliumHubbleRelayServiceName)

	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: false,
		MinVersion:         tls.VersionTLS12,
		RootCAs:            certPool,
		ServerName:         serverName,
	}

	return tlsConfig, nil
}

// GetTLSConfig reads TLS config to connect to Hubble from the given Secret.
func GetTLSConfig(ctx context.Context, clientset kubernetes.Interface,
	logger *zap.Logger, secretName, namespace string) (*tls.Config, error) {
	// Fetch mTLS certificates
	caData, clientCertData, clientKeyData, fetchErr := getMTLSCertificatesFromSecret(
		ctx, clientset, logger, secretName, namespace,
	)
	if fetchErr != nil {
		return nil, fmt.Errorf("failed to fetch mTLS certificates: %w", fetchErr)
	}

	// Load config
	tlsConfig, loadErr := loadMTLSConfigFromData(logger, caData, clientCertData, clientKeyData, namespace)
	if loadErr != nil {
		return nil, fmt.Errorf("failed to load mTLS credentials: %w", loadErr)
	}

	return tlsConfig, nil
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

// generateTransportCredentials generates TransportCredentials from TLS config.
func generateTransportCredentials(tlsConfig *tls.Config, logger *zap.Logger, disableALPN bool) credentials.TransportCredentials {
	var cred credentials.TransportCredentials

	if disableALPN {
		logger.Info("Disabling ALPN for MTLS connections to Cilium Hubble Relay")
		cred = exp_credentials.NewTLSWithALPNDisabled(tlsConfig, logger)
	} else {
		cred = credentials.NewTLS(tlsConfig)
	}

	return cred
}

// ConnectToHubbleRelay handles the connection logic for Hubble Relay.
func ConnectToHubbleRelay(ctx context.Context, logger *zap.Logger, hubbleAddress string, tlsConfig *tls.Config, disableALPN bool) (*grpc.ClientConn, error) {
	var creds credentials.TransportCredentials

	if tlsConfig != nil {
		logger.Debug("Attempting mTLS connection to Cilium Hubble Relay", zap.String("address", hubbleAddress))

		if disableALPN {
			logger.Info("Disabling ALPN for mTLS connection")
			creds = exp_credentials.NewTLSWithALPNDisabled(tlsConfig, logger)
		} else {
			creds = credentials.NewTLS(tlsConfig)
		}
	} else {
		logger.Debug("Using insecure connection to Cilium Hubble Relay", zap.String("address", hubbleAddress))

		creds = insecure.NewCredentials()
	}

	conn, err := grpc.NewClient(
		hubbleAddress,
		grpc.WithTransportCredentials(creds),
		grpc.WithNoProxy(),
		grpc.WithKeepaliveParams(hubbleKeepaliveParams),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Cilium Hubble Relay: %w", err)
	}

	return conn, nil
}
