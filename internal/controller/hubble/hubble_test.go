// Copyright 2025 Illumio, Inc. All Rights Reserved.

package hubble

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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

type HubbleSuite struct {
	suite.Suite
}

var (
	caCertPEM, clientCertPEM, clientKeyPEM string
)

func TestHubbleSuite(t *testing.T) {
	suite.Run(t, new(HubbleSuite))
}

func init() {
	var err error
	caCertPEM, clientCertPEM, clientKeyPEM, err = GenerateTestCerts()
	if err != nil {
		panic("Failed to generate test certificates: " + err.Error())
	}
}

func (suite *HubbleSuite) TestFetchAndLoadMTLSCredentials() {
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

			creds, err := GetTransportCredentials(ctx, clientset, logger, "hubble-relay-client-certs", "kube-system", true)

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

func (suite *HubbleSuite) TestGetHubbleMTLSCertificatesFromSecret() {
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

			caData, clientCertData, clientKeyData, err := getMTLSCertificatesFromSecret(ctx, clientset, logger, "hubble-relay-client-certs", "kube-system")

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

func (suite *HubbleSuite) TestLoadMTLSCredentialsFromData() {
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

func (suite *HubbleSuite) TestGetHubbleMTLSCertificatesFromSecret_MissingData() {
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

	_, _, _, err := getMTLSCertificatesFromSecret(ctx, clientset, logger, "hubble-relay-client-certs", "kube-system")
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

func (suite *HubbleSuite) TestDiscoverHubbleRelay() {
	ctx := context.Background()

	hubbleRelayServiceKubeSystem := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ciliumHubbleRelayServiceName,
			Namespace: "kube-system",
		},
		Spec: v1.ServiceSpec{
			ClusterIP: "10.0.0.1",
			Ports: []v1.ServicePort{
				{Port: 80},
			},
		},
	}

	hubbleRelayServiceNoPortsKubeSystem := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ciliumHubbleRelayServiceName,
			Namespace: "kube-system",
		},
		Spec: v1.ServiceSpec{
			ClusterIP: "10.0.0.1",
			Ports:     []v1.ServicePort{}, // No ports
		},
	}

	hubbleRelayServiceAKSKubeSystem := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ciliumHubbleRelayServiceName,
			Namespace: "kube-system",
			Annotations: map[string]string{
				"meta.helm.sh/release-name": "aks-managed-hubble",
			},
		},
		Spec: v1.ServiceSpec{
			ClusterIP: "10.0.0.1",
			Ports: []v1.ServicePort{
				{Port: 80},
			},
		},
	}

	hubbleRelayServiceOtherNS := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ciliumHubbleRelayServiceName,
			Namespace: "other-namespace", // Different namespace
		},
		Spec: v1.ServiceSpec{
			ClusterIP: "10.0.0.2",
			Ports: []v1.ServicePort{
				{Port: 443},
			},
		},
	}

	tests := map[string]struct {
		serviceToCreate  *v1.Service // Service to pre-populate in the fake client
		namespaceToQuery string      // Namespace argument for DiscoverCiliumHubbleRelay
		expectedService  *v1.Service // The expected service object to be returned
		expectedError    error       // Expected error object (e.g., ErrHubbleNotFound or nil)
	}{
		"successful discovery in kube-system": {
			serviceToCreate:  hubbleRelayServiceKubeSystem,
			namespaceToQuery: "kube-system",
			expectedService:  hubbleRelayServiceKubeSystem, // Expect the created service object
			expectedError:    nil,
		},
		"service not found in specified namespace": {
			serviceToCreate:  nil, // No service will be created in the clientset
			namespaceToQuery: "kube-system",
			expectedService:  nil,
			expectedError:    ErrHubbleNotFound,
		},
		"service exists in different namespace, not found in queried namespace": {
			serviceToCreate:  hubbleRelayServiceOtherNS, // Service created in "other-namespace"
			namespaceToQuery: "kube-system",             // Querying "kube-system"
			expectedService:  nil,
			expectedError:    ErrHubbleNotFound,
		},
		"service found but has no ports": {
			serviceToCreate:  hubbleRelayServiceNoPortsKubeSystem,
			namespaceToQuery: "kube-system",
			expectedService:  hubbleRelayServiceNoPortsKubeSystem, // Function should return the service as is
			expectedError:    nil,                                 // No error, as port checking is removed
		},
		"aks managed service discovered successfully": {
			serviceToCreate:  hubbleRelayServiceAKSKubeSystem,
			namespaceToQuery: "kube-system",
			expectedService:  hubbleRelayServiceAKSKubeSystem,
			expectedError:    nil,
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			clientset := fake.NewSimpleClientset()
			if tt.serviceToCreate != nil {
				// Create the service in the namespace defined within its ObjectMeta
				_, err := clientset.CoreV1().Services(tt.serviceToCreate.ObjectMeta.Namespace).Create(context.TODO(), tt.serviceToCreate, metav1.CreateOptions{})
				assert.NoError(suite.T(), err, "Failed to create service for test setup")
			}

			discoveredService, err := DiscoverCiliumHubbleRelay(ctx, tt.namespaceToQuery, clientset)

			// Compare the returned service object
			// Note: When comparing Kubernetes objects, fake clients might return copies.
			// assert.Equal for testify usually handles deep comparisons for structs.
			// If tt.expectedService is nil, discoveredService should also be nil.
			// If there are issues with complex fields (like TypeMeta, ResourceVersion),
			// you might need to use a more specific comparison (e.g., equality.Semantic.DeepEqual or field-by-field).
			assert.Equal(suite.T(), tt.expectedService, discoveredService)

			// Compare the error
			if tt.expectedError != nil {
				assert.ErrorIs(suite.T(), err, tt.expectedError)
				// You could also check the exact error message if needed, but ErrorIs is generally preferred.
				// assert.EqualError(suite.T(), err, tt.expectedError.Error())
			} else {
				assert.NoError(suite.T(), err)
			}
		})
	}
}

