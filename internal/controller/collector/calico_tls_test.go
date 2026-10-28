// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

const testLogCollectorRecord = `<14>Oct 26 21:10:09 calico-ent tigera_secure: {"start_time":1793048988,"source_ip":"10.244.163.79","source_port":59106,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`

// recordingSink records cached flows so tests can wait for them.
type recordingSink struct {
	mu    sync.Mutex
	flows []pb.Flow
	seen  chan struct{}
}

func newRecordingSink() *recordingSink {
	return &recordingSink{seen: make(chan struct{}, 16)}
}

func (s *recordingSink) CacheFlow(_ context.Context, flow pb.Flow) error {
	s.mu.Lock()
	s.flows = append(s.flows, flow)
	s.mu.Unlock()

	s.seen <- struct{}{}

	return nil
}

func (s *recordingSink) IncrementFlowsReceived() {}

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.flows)
}

// writeTestCertificate writes a self-signed certificate and key for localhost
// and returns the file paths and a pool that trusts the certificate.
func writeTestCertificate(t *testing.T) (string, string, *x509.CertPool) {
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

func TestLoadCalicoSyslogTLSConfig(t *testing.T) {
	certFile, keyFile, _ := writeTestCertificate(t)

	tests := map[string]struct {
		tlsSettings CalicoSyslogTLS
		wantConfig  bool
		wantErr     bool
	}{
		"no certificate configured": {
			tlsSettings: CalicoSyslogTLS{},
		},
		"certificate and key configured": {
			tlsSettings: CalicoSyslogTLS{CertFile: certFile, KeyFile: keyFile},
			wantConfig:  true,
		},
		"certificate without key": {
			tlsSettings: CalicoSyslogTLS{CertFile: certFile},
			wantErr:     true,
		},
		"missing certificate file": {
			tlsSettings: CalicoSyslogTLS{CertFile: "/nonexistent/tls.crt", KeyFile: keyFile},
			wantErr:     true,
		},
		"TLS required without a certificate": {
			tlsSettings: CalicoSyslogTLS{Required: true},
			wantErr:     true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			config, err := loadCalicoSyslogTLSConfig(tt.tlsSettings)
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

// startTestCollector serves a single connection with handleConn and returns
// the client end of the connection.
func startTestCollector(t *testing.T, collector *CalicoCollector) net.Conn {
	t.Helper()

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = listener.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}

		collector.handleConn(ctx, zap.NewNop(), conn)
	}()

	var dialer net.Dialer

	conn, err := dialer.DialContext(context.Background(), "tcp", listener.Addr().String())
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

func waitForFlow(t *testing.T, sink *recordingSink) {
	t.Helper()

	select {
	case <-sink.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a flow")
	}
}

// assertConnClosed asserts that the collector closed the connection.
func assertConnClosed(t *testing.T, conn net.Conn) {
	t.Helper()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))

	_, err := conn.Read(make([]byte, 1))
	require.Error(t, err)

	// A closed connection reads as EOF or a reset; a timeout means it is still open.
	var netErr net.Error
	if errors.As(err, &netErr) {
		assert.False(t, netErr.Timeout(), "connection should be closed, not idle")
	}
}

func newTestCalicoCollector(t *testing.T, sink FlowSink, settings CalicoSyslogTLS) *CalicoCollector {
	t.Helper()

	config, err := loadCalicoSyslogTLSConfig(settings)
	require.NoError(t, err)

	collector := NewCalicoCollector(zap.NewNop(), "0", settings, sink, nil)
	collector.tlsConfig = config
	collector.firstByteTimeout = time.Second
	collector.handshakeTimeout = time.Second

	return collector
}

func TestCalicoCollectorPlainConnection(t *testing.T) {
	certFile, keyFile, _ := writeTestCertificate(t)

	tests := map[string]CalicoSyslogTLS{
		"no certificate configured":        {},
		"certificate configured, optional": {CertFile: certFile, KeyFile: keyFile},
	}

	for name, settings := range tests {
		t.Run(name, func(t *testing.T) {
			sink := newRecordingSink()
			conn := startTestCollector(t, newTestCalicoCollector(t, sink, settings))

			_, err := conn.Write([]byte(testLogCollectorRecord + "\n"))
			require.NoError(t, err)

			waitForFlow(t, sink)
			assert.Equal(t, 1, sink.count())
		})
	}
}

func TestCalicoCollectorTLSConnection(t *testing.T) {
	certFile, keyFile, pool := writeTestCertificate(t)

	tests := map[string]CalicoSyslogTLS{
		"certificate configured, optional": {CertFile: certFile, KeyFile: keyFile},
		"certificate configured, required": {CertFile: certFile, KeyFile: keyFile, Required: true},
	}

	for name, settings := range tests {
		t.Run(name, func(t *testing.T) {
			sink := newRecordingSink()
			rawConn := startTestCollector(t, newTestCalicoCollector(t, sink, settings))

			conn := tls.Client(rawConn, &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12})
			require.NoError(t, conn.HandshakeContext(context.Background()))

			// Two records in one write must both be read after the handshake.
			_, err := conn.Write([]byte(testLogCollectorRecord + "\n" + testLogCollectorRecord + "\n"))
			require.NoError(t, err)

			waitForFlow(t, sink)
			waitForFlow(t, sink)
			assert.Equal(t, 2, sink.count())
		})
	}
}

