// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"
)

// Errors for Calico syslog connection setup.
var (
	ErrCalicoTLSNotConfigured = errors.New("syslog client started a TLS handshake but no TLS certificate is configured")
	ErrCalicoPlainNotAllowed  = errors.New("syslog client sent plain text but TLS is required")
)

const (
	// tlsRecordTypeHandshake is the first byte of every TLS connection: the
	// record type of the ClientHello. Syslog records start with '<' instead.
	tlsRecordTypeHandshake = 0x16

	// calicoFirstByteTimeout bounds how long a new connection may stay silent
	// before the collector gives up on it.
	calicoFirstByteTimeout = 10 * time.Second

	// calicoHandshakeTimeout bounds the TLS handshake.
	calicoHandshakeTimeout = 10 * time.Second
)

// CalicoSyslogTLS configures TLS for the Calico Enterprise syslog collector.
// With a certificate configured, each connection may use TLS or plain text
// unless Required is set; without one, only plain text is accepted.
type CalicoSyslogTLS struct {
	CertFile string
	KeyFile  string
	Required bool
}

// loadCalicoSyslogTLSConfig loads the server certificate for the syslog
// collector. It returns a nil config when no certificate is configured.
func loadCalicoSyslogTLSConfig(settings CalicoSyslogTLS) (*tls.Config, error) {
	if settings.CertFile == "" && settings.KeyFile == "" {
		if settings.Required {
			return nil, errors.New("syslog TLS is required but no TLS certificate is configured")
		}

		return nil, nil //nolint:nilnil // no certificate means plain text only
	}

	if settings.CertFile == "" || settings.KeyFile == "" {
		return nil, errors.New("both the syslog TLS certificate file and key file must be set")
	}

	certificate, err := tls.LoadX509KeyPair(settings.CertFile, settings.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load syslog TLS certificate: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	}, nil
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

// prepareConn peeks at the first byte of a new connection and returns a
// connection that yields plain syslog records: the TLS connection after a
// successful handshake if the client started one, or the plain connection.
func (c *CalicoCollector) prepareConn(ctx context.Context, conn net.Conn) (net.Conn, error) {
	if err := conn.SetReadDeadline(time.Now().Add(c.firstByteTimeout)); err != nil {
		return nil, fmt.Errorf("failed to set read deadline: %w", err)
	}

	reader := bufio.NewReader(conn)

	firstByte, err := reader.Peek(1)
	if err != nil {
		return nil, fmt.Errorf("failed to read from syslog connection: %w", err)
	}

	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return nil, fmt.Errorf("failed to clear read deadline: %w", err)
	}

	buffered := &bufferedConn{Conn: conn, reader: reader}

	if firstByte[0] != tlsRecordTypeHandshake {
		if c.tlsSettings.Required {
			return nil, ErrCalicoPlainNotAllowed
		}

		return buffered, nil
	}

	if c.tlsConfig == nil {
		return nil, ErrCalicoTLSNotConfigured
	}

	tlsConn := tls.Server(buffered, c.tlsConfig)

	handshakeCtx, cancel := context.WithTimeout(ctx, c.handshakeTimeout)
	defer cancel()

	if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
		return nil, fmt.Errorf("syslog TLS handshake failed: %w", err)
	}

	return tlsConn, nil
}