func TestGetHubbleRelayAddress(t *testing.T) {
	tests := map[string]struct {
		service      *v1.Service
		expectedAddr string
		expectedErr  error
	}{
		"successful discovery": {
			service: &v1.Service{
				Spec: v1.ServiceSpec{
					ClusterIP: "10.0.0.1",
					Ports: []v1.ServicePort{
						{Port: 8080, Name: "grpc"},
						{Port: 4040, Name: "http"},
					},
				},
			},
			expectedAddr: "10.0.0.1:8080",
			expectedErr:  nil,
		},
		"service with no ports": {
			service: &v1.Service{
				Spec: v1.ServiceSpec{
					ClusterIP: "10.0.0.2",
					Ports:     []v1.ServicePort{},
				},
			},
			expectedAddr: "",
			expectedErr:  ErrNoPortsAvailable,
		},
		"service with single port": {
			service: &v1.Service{
				Spec: v1.ServiceSpec{
					ClusterIP: "192.168.1.100",
					Ports: []v1.ServicePort{
						{Port: 443},
					},
				},
			},
			expectedAddr: "192.168.1.100:443",
			expectedErr:  nil,
		},
		"service with port 0 (valid port number)": {
			service: &v1.Service{
				Spec: v1.ServiceSpec{
					ClusterIP: "10.10.10.10",
					Ports: []v1.ServicePort{
						{Port: 0}, // Port 0 can mean "assign a port" in some contexts, but here it's just a number
					},
				},
			},
			expectedAddr: "10.10.10.10:0",
			expectedErr:  nil,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			addr, err := GetAddressFromService(tt.service)

			assert.Equal(t, tt.expectedAddr, addr)

			if tt.expectedErr != nil {
				assert.EqualError(t, err, tt.expectedErr.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
