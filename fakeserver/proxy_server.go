// Copyright 2024 Illumio, Inc. All Rights Reserved.
// Simplified to focus on HTTP CONNECT tunneling, with Hijack buffering fix.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ProxyServer represents a proxy server focused on HTTP CONNECT.
type ProxyServer struct {
	httpServer *http.Server
	logger     *zap.Logger
	wg         sync.WaitGroup
}

// NewProxyServer creates and initializes a new ProxyServer.
func NewProxyServer(httpAddress string, logger *zap.Logger) *ProxyServer {
	httpServer := &http.Server{
		Addr:              httpAddress,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Second,
	}
	proxyServer := &ProxyServer{
		httpServer: httpServer,
		logger:     logger,
	}
	httpServer.Handler = proxyServer

	return proxyServer
}

// Start launches the ProxyServer's HTTP listener.
func (p *ProxyServer) Start() {
	p.logger.Info("Starting Simplified ProxyServer (with Hijack fix)...")
	p.wg.Add(1)

	go func() {
		defer p.wg.Done()

		p.logger.Info("Proxy HTTP server starting", zap.String("address", p.httpServer.Addr))

		if err := p.httpServer.ListenAndServe(); err != http.ErrServerClosed {
			p.logger.Error("Proxy HTTP server error", zap.Error(err))
		}

		p.logger.Info("Proxy HTTP server stopped")
	}()

	p.logger.Info("Simplified ProxyServer ready: HTTP Proxy for CONNECT on", zap.String("address", p.httpServer.Addr))
}

// Stop gracefully shuts down the ProxyServer.
func (p *ProxyServer) Stop() error {
	p.logger.Info("Stopping Simplified ProxyServer...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := p.httpServer.Shutdown(ctx); err != nil {
		p.logger.Error("Proxy HTTP server shutdown error", zap.Error(err))
	}

	p.wg.Wait()
	p.logger.Info("Simplified ProxyServer stopped.")

	return nil
}

// ServeHTTP is the entry point for all HTTP requests made to the proxy server.
func (p *ProxyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.logger.Info("Proxy ServeHTTP", zap.String("method", r.Method), zap.String("url", r.URL.String()), zap.String("host", r.Host), zap.String("remoteAddr", r.RemoteAddr), zap.String("proto", r.Proto))

	if r.Method == http.MethodConnect {
		p.logger.Info("Handling HTTPS CONNECT request", zap.String("target", r.Host))
		p.handleConnect(w, r)

		return
	}

	p.logger.Info("Proxy ServeHTTP: Received non-CONNECT method", zap.String("method", r.Method), zap.String("proxy", "This proxy only handles CONNECT."))
	http.Error(w, "This proxy only handles HTTP CONNECT requests", http.StatusMethodNotAllowed)
}

