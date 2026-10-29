// Copyright 2026 Illumio, Inc. All Rights Reserved.

package tls

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"
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
// no certificate is configured.
func LoadServerConfig(settings ServerSettings) (*tls.Config, error) {
	if settings.CertFile == "" && settings.KeyFile == "" {
		if settings.Required {
			return nil, errors.New("TLS is required but no TLS certificate is configured")
		}

		return nil, nil //nolint:nilnil // no certificate means plain text only
	}

	if settings.CertFile == "" || settings.KeyFile == "" {
		return nil, errors.New("both the TLS certificate file and key file must be set")
	}

	certificate, err := tls.LoadX509KeyPair(settings.CertFile, settings.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS certificate: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	}, nil
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
