// Copyright 2026 Illumio, Inc. All Rights Reserved.

package tls

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// writeServerTestCertificate writes a self-signed certificate and key for
// localhost and returns the file paths and a pool that trusts the certificate.
func writeServerTestCertificate(t *testing.T) (string, string, *x509.CertPool) {
	t.Helper()

	dir := t.TempDir()
	pool := writeServerTestCertificateTo(t, dir, 1)

	return filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"), pool
}

// writeServerTestCertificateTo writes a self-signed certificate for localhost
// with the given serial number to tls.crt and tls.key in dir, replacing any
// existing files, and returns a pool that trusts the certificate.
func writeServerTestCertificateTo(t *testing.T, dir string, serial int64) *x509.CertPool {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	require.NoError(t, err)

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "tls.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tls.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))

	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AddCert(cert)

	return pool
}

func TestLoadServerConfig(t *testing.T) {
	certFile, keyFile, _ := writeServerTestCertificate(t)

	tests := map[string]struct {
		settings   ServerSettings
		wantConfig bool
		wantErr    bool
	}{
		"no certificate configured": {
			settings: ServerSettings{},
		},
		"certificate and key configured": {
			settings:   ServerSettings{CertFile: certFile, KeyFile: keyFile},
			wantConfig: true,
		},
		"certificate without key": {
			settings: ServerSettings{CertFile: certFile},
			wantErr:  true,
		},
		"missing certificate file": {
			settings: ServerSettings{CertFile: "/nonexistent/tls.crt", KeyFile: keyFile},
			wantErr:  true,
		},
		"TLS required without a certificate": {
			settings: ServerSettings{Required: true},
			wantErr:  true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			config, err := LoadServerConfig(zap.NewNop(), tt.settings)
			if tt.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)

			if !tt.wantConfig {
				assert.Nil(t, config)

				return
			}

			require.NotNil(t, config)
			assert.Empty(t, config.Certificates, "the certificate is served through GetCertificate")
			assert.NotNil(t, config.GetCertificate)
			assert.Equal(t, uint16(tls.VersionTLS12), config.MinVersion)
		})
	}
}

// prepareTestServerConn runs PrepareServerConn on the server end of a pipe and
// returns its result once client has run on the other end.
func prepareTestServerConn(t *testing.T, opts ServerConnOptions, client func(net.Conn)) (net.Conn, error) {
	t.Helper()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})

	go client(clientConn)

	return PrepareServerConn(t.Context(), serverConn, opts)
}

func TestPrepareServerConnPlainText(t *testing.T) {
	opts := ServerConnOptions{FirstByteTimeout: time.Second, HandshakeTimeout: time.Second}

	conn, err := prepareTestServerConn(t, opts, func(c net.Conn) {
		_, _ = c.Write([]byte("<14>hello\n"))
	})
	require.NoError(t, err)

	line, err := bufio.NewReader(conn).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "<14>hello\n", line, "the peeked first byte must not be lost")
}

func TestPrepareServerConnTLS(t *testing.T) {
	certFile, keyFile, pool := writeServerTestCertificate(t)

	config, err := LoadServerConfig(zap.NewNop(), ServerSettings{CertFile: certFile, KeyFile: keyFile})
	require.NoError(t, err)

	opts := ServerConnOptions{Config: config, FirstByteTimeout: time.Second, HandshakeTimeout: time.Second}

	conn, err := prepareTestServerConn(t, opts, func(c net.Conn) {
		client := tls.Client(c, &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12})
		_, _ = client.Write([]byte("<14>hello\n"))
	})
	require.NoError(t, err)

	_, isTLS := conn.(*tls.Conn)
	assert.True(t, isTLS)

	line, err := bufio.NewReader(conn).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "<14>hello\n", line)
}

func TestPrepareServerConnRejects(t *testing.T) {
	certFile, keyFile, _ := writeServerTestCertificate(t)

	config, err := LoadServerConfig(zap.NewNop(), ServerSettings{CertFile: certFile, KeyFile: keyFile})
	require.NoError(t, err)

	startTLS := func(c net.Conn) {
		_ = tls.Client(c, &tls.Config{InsecureSkipVerify: true}).Handshake() //nolint:gosec // the server is expected to reject the handshake
	}
	sendPlain := func(c net.Conn) {
		_, _ = c.Write([]byte("<14>hello\n"))
	}
	stayIdle := func(net.Conn) {}

	tests := map[string]struct {
		opts    ServerConnOptions
		client  func(net.Conn)
		wantErr error
	}{
		"TLS without a certificate configured": {
			opts:    ServerConnOptions{},
			client:  startTLS,
			wantErr: ErrServerTLSNotConfigured,
		},
		"plain text when TLS is required": {
			opts:    ServerConnOptions{Config: config, Required: true},
			client:  sendPlain,
			wantErr: ErrPlainTextNotAllowed,
		},
		"silent connection": {
			opts:   ServerConnOptions{Config: config},
			client: stayIdle,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tt.opts.FirstByteTimeout = 100 * time.Millisecond
			tt.opts.HandshakeTimeout = 100 * time.Millisecond

			conn, err := prepareTestServerConn(t, tt.opts, tt.client)
			require.Error(t, err)
			assert.Nil(t, conn)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}

// servedCertificateSerial performs a TLS handshake with a server using config
// and returns the serial number of the certificate the server presented.
func servedCertificateSerial(t *testing.T, config *tls.Config) int64 {
	t.Helper()

	serverConn, clientConn := net.Pipe()

	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})

	go func() {
		_ = tls.Server(serverConn, config).HandshakeContext(t.Context())
	}()

	client := tls.Client(clientConn, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // the test inspects the served certificate itself
	require.NoError(t, client.HandshakeContext(t.Context()))

	return client.ConnectionState().PeerCertificates[0].SerialNumber.Int64()
}

func TestLoadServerConfigReloadsRenewedCertificate(t *testing.T) {
	dir := t.TempDir()
	writeServerTestCertificateTo(t, dir, 1)

	core, logs := observer.New(zap.WarnLevel)

	config, err := LoadServerConfig(zap.New(core), ServerSettings{
		CertFile: filepath.Join(dir, "tls.crt"),
		KeyFile:  filepath.Join(dir, "tls.key"),
	})
	require.NoError(t, err)

	assert.Equal(t, int64(1), servedCertificateSerial(t, config))

	// Renew the certificate without recreating the config, as when an updated
	// Secret is written to a mounted volume.
	writeServerTestCertificateTo(t, dir, 2)
	assert.Equal(t, int64(2), servedCertificateSerial(t, config), "a new connection must get the renewed certificate")

	// A certificate that fails to load keeps the last good certificate in use.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tls.crt"), []byte("not a certificate"), 0o600))
	assert.Equal(t, int64(2), servedCertificateSerial(t, config), "a failed reload must keep serving the last good certificate")

	warnings := logs.FilterMessageSnippet("Failed to reload the TLS certificate").All()
	require.Len(t, warnings, 1, "a failed reload must be logged")

	// Once the files are valid again, the new certificate is served.
	writeServerTestCertificateTo(t, dir, 3)
	assert.Equal(t, int64(3), servedCertificateSerial(t, config))
}