func TestCalicoCollectorRejectsTLSWithoutCertificate(t *testing.T) {
	sink := newRecordingSink()
	rawConn := startTestCollector(t, newTestCalicoCollector(t, sink, CalicoSyslogTLS{}))

	conn := tls.Client(rawConn, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // the server is expected to reject the handshake
	require.Error(t, conn.HandshakeContext(context.Background()))
	assert.Equal(t, 0, sink.count())
}

func TestCalicoCollectorRejectsPlainWhenTLSRequired(t *testing.T) {
	certFile, keyFile, _ := writeTestCertificate(t)

	sink := newRecordingSink()
	conn := startTestCollector(t, newTestCalicoCollector(t, sink, CalicoSyslogTLS{CertFile: certFile, KeyFile: keyFile, Required: true}))

	_, err := conn.Write([]byte(testLogCollectorRecord + "\n"))
	require.NoError(t, err)

	assertConnClosed(t, conn)
	assert.Equal(t, 0, sink.count())
}

func TestCalicoCollectorClosesSilentConnection(t *testing.T) {
	sink := newRecordingSink()
	conn := startTestCollector(t, newTestCalicoCollector(t, sink, CalicoSyslogTLS{}))

	// Send nothing: the collector must give up after the first-byte timeout.
	assertConnClosed(t, conn)
}

func TestCalicoCollectorClosesStalledHandshake(t *testing.T) {
	certFile, keyFile, _ := writeTestCertificate(t)

	sink := newRecordingSink()
	conn := startTestCollector(t, newTestCalicoCollector(t, sink, CalicoSyslogTLS{CertFile: certFile, KeyFile: keyFile}))

	// Send only the first byte of a TLS record and stall the handshake.
	_, err := conn.Write([]byte{tlsRecordTypeHandshake})
	require.NoError(t, err)

	assertConnClosed(t, conn)
}

func TestCalicoCollectorWarnsOnceAboutAggregatedRecords(t *testing.T) {
	sink := newRecordingSink()

	var dropped atomic.Int64

	collector := NewCalicoCollector(zap.NewNop(), "0", CalicoSyslogTLS{}, sink, func() { dropped.Add(1) })
	collector.firstByteTimeout = time.Second

	core, logs := observer.New(zap.WarnLevel)

	aggregated := `<14>Oct 26 21:10:09 calico-ent tigera_secure: {"start_time":1793048988,"source_ip":"10.244.163.79","source_port":null,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`
	noTimestamp := `<14>Oct 26 21:10:09 calico-ent tigera_secure: {"source_ip":"10.244.163.79","source_port":59106,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`

	serverConn, clientConn := net.Pipe()

	done := make(chan struct{})

	go func() {
		collector.handleConn(context.Background(), zap.New(core), serverConn)
		close(done)
	}()

	_, err := clientConn.Write([]byte(aggregated + "\n" + aggregated + "\n" + noTimestamp + "\n" + aggregated + "\n" + testLogCollectorRecord + "\n"))
	require.NoError(t, err)

	waitForFlow(t, sink)
	require.NoError(t, clientConn.Close())
	<-done

	assert.Equal(t, 1, sink.count())
	assert.Equal(t, int64(4), dropped.Load(), "every rejected record is counted")

	warnings := logs.FilterMessageSnippet("aggregated").All()
	require.Len(t, warnings, 1, "one warning per connection")
	assert.Contains(t, warnings[0].Message, "FelixConfiguration")
}
