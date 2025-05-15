package mtls

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

type MTLSuite struct {
	suite.Suite
}

var (
	caCertPEM, clientCertPEM, clientKeyPEM string
)

func TestMTLSuite(t *testing.T) {
	suite.Run(t, new(MTLSuite))
}

func init() {
	var err error
	caCertPEM, clientCertPEM, clientKeyPEM, err = GenerateTestCerts()
	if err != nil {
		panic("Failed to generate test certificates: " + err.Error())
	}
}

func (suite *MTLSuite) TestFetchAndLoadMTLSCredentials() {
	tests := map[string]struct {
		secretData         map[string][]byte
		expectedErr        error
		expectedCredsExist bool
	}{
		"valid secret": {
			secretData: map[string][]byte{
				"ca.crt":  []byte(caCertPEM),
				"tls.crt": []byte(clientCertPEM),
				"tls.key": []byte(clientKeyPEM),
			},
			expectedErr:        nil,
			expectedCredsExist: true,
		},
		"missing ca.crt": {
			secretData: map[string][]byte{
				"tls.crt": []byte(clientCertPEM),
				"tls.key": []byte(clientKeyPEM),
			},
			expectedErr:        ErrCertDataMissingInSecret,
			expectedCredsExist: false,
		},
		"missing tls.crt": {
			secretData: map[string][]byte{
				"ca.crt":  []byte(caCertPEM),
				"tls.key": []byte(clientKeyPEM),
			},
			expectedErr:        ErrCertDataMissingInSecret,
			expectedCredsExist: false,
		},
		"missing tls.key": {
			secretData: map[string][]byte{
				"ca.crt":  []byte(caCertPEM),
				"tls.crt": []byte(clientCertPEM),
			},
			expectedErr:        ErrCertDataMissingInSecret,
			expectedCredsExist: false,
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			logger := zap.NewExample()
			ctx := context.Background()
			clientset := k8sfake.NewSimpleClientset(&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hubble-relay-client-certs",
					Namespace: "kube-system",
				},
				Data: tt.secretData,
			})

			creds, err := FetchAndLoadMTLSCredentials(ctx, clientset, logger, "hubble-relay-client-certs", "kube-system", true)

			if tt.expectedErr != nil {
				suite.ErrorIs(err, tt.expectedErr)
			} else {
				suite.NoError(err)
			}

			if tt.expectedCredsExist {
				suite.NotNil(creds)
			} else {
				suite.Nil(creds)
			}
		})
	}
}

func (suite *MTLSuite) TestGetHubbleMTLSCertificatesFromSecret() {
	tests := map[string]struct {
		secretData  map[string][]byte
		expectedErr error
	}{
		"valid secret": {
			secretData: map[string][]byte{
				"ca.crt":  []byte(caCertPEM),
				"tls.crt": []byte(clientCertPEM),
				"tls.key": []byte(clientKeyPEM),
			},
			expectedErr: nil,
		},
		"missing ca.crt": {
			secretData: map[string][]byte{
				"tls.crt": []byte(clientCertPEM),
				"tls.key": []byte(clientKeyPEM),
			},
			expectedErr: ErrCertDataMissingInSecret,
		},
		"missing tls.crt": {
			secretData: map[string][]byte{
				"ca.crt":  []byte(caCertPEM),
				"tls.key": []byte(clientKeyPEM),
			},
			expectedErr: ErrCertDataMissingInSecret,
		},
		"missing tls.key": {
			secretData: map[string][]byte{
				"ca.crt":  []byte(caCertPEM),
				"tls.crt": []byte(clientCertPEM),
			},
			expectedErr: ErrCertDataMissingInSecret,
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			logger := zap.NewExample()
			ctx := context.Background()
			clientset := k8sfake.NewSimpleClientset(&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hubble-relay-client-certs",
					Namespace: "kube-system",
				},
				Data: tt.secretData,
			})

			caData, clientCertData, clientKeyData, err := getHubbleMTLSCertificatesFromSecret(ctx, clientset, logger, "hubble-relay-client-certs", "kube-system")

			if tt.expectedErr != nil {
				suite.ErrorIs(err, tt.expectedErr)
			} else {
				suite.NoError(err)
				suite.Equal(caCertPEM, string(caData))
				suite.Equal(clientCertPEM, string(clientCertData))
				suite.Equal(clientKeyPEM, string(clientKeyData))
			}
		})
	}
}

func (suite *MTLSuite) TestLoadMTLSCredentialsFromData() {
	logger := zap.NewExample()
	caCertData := []byte(caCertPEM)
	clientCertData := []byte(clientCertPEM)
	clientKeyData := []byte(clientKeyPEM)

	creds, err := loadMTLSCredentialsFromData(logger, true, caCertData, clientCertData, clientKeyData)
	suite.NoError(err)
	suite.NotNil(creds)

	// Ensure the returned credentials are of the correct type
	if reflect.TypeOf(creds).String() != "credentials.TransportCredentials" {
		suite.Fail("Expected TransportCredentials, got %T", creds)
	}
}

func (suite *MTLSuite) TestGetHubbleMTLSCertificatesFromSecret_MissingData() {
	logger := zap.NewExample()
	ctx := context.Background()
	clientset := k8sfake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hubble-relay-client-certs",
			Namespace: "kube-system",
		},
		Data: map[string][]byte{
			"ca.crt": []byte(caCertPEM),
		},
	})

	_, _, _, err := getHubbleMTLSCertificatesFromSecret(ctx, clientset, logger, "hubble-relay-client-certs", "kube-system")
	suite.Error(err)
	suite.ErrorIs(err, ErrCertDataMissingInSecret)
}

// GenerateTestCerts generates a self-signed certificate and private key for testing purposes.
// It returns the CA certificate, client certificate, and client private key as PEM-encoded strings.
func GenerateTestCerts() (caCertPEM, clientCertPEM, clientKeyPEM string, err error) {
	// Generate a private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", "", err
	}

	// Create a certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test Org"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour), // 1 year validity
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	// Self-sign the certificate
	certBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return "", "", "", err
	}

	// Encode the certificate to PEM format
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})

	// Encode the private key to PEM format
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	// Return the same certificate as the CA certificate for simplicity
	return string(certPEM), string(certPEM), string(keyPEM), nil
}
