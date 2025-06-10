/*
 *
 * Copyright 2025 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

// Modifications copyright (C) Illumio 2025
// Modified to only keep the TLS handshake functions

package tls

import (
	"context"
	"crypto/tls"
	"errors"
	"net"

	"github.com/illumio/cloud-operator/internal/pkg/tls/spiffe"
	"github.com/illumio/cloud-operator/internal/pkg/tls/syscallconn"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"google.golang.org/grpc/credentials"
)

// tlsCreds is the credentials required for authenticating a connection using TLS.
type tlsCreds struct {
	// TLS configuration
	config *tls.Config
	logger *zap.Logger
}

type AuthProperties struct {
	DisableALPN bool
	DisableTLS  bool
}

var (
	ErrTLSALPNHandshakeFailed = errors.New("alpn handshake failed, retrying with ALPN disabled")
	ErrNoTLSHandshake         = errors.New("no TLS handshake")
)

func (c tlsCreds) Info() credentials.ProtocolInfo {
	return credentials.ProtocolInfo{
		SecurityProtocol: "tls",
		SecurityVersion:  "1.2",
		ServerName:       c.config.ServerName,
	}
}

func (c *tlsCreds) ClientHandshake(ctx context.Context, authority string, rawConn net.Conn) (_ net.Conn, _ credentials.AuthInfo, err error) {
	// use local cfg to avoid clobbering ServerName if using multiple endpoints
	cfg := cloneTLSConfig(c.config)
	if cfg.ServerName == "" {
		serverName, _, err := net.SplitHostPort(authority)
		if err != nil {
			// If the authority had no host port or if the authority cannot be parsed, use it as-is.
			serverName = authority
		}
		cfg.ServerName = serverName
	}
	conn := tls.Client(rawConn, cfg)
	errChannel := make(chan error, 1)
	go func() {
		errChannel <- conn.Handshake()
		close(errChannel)
	}()
	select {
	case err := <-errChannel:
		if err != nil {
			conn.Close()
			return nil, nil, err
		}
	case <-ctx.Done():
		conn.Close()
		return nil, nil, ctx.Err()
	}

	tlsInfo := credentials.TLSInfo{
		State: conn.ConnectionState(),
		CommonAuthInfo: credentials.CommonAuthInfo{
			SecurityLevel: credentials.PrivacyAndIntegrity,
		},
	}
	id := spiffe.SPIFFEIDFromState(conn.ConnectionState(), c.logger)
	if id != nil {
		tlsInfo.SPIFFEID = id
	}
	return syscallconn.WrapSyscallConn(rawConn, conn), tlsInfo, nil
}

func (c *tlsCreds) ServerHandshake(rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	conn := tls.Server(rawConn, c.config)
	if err := conn.Handshake(); err != nil {
		conn.Close()
		return nil, nil, err
	}
	cs := conn.ConnectionState()
	tlsInfo := credentials.TLSInfo{
		State: cs,
		CommonAuthInfo: credentials.CommonAuthInfo{
			SecurityLevel: credentials.PrivacyAndIntegrity,
		},
	}
	id := spiffe.SPIFFEIDFromState(conn.ConnectionState(), c.logger)
	if id != nil {
		tlsInfo.SPIFFEID = id
	}
	return syscallconn.WrapSyscallConn(rawConn, conn), tlsInfo, nil
}

func (c *tlsCreds) Clone() credentials.TransportCredentials {
	return NewTLSWithALPNDisabled(c.config, c.logger)
}

func (c *tlsCreds) OverrideServerName(serverNameOverride string) error {
	c.config.ServerName = serverNameOverride
	return nil
}

// The following cipher suites are forbidden for use with HTTP/2 by
// https://datatracker.ietf.org/doc/html/rfc7540#appendix-A
var tls12ForbiddenCipherSuites = map[uint16]struct{}{
	tls.TLS_RSA_WITH_AES_128_CBC_SHA:         {},
	tls.TLS_RSA_WITH_AES_256_CBC_SHA:         {},
	tls.TLS_RSA_WITH_AES_128_GCM_SHA256:      {},
	tls.TLS_RSA_WITH_AES_256_GCM_SHA384:      {},
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA: {},
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA: {},
	tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA:   {},
	tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA:   {},
}

// cloneTLSConfig returns a shallow clone of the exported
// fields of cfg, ignoring the unexported sync.Once, which
// contains a mutex and must not be copied.
//
// If cfg is nil, a new zero tls.Config is returned.
func cloneTLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return &tls.Config{}
	}

	return cfg.Clone()
}

// appendH2ToNextProtos appends h2 to next protos.
func appendH2ToNextProtos(ps []string) []string {
	for _, p := range ps {
		if p == http2.NextProtoTLS {
			return ps
		}
	}
	ret := make([]string, 0, len(ps)+1)
	ret = append(ret, ps...)
	return append(ret, http2.NextProtoTLS)
}

// NewTLSWithALPNDisabled uses c to construct a TransportCredentials based on
// TLS. ALPN verification is disabled.
func NewTLSWithALPNDisabled(c *tls.Config, logger *zap.Logger) credentials.TransportCredentials {
	config := applyDefaults(c)
	if config.GetConfigForClient != nil {
		oldFn := config.GetConfigForClient
		config.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			cfgForClient, err := oldFn(hello)
			if err != nil || cfgForClient == nil {
				return cfgForClient, err
			}
			return applyDefaults(cfgForClient), nil
		}
	}
	return &tlsCreds{config: config, logger: logger}
}

func applyDefaults(c *tls.Config) *tls.Config {
	config := cloneTLSConfig(c)
	config.NextProtos = appendH2ToNextProtos(config.NextProtos)
	// If the user did not configure a MinVersion and did not configure a
	// MaxVersion < 1.2, use MinVersion=1.2, which is required by
	// https://datatracker.ietf.org/doc/html/rfc7540#section-9.2
	if config.MinVersion == 0 && (config.MaxVersion == 0 || config.MaxVersion >= tls.VersionTLS12) {
		config.MinVersion = tls.VersionTLS12
	}
	// If the user did not configure CipherSuites, use all "secure" cipher
	// suites reported by the TLS package, but remove some explicitly forbidden
	// by https://datatracker.ietf.org/doc/html/rfc7540#appendix-A
	if config.CipherSuites == nil {
		for _, cs := range tls.CipherSuites() {
			if _, ok := tls12ForbiddenCipherSuites[cs.ID]; !ok {
				config.CipherSuites = append(config.CipherSuites, cs.ID)
			}
		}
	}
	return config
}

// HandleTLSError processes specific TLS handshake errors and updates the tlsAuthProperties accordingly.
func HandleTLSError(err error, tlsAuthProperties *AuthProperties) error {
	switch {
	case errors.Is(err, ErrTLSALPNHandshakeFailed):
		tlsAuthProperties.DisableALPN = true
	case errors.Is(err, ErrNoTLSHandshake):
		tlsAuthProperties.DisableTLS = true
	default:
		// If the error is not recognized, return it as-is.
		return err
	}
	return err
}