// handleConnect handles HTTP CONNECT requests.
func (p *ProxyServer) handleConnect(w http.ResponseWriter, r *http.Request) { //nolint:gocognit
	originalTargetHost := r.Host
	effectiveTargetHost := r.Host

	p.logger.Info("Proxy handleConnect: Initial Target", zap.String("target", originalTargetHost), zap.String("client", r.RemoteAddr))

	var targetPort string
	if hostPortParts := strings.Split(effectiveTargetHost, ":"); len(hostPortParts) == 2 {
		targetPort = hostPortParts[1]
	} else {
		p.logger.Error("Target is missing an explicit port", zap.String("target", originalTargetHost), zap.String("client", r.RemoteAddr))
		http.Error(w, fmt.Sprintf("Target %s is missing an explicit port", originalTargetHost), http.StatusBadRequest)

		return
	}

	if strings.HasPrefix(effectiveTargetHost, "host.docker.internal:") ||
		(strings.HasPrefix(effectiveTargetHost, "192.168.65.") && (strings.HasSuffix(effectiveTargetHost, ":50051") || strings.HasSuffix(effectiveTargetHost, ":50053"))) {
		newDialTarget := "127.0.0.1:" + targetPort
		p.logger.Info("Remapping target", zap.String("originalTarget", originalTargetHost), zap.String("newTarget", newDialTarget))
		effectiveTargetHost = newDialTarget
	}

	p.logger.Info("Proxy handleConnect: Effective Target for Dial", zap.String("target", effectiveTargetHost), zap.String("client", r.RemoteAddr))

	dialer := net.Dialer{
		Timeout: 15 * time.Second,
	}

	destConn, err := dialer.DialContext(r.Context(), "tcp", effectiveTargetHost)
	if err != nil {
		errMsgBody := fmt.Sprintf("Failed to connect to destination %s (dialed as %s): %v", originalTargetHost, effectiveTargetHost, err)
		http.Error(w, errMsgBody, http.StatusServiceUnavailable)
		p.logger.Error("Dial error", zap.String("target", originalTargetHost), zap.String("dialedAs", effectiveTargetHost), zap.String("client", r.RemoteAddr), zap.Error(err))

		return
	}

	defer func() {
		err := destConn.Close()
		p.logger.Error(
			"Proxy handleConnect: Failed to close connection to destination",
			zap.Error(err),
		)
	}()

	p.logger.Info("Proxy handleConnect: TCP connected to target", zap.String("target", originalTargetHost), zap.String("dialedAs", effectiveTargetHost), zap.String("client", r.RemoteAddr))
	w.WriteHeader(http.StatusOK)

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		p.logger.Error("Hijacking not supported", zap.String("target", originalTargetHost), zap.String("client", r.RemoteAddr))

		return
	}
	// Hijack the connection. clientUnderlyingConn is the raw TCP connection.
	// clientRW is a bufio.ReadWriter that the HTTP server was using; its Reader might contain
	// bytes already read from clientUnderlyingConn (e.g., the start of the TLS ClientHello).
	clientUnderlyingConn, clientRW, err := hijacker.Hijack()
	if err != nil {
		p.logger.Error("Proxy handleConnect: Hijack error", zap.String("target", originalTargetHost), zap.String("client", r.RemoteAddr), zap.Error(err))

		return
	}

	defer func() {
		err := clientUnderlyingConn.Close()
		p.logger.Error(
			"Proxy handleConnect: Failed to close underlying connection to destination",
			zap.Error(err),
		)
	}()

	p.logger.Info("Proxy handleConnect: Hijacked connection", zap.String("clientRemote", clientUnderlyingConn.RemoteAddr().String()), zap.String("clientLocal", clientUnderlyingConn.LocalAddr().String()), zap.String("destRemote", destConn.RemoteAddr().String()), zap.String("destLocal", destConn.LocalAddr().String()))
	p.logger.Info("Proxy handleConnect: Tunneling data", zap.String("client", r.RemoteAddr), zap.String("target", originalTargetHost))

	var tunnelWg sync.WaitGroup
	tunnelWg.Add(2)

	go func() {
		defer tunnelWg.Done()

		buf := make([]byte, 4096) // Or smaller buffer

		for {
			p.logger.Info("Proxy handleConnect: Client->Target attempting read...")

			nr, err := clientRW.Read(buf)
			if nr > 0 {
				p.logger.Info("Proxy handleConnect: Client->Target read", zap.Int("bytes", nr))
				nw, ew := destConn.Write(buf[0:nr])
				p.logger.Info("Proxy handleConnect: Client->Target wrote", zap.Int("bytes", nw))

				if ew != nil {
					p.logger.Error("Client->Target write error", zap.Error(ew))

					break
				}

				if nr != nw {
					p.logger.Error("Client->Target short write", zap.Error(io.ErrShortWrite))

					break
				}
			}

			if err != nil {
				if errors.Is(err, io.EOF) {
					p.logger.Info("Client->Target read EOF")
				} else {
					p.logger.Error("Client->Target read error", zap.Error(err))
				}

				break
			}
		}

		p.logger.Info("Proxy handleConnect: Client->Target loop finished")
		// ... CloseWrite logic ...
	}()
	// Similar loop for the other direction

	// Goroutine to copy data from destination to client.
	// IMPORTANT: Write to clientRW.Writer and remember to Flush.
	go func() {
		defer tunnelWg.Done()
		// Using clientRW.Writer which wraps clientUnderlyingConn.
		bytesCopied, copyErr := io.Copy(clientRW, destConn)
		if copyErr == nil { // Only flush if copy was successful or EOF
			if err := clientRW.Flush(); err != nil {
				p.logger.Error("Flushing to client writer error", zap.Error(err))
			}
		}

		p.logger.Info("Target->Client data copied", zap.String("target", originalTargetHost), zap.String("dialedAs", effectiveTargetHost), zap.Int64("bytes", bytesCopied), zap.Error(copyErr))

		if tcpConn, ok := clientUnderlyingConn.(*net.TCPConn); ok { // Though CloseWrite on client side is less common
			_ = tcpConn.CloseWrite()
		}
	}()

	tunnelWg.Wait()
	p.logger.Info("Proxy handleConnect: Tunnel finished", zap.String("target", originalTargetHost), zap.String("client", r.RemoteAddr))
}
