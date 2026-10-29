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
)

// writeServerTestCertificate writes a self-signed certificate and key for
// localhost and returns the file paths and a pool that trusts the certificate.
func writeServerTestCertificate(t *testing.T) (string, string, *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
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

	dir := t.TempDir()
	certFile := filepath.Join(dir, "tls.crt")
	keyFile := filepath.Join(dir, "tls.key")

	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))

	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AddCert(cert)

	return certFile, keyFile, pool
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
			config, err := LoadServerConfig(tt.settings)
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
			assert.Len(t, config.Certificates, 1)
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

	config, err := LoadServerConfig(ServerSettings{CertFile: certFile, KeyFile: keyFile})
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

	config, err := LoadServerConfig(ServerSettings{CertFile: certFile, KeyFile: keyFile})
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
