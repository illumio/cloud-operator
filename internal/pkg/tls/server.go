// Copyright 2026 Illumio, Inc. All Rights Reserved.

package tls

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Errors for accepting server connections that may use TLS or plain text.
var (
	ErrServerTLSNotConfigured = errors.New("client started a TLS handshake but no TLS certificate is configured")
	ErrPlainTextNotAllowed    = errors.New("client sent plain text but TLS is required")
)

// tlsRecordTypeHandshake is the first byte of every TLS connection: the record
// type of the ClientHello. Plain-text protocols such as syslog start differently.
const tlsRecordTypeHandshake = 0x16

// ServerSettings configures TLS for a server that may accept both TLS and
// plain-text connections on the same port. With a certificate configured, each
// connection may use TLS or plain text unless Required is set; without one,
// only plain text is accepted.
type ServerSettings struct {
	CertFile string
	KeyFile  string
	Required bool
}

// LoadServerConfig loads the server certificate. It returns a nil config when
// no certificate files are configured.
//
// The returned config re-reads the certificate files on each TLS handshake, so
// a created or renewed certificate, such as a Secret mounted as an optional
// volume, is used for new connections without a restart. Until the files
// exist, TLS handshakes fail and plain-text connections are still accepted,
// unless TLS is required. The certificate is loaded once here, so that an
// invalid certificate is reported at startup.
func LoadServerConfig(logger *zap.Logger, settings ServerSettings) (*tls.Config, error) {
	if settings.CertFile == "" && settings.KeyFile == "" {
		if settings.Required {
			return nil, errors.New("TLS is required but no TLS certificate is configured")
		}

		return nil, nil //nolint:nilnil // no certificate means plain text only
	}

	if settings.CertFile == "" || settings.KeyFile == "" {
		return nil, errors.New("both the TLS certificate file and key file must be set")
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	reloader := &certificateReloader{logger: logger, certFile: settings.CertFile, keyFile: settings.KeyFile}

	_, err := reloader.load()

	switch {
	case err == nil:
		logger.Info("Loaded TLS certificate",
			zap.String("cert_file", settings.CertFile), zap.Bool("tls_required", settings.Required))
	case errors.Is(err, fs.ErrNotExist):
		// The certificate may be created later, e.g. an optional Secret volume.
		logger.Info("TLS certificate not found; TLS connections are accepted once it is created",
			zap.String("cert_file", settings.CertFile), zap.Bool("tls_required", settings.Required))
	default:
		return nil, err
	}

	return &tls.Config{
		GetCertificate: reloader.getCertificate,
		MinVersion:     tls.VersionTLS12,
	}, nil
}

// certificateReloader serves the certificate in certFile and keyFile, re-reading
// both files on each TLS handshake. Handshakes are rare for long-lived
// connections, so reading the files each time is cheaper and simpler than
// watching them.
type certificateReloader struct {
	logger   *zap.Logger
	certFile string
	keyFile  string

	mu   sync.Mutex
	last *tls.Certificate // last certificate that loaded successfully
}

// load reads the certificate and key files and remembers the result as the
// last good certificate.
func (r *certificateReloader) load() (*tls.Certificate, error) {
	certificate, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS certificate: %w", err)
	}

	r.mu.Lock()
	r.last = &certificate
	r.mu.Unlock()

	return &certificate, nil
}

// getCertificate implements tls.Config.GetCertificate. If the files cannot be
// loaded, it keeps serving the last good certificate. Kubernetes swaps all the
// files of an updated Secret volume at once, but the two files are opened one
// after the other, so a read that straddles the swap can see a mismatched pair;
// the next handshake then reads a consistent pair again.
func (r *certificateReloader) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	certificate, err := r.load()
	if err == nil {
		return certificate, nil
	}

	r.mu.Lock()
	last := r.last
	r.mu.Unlock()

	if last == nil {
		return nil, err
	}

	r.logger.Warn("Failed to reload the TLS certificate; serving the previously loaded certificate",
		zap.String("cert_file", r.certFile), zap.Error(err))

	return last, nil
}

// ServerConnOptions configures PrepareServerConn.
type ServerConnOptions struct {
	// Config is the server TLS configuration, or nil to accept plain text only.
	Config *tls.Config
	// Required rejects plain-text connections.
	Required bool
	// FirstByteTimeout bounds how long a new connection may stay silent.
	FirstByteTimeout time.Duration
	// HandshakeTimeout bounds the TLS handshake.
	HandshakeTimeout time.Duration
}

// bufferedConn is a net.Conn whose reads go through a bufio.Reader, so bytes
// peeked from the connection are not lost.
type bufferedConn struct {
	net.Conn

	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

// PrepareServerConn peeks at the first byte of a newly accepted connection and
// returns a connection that yields the plain-text stream: the TLS connection
// after a successful handshake if the client started one, or the plain
// connection otherwise.
func PrepareServerConn(ctx context.Context, conn net.Conn, opts ServerConnOptions) (net.Conn, error) {
	if err := conn.SetReadDeadline(time.Now().Add(opts.FirstByteTimeout)); err != nil {
		return nil, fmt.Errorf("failed to set read deadline: %w", err)
	}

	reader := bufio.NewReader(conn)

	firstByte, err := reader.Peek(1)
	if err != nil {
		return nil, fmt.Errorf("failed to read from connection: %w", err)
	}

	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return nil, fmt.Errorf("failed to clear read deadline: %w", err)
	}

	buffered := &bufferedConn{Conn: conn, reader: reader}

	if firstByte[0] != tlsRecordTypeHandshake {
		if opts.Required {
			return nil, ErrPlainTextNotAllowed
		}

		return buffered, nil
	}

	if opts.Config == nil {
		return nil, ErrServerTLSNotConfigured
	}

	tlsConn := tls.Server(buffered, opts.Config)

	handshakeCtx, cancel := context.WithTimeout(ctx, opts.HandshakeTimeout)
	defer cancel()

	if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
		return nil, fmt.Errorf("TLS handshake failed: %w", err)
	}

	return tlsConn, nil
}
