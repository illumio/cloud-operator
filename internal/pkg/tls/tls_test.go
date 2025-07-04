/*
 *
 * Copyright 2025 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

// Modifications copyright (C) by Illumio 2025
// Modified to not use grpc internal package and remove server/client handshake

package tls

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/testdata"
)

const defaultTestTimeout = 10 * time.Second

func TestTLSOverrideServerName(t *testing.T) {
	logger := zaptest.NewLogger(t)
	authority := "server.name"
	c := NewTLSWithALPNDisabled(&tls.Config{ServerName: authority}, logger)
	if c.Info().ServerName != authority {
		t.Fatalf("c.Info().ServerName = %v, want %v", c.Info().ServerName, authority)
	}
}

func TestTLSClone(t *testing.T) {
	logger := zaptest.NewLogger(t)
	authority := "server.name"
	c := NewTLSWithALPNDisabled(&tls.Config{ServerName: authority}, logger)
	if c.Info().ServerName != authority {
		t.Fatalf("Change in clone should not affect the original, c.Info().ServerName = %v, want %v", c.Info().ServerName, authority)
	}
}

type serverHandshake func(net.Conn) (credentials.AuthInfo, error)

func TestClientHandshakeReturnsAuthInfo(t *testing.T) {
	tcs := []struct {
		name    string
		address string
	}{
		{
			name:    "localhost",
			address: "localhost:0",
		},
		{
			name:    "ipv4",
			address: "127.0.0.1:0",
		},
		{
			name:    "ipv6",
			address: "[::1]:0",
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan credentials.AuthInfo, 1)
			lis := launchServerOnListenAddress(t, tlsServerHandshake, done, tc.address)
			defer lis.Close()
			lisAddr := lis.Addr().String()
			clientAuthInfo := clientHandle(t, gRPCClientHandshake, lisAddr)
			// wait until server sends serverAuthInfo or fails.
			serverAuthInfo, ok := <-done
			if !ok {
				t.Fatalf("Error at server-side")
			}
			if !compare(clientAuthInfo, serverAuthInfo) {
				t.Fatalf("c.ClientHandshake(_, %v, _) = %v, want %v.", lisAddr, clientAuthInfo, serverAuthInfo)
			}
		})
	}
}

func compare(a1, a2 credentials.AuthInfo) bool {
	if a1.AuthType() != a2.AuthType() {
		return false
	}
	switch a1.AuthType() {
	case "tls":
		state1 := a1.(credentials.TLSInfo).State
		state2 := a2.(credentials.TLSInfo).State
		if state1.Version == state2.Version &&
			state1.HandshakeComplete == state2.HandshakeComplete &&
			state1.CipherSuite == state2.CipherSuite &&
			state1.NegotiatedProtocol == state2.NegotiatedProtocol {
			return true
		}
		return false
	default:
		return false
	}
}

func launchServerOnListenAddress(t *testing.T, hs serverHandshake, done chan credentials.AuthInfo, address string) net.Listener {
	lis, err := net.Listen("tcp", address)
	if err != nil {
		if strings.Contains(err.Error(), "bind: cannot assign requested address") ||
			strings.Contains(err.Error(), "socket: address family not supported by protocol") {
			t.Skipf("no support for address %v", address)
		}
		t.Fatalf("Failed to listen: %v", err)
	}
	go serverHandle(t, hs, done, lis)
	return lis
}

// Is run in a separate goroutine.
func serverHandle(t *testing.T, hs serverHandshake, done chan credentials.AuthInfo, lis net.Listener) {
	serverRawConn, err := lis.Accept()
	if err != nil {
		t.Errorf("Server failed to accept connection: %v", err)
		close(done)
		return
	}
	serverAuthInfo, err := hs(serverRawConn)
	if err != nil {
		t.Errorf("Server failed while handshake. Error: %v", err)
		serverRawConn.Close()
		close(done)
		return
	}
	done <- serverAuthInfo
}

func clientHandle(t *testing.T, hs func(net.Conn, string) (credentials.AuthInfo, error), lisAddr string) credentials.AuthInfo {
	conn, err := net.Dial("tcp", lisAddr)
	if err != nil {
		t.Fatalf("Client failed to connect to %s. Error: %v", lisAddr, err)
	}
	defer conn.Close()
	clientAuthInfo, err := hs(conn, lisAddr)
	if err != nil {
		t.Fatalf("Error on client while handshake. Error: %v", err)
	}
	return clientAuthInfo
}

// Client handshake implementation in gRPC.
func gRPCClientHandshake(conn net.Conn, lisAddr string) (credentials.AuthInfo, error) {
	logger := zaptest.NewLogger(nil)
	clientTLS := NewTLSWithALPNDisabled(&tls.Config{InsecureSkipVerify: true}, logger)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	_, authInfo, err := clientTLS.ClientHandshake(ctx, lisAddr, conn)
	if err != nil {
		return nil, err
	}
	return authInfo, nil
}

func tlsServerHandshake(conn net.Conn) (credentials.AuthInfo, error) {
	cert, err := tls.LoadX509KeyPair(testdata.Path("x509/server1_cert.pem"), testdata.Path("x509/server1_key.pem"))
	if err != nil {
		return nil, err
	}
	serverTLSConfig := &tls.Config{Certificates: []tls.Certificate{cert}}
	serverConn := tls.Server(conn, serverTLSConfig)
	err = serverConn.Handshake()
	if err != nil {
		return nil, err
	}
	return credentials.TLSInfo{State: serverConn.ConnectionState(), CommonAuthInfo: credentials.CommonAuthInfo{SecurityLevel: credentials.PrivacyAndIntegrity}}, nil
}

func TestHandleTLSHandshakeError(t *testing.T) {
	tests := []struct {
		name          string
		inputError    error
		expectedError error
	}{
		{
			name:          "ALPN handshake error",
			inputError:    errors.New("missing selected ALPN property"),
			expectedError: ErrTLSALPNHandshakeFailed,
		},
		{
			name:          "TLS handshake error",
			inputError:    errors.New("first record does not look like a TLS handshake"),
			expectedError: ErrNoTLSHandshakeFailed,
		},
		{
			name:          "Unrecognized error",
			inputError:    errors.New("some other error"),
			expectedError: errors.New("some other error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := AsTLSHandshakeError(tt.inputError)
			if result.Error() != tt.expectedError.Error() {
				t.Errorf("expected %v, got %v", tt.expectedError, result)
			}
		})
	}
}
