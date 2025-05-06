// Copyright 2024 Illumio, Inc. All Rights Reserved.
// (Based on previous refactoring for graceful shutdown)
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log" // Using standard log for proxy, can be replaced with zap
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ProxyServer represents a proxy server with HTTP and gRPC forwarding capabilities.
type ProxyServer struct {
	httpServer          *http.Server
	grpcListener        net.Listener
	mainWg              sync.WaitGroup     // For main server loops (http listener, grpc accept loop)
	activeConnectionsWg sync.WaitGroup     // To track active proxied connections (CONNECT tunnels, gRPC forwards)
	mainCtx             context.Context    // Server-wide context, cancelled on Stop()
	mainCancel          context.CancelFunc // Function to cancel mainCtx
	logger              *log.Logger        // ADDED: Using standard logger for simplicity, can be zap
}

const (
	// Hardcoded target for non-CONNECT HTTP requests reverse-proxied by this server.
	proxyDefaultReverseProxyTargetScheme   = "https"
	proxyDefaultReverseProxyTargetHostPort = "host.docker.internal:50053" // Example, should match FakeServer HTTP
	// Hardcoded target for the dedicated gRPC forwarding port
	proxyDefaultDedicatedGRPCBackendAddress = "127.0.0.1:50051" // Should match FakeServer gRPC
)

// NewProxyServer creates and initializes a new ProxyServer.
func NewProxyServer(httpAddress, grpcAddress string, logger *log.Logger) (*ProxyServer, error) {
	mainCtx, mainCancel := context.WithCancel(context.Background())

	if logger == nil { // Default logger if nil
		// Default to discard if no logger, or use a default stdout logger:
		// logger = log.New(os.Stdout, "[ProxyServerDefault] ", log.LstdFlags|log.Lmicroseconds)
		logger = log.New(io.Discard, "[ProxyServerDefault] ", log.LstdFlags)
	}

	httpServer := &http.Server{
		Addr:        httpAddress,
		BaseContext: func(_ net.Listener) context.Context { return mainCtx }, // Propagate mainCtx
	}

	var pGrpcListener net.Listener
	var err error
	if grpcAddress != "" {
		pGrpcListener, err = net.Listen("tcp", grpcAddress)
		if err != nil {
			logger.Printf("ERROR: Failed to start Proxy gRPC listener on %s: %v", grpcAddress, err)
			mainCancel() // Cancel context if setup fails
			return nil, fmt.Errorf("proxy gRPC listener on %s: %w", grpcAddress, err)
		}
	}

	proxy := &ProxyServer{
		httpServer:   httpServer,
		grpcListener: pGrpcListener,
		mainCtx:      mainCtx,
		mainCancel:   mainCancel,
		logger:       logger,
	}
	httpServer.Handler = proxy // The proxy itself handles HTTP requests

	return proxy, nil
}

// Start launches the ProxyServer's HTTP and gRPC listeners. Non-blocking.
func (p *ProxyServer) Start() error {
	p.logger.Println("Starting ProxyServer...")
	p.mainWg.Add(1) // For HTTP server listener loop

	go func() {
		defer p.mainWg.Done()
		p.logger.Printf("Proxy HTTP server starting on %s", p.httpServer.Addr)
		if err := p.httpServer.ListenAndServe(); err != http.ErrServerClosed {
			p.logger.Printf("ERROR: Proxy HTTP server error: %v", err)
			p.mainCancel() // Signal other parts to stop
		}
		p.logger.Println("Proxy HTTP server stopped.")
	}()

	if p.grpcListener != nil {
		p.mainWg.Add(1) // For gRPC listener accept loop
		go p.acceptGRPCConnections()
		p.logger.Printf("ProxyServer ready: HTTP Proxy on %s, Dedicated gRPC Forwarder on %s", p.httpServer.Addr, p.grpcListener.Addr().String())
	} else {
		p.logger.Printf("ProxyServer ready: HTTP Proxy on %s (Dedicated gRPC Forwarder not configured)", p.httpServer.Addr)
	}
	return nil
}

func (p *ProxyServer) acceptGRPCConnections() {
	defer p.mainWg.Done()
	p.logger.Printf("Proxy dedicated gRPC forwarder starting on %s (forwards to %s)", p.grpcListener.Addr().String(), proxyDefaultDedicatedGRPCBackendAddress)
	for {
		select {
		case <-p.mainCtx.Done():
			p.logger.Printf("Proxy gRPC forwarder: main context cancelled, stopping accept loop on %s.", p.grpcListener.Addr().String())
			return
		default:
		}

		// Set a deadline for accept to make it non-blocking periodically
		if tcpListener, ok := p.grpcListener.(*net.TCPListener); ok {
			_ = tcpListener.SetDeadline(time.Now().Add(1 * time.Second))
		}

		conn, err := p.grpcListener.Accept()
		if err != nil {
			if p.mainCtx.Err() != nil { // Check if server is stopping
				return
			}
			if opError, ok := err.(*net.OpError); ok && opError.Timeout() {
				continue // Normal timeout, check context again
			}
			if opError, ok := err.(*net.OpError); ok && strings.Contains(opError.Err.Error(), "use of closed network connection") {
				p.logger.Printf("Proxy gRPC forwarder listener closed on %s, stopping accept loop.", p.grpcListener.Addr().String())
				return
			}
			p.logger.Printf("ERROR: Proxy gRPC forwarder accept error on %s: %v. Stopping.", p.grpcListener.Addr().String(), err)
			p.mainCancel() // Signal other parts to stop on unrecoverable error
			return
		}

		if tcpConn, ok := conn.(*net.TCPConn); ok { // Clear deadline for the accepted connection
			_ = tcpConn.SetDeadline(time.Time{})
		}

		p.logger.Printf("Proxy gRPC forwarder: Accepted connection from %s on %s", conn.RemoteAddr(), p.grpcListener.Addr().String())
		p.activeConnectionsWg.Add(1)
		go p.handleProxiedGRPC(p.mainCtx, conn) // Pass mainCtx
	}
}

// handleProxiedGRPC forwards a TCP connection (for gRPC) using the cancellable bidirectional copy.
func (p *ProxyServer) handleProxiedGRPC(ctx context.Context, clientConn net.Conn) {
	defer p.activeConnectionsWg.Done()

	p.logger.Printf("Proxy handleGRPC: Handling connection from %s, to backend %s", clientConn.RemoteAddr(), proxyDefaultDedicatedGRPCBackendAddress)

	connCtx, connCancel := context.WithCancel(ctx) // Context for this specific proxied connection, derived from main server ctx
	defer connCancel()

	defer func() {
		p.logger.Printf("Proxy handleGRPC: Ensuring client connection %s is closed.", clientConn.RemoteAddr())
		clientConn.Close()
	}()

	dialer := net.Dialer{Timeout: 10 * time.Second}
	backendConn, err := dialer.DialContext(connCtx, "tcp", proxyDefaultDedicatedGRPCBackendAddress)
	if err != nil {
		p.logger.Printf("ERROR: Proxy handleGRPC: Failed to connect to backend %s for client %s: %v", proxyDefaultDedicatedGRPCBackendAddress, clientConn.RemoteAddr(), err)
		return
	}
	defer func() {
		p.logger.Printf("Proxy handleGRPC: Ensuring backend connection %s (for client %s) is closed.", backendConn.RemoteAddr(), clientConn.RemoteAddr())
		backendConn.Close()
	}()

	p.logger.Printf("Proxy handleGRPC: Successfully connected to backend %s for client %s. Tunneling...", proxyDefaultDedicatedGRPCBackendAddress, clientConn.RemoteAddr())

	clientDesc := fmt.Sprintf("ClientGRPC(%s)", clientConn.RemoteAddr())
	backendDesc := fmt.Sprintf("BackendGRPC(%s)", backendConn.RemoteAddr())
	performBidirectionalCopy(connCtx, clientConn, backendConn, clientDesc, backendDesc, p.logger)
	p.logger.Printf("Proxy handleGRPC: Tunnel finished for client %s to backend %s", clientConn.RemoteAddr(), proxyDefaultDedicatedGRPCBackendAddress)
}

// Stop gracefully shuts down the ProxyServer.
func (p *ProxyServer) Stop(ctx context.Context) error {
	p.logger.Println("Stopping ProxyServer...")
	var firstErr error

	p.mainCancel() // Signal all internal operations to stop

	if p.grpcListener != nil {
		p.logger.Println("Proxy closing gRPC listener...")
		if err := p.grpcListener.Close(); err != nil {
			p.logger.Printf("ERROR: Proxy gRPC listener close error: %v", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	p.logger.Println("Proxy shutting down HTTP server...")
	httpShutdownCtx, httpShutdownCancel := context.WithTimeout(ctx, 15*time.Second)
	defer httpShutdownCancel()
	if err := p.httpServer.Shutdown(httpShutdownCtx); err != nil {
		p.logger.Printf("ERROR: Proxy HTTP server shutdown error: %v", err)
		if firstErr == nil {
			firstErr = err
		}
	}

	p.logger.Println("Proxy waiting for main server loops to stop...")
	if !waitWithTimeout(&p.mainWg, 5*time.Second) {
		p.logger.Println("WARN: Proxy timed out waiting for main server loops.")
		if firstErr == nil {
			firstErr = errors.New("proxy timed out waiting for main loops")
		}
	}

	p.logger.Println("Proxy waiting for active connections to terminate...")
	if !waitWithTimeout(&p.activeConnectionsWg, 10*time.Second) {
		p.logger.Println("WARN: Proxy timed out waiting for all active connections to terminate.")
		if firstErr == nil {
			firstErr = errors.New("proxy timed out waiting for active connections")
		}
	}

	p.logger.Println("ProxyServer stopped.")
	return firstErr
}

// ServeHTTP handles all incoming HTTP requests to the proxy.
func (p *ProxyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// reqCtx is derived from p.mainCtx (due to httpServer.BaseContext)
	// and is cancelled when this ServeHTTP handler returns.
	reqCtx := r.Context()
	p.logger.Printf("Proxy ServeHTTP: Method=%s, URL=%s, Host=%s, RemoteAddr=%s", r.Method, r.URL.String(), r.Host, r.RemoteAddr)

	if r.Method == http.MethodConnect {
		p.logger.Printf("Proxy ServeHTTP: Handling HTTPS CONNECT request for target %s", r.Host)
		// Pass reqCtx here, as handleConnectRequest is synchronous up to hijack
		p.handleConnectRequest(reqCtx, w, r)
		return
	}

	p.logger.Printf("Proxy ServeHTTP: Handling plain HTTP request (to be reverse proxied): %s %s", r.Method, r.URL.Path)
	targetURL := &url.URL{
		Scheme: proxyDefaultReverseProxyTargetScheme,
		Host:   proxyDefaultReverseProxyTargetHostPort,
	}
	reverseProxy := httputil.NewSingleHostReverseProxy(targetURL)
	reverseProxy.Transport = &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true,
	}
	// For reverse proxy, it's correct to use the request's context.
	// The reverse proxy operation should be tied to the lifecycle of this specific request.
	reverseProxy.ServeHTTP(w, r.WithContext(reqCtx))
	p.logger.Printf("Proxy ServeHTTP: Finished plain HTTP request for %s %s", r.Method, r.URL.Path)
}

// handleConnectRequest handles the synchronous part of an HTTP CONNECT request:
// dialing, sending 200 OK, and hijacking. Then spawns a goroutine for the tunnel.
func (p *ProxyServer) handleConnectRequest(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	originalTargetHost := r.Host
	effectiveTargetHost := r.Host
	var targetPort string

	if hostPort := strings.Split(effectiveTargetHost, ":"); len(hostPort) == 2 {
		targetPort = hostPort[1]
	} else {
		p.logger.Printf("WARN: Proxy handleConnect: Target %s without explicit port, assuming 443", effectiveTargetHost)
		targetPort = "443"
	}

	if strings.HasPrefix(effectiveTargetHost, "host.docker.internal:") ||
		(strings.HasPrefix(effectiveTargetHost, "192.168.65.") && (strings.HasSuffix(effectiveTargetHost, ":50051") || strings.HasSuffix(effectiveTargetHost, ":50053") || strings.HasSuffix(effectiveTargetHost, proxyDefaultReverseProxyTargetHostPort[strings.LastIndex(proxyDefaultReverseProxyTargetHostPort, ":"):]))) {
		newDialTarget := fmt.Sprintf("127.0.0.1:%s", targetPort)
		p.logger.Printf("Proxy handleConnect: Remapping target from %s to %s for local dialing", originalTargetHost, newDialTarget)
		effectiveTargetHost = newDialTarget
	}
	p.logger.Printf("Proxy handleConnect: Effective Target for Dial %s", effectiveTargetHost)

	// Use the passed context `ctx` (which is r.Context()) for the dial operation.
	// If this dial takes too long and r.Context() is cancelled (e.g. client disconnects),
	// the dial will be cancelled. This is correct.
	dialer := net.Dialer{}
	destConn, err := dialer.DialContext(ctx, "tcp", effectiveTargetHost)
	if err != nil {
		p.logger.Printf("ERROR: Proxy handleConnect: Dial error to %s (dialed %s): %v", originalTargetHost, effectiveTargetHost, err)
		http.Error(w, fmt.Sprintf("Failed to connect to destination %s: %v", originalTargetHost, err), http.StatusServiceUnavailable)
		return
	}
	// destConn is passed to handleConnectTunnel, which will manage its closure.

	w.WriteHeader(http.StatusOK) // Send 200 OK for CONNECT

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		p.logger.Printf("ERROR: Proxy handleConnect: Hijacking not supported for target %s", originalTargetHost)
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		destConn.Close()
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		p.logger.Printf("ERROR: Proxy handleConnect: Hijack error for target %s: %v", originalTargetHost, err)
		destConn.Close()
		return
	}
	// clientConn is passed to handleConnectTunnel, which will manage its closure.

	p.logger.Printf("Proxy handleConnect: Hijacked. Tunneling data between client %s and target %s (dialed %s).", clientConn.RemoteAddr(), originalTargetHost, effectiveTargetHost)
	p.activeConnectionsWg.Add(1)
	// **THE FIX IS HERE:** Pass `p.mainCtx` to the tunnel goroutine.
	// The tunnel's lifecycle should be tied to the ProxyServer's lifecycle,
	// not the lifecycle of this specific CONNECT request handler.
	go p.handleConnectTunnel(p.mainCtx, clientConn, destConn, originalTargetHost, effectiveTargetHost)
}

// handleConnectTunnel manages the bidirectional data copy for a CONNECT tunnel.
func (p *ProxyServer) handleConnectTunnel(ctx context.Context, clientConn net.Conn, destConn net.Conn, originalTargetHost, effectiveTargetHost string) {
	defer p.activeConnectionsWg.Done()
	defer clientConn.Close()
	defer destConn.Close()

	// Create a new context for this specific tunnel, derived from the passed `ctx` (which should now be p.mainCtx).
	// This allows this specific tunnel to be cancelled if its parent (p.mainCtx) is cancelled.
	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()

	clientDesc := fmt.Sprintf("ClientCONNECT(%s)", clientConn.RemoteAddr())
	targetDesc := fmt.Sprintf("TargetCONNECT(%s on %s)", originalTargetHost, effectiveTargetHost)

	p.logger.Printf("Proxy handleConnectTunnel: Starting bidirectional copy for %s <-> %s", clientDesc, targetDesc)
	performBidirectionalCopy(connCtx, clientConn, destConn, clientDesc, targetDesc, p.logger)
	p.logger.Printf("Proxy handleConnectTunnel: Finished for %s <-> %s", clientDesc, targetDesc)
}

// performBidirectionalCopy handles copying data between two connections and respects context cancellation.
func performBidirectionalCopy(ctx context.Context, conn1, conn2 net.Conn, desc1, desc2 string, logger *log.Logger) {
	var copyWg sync.WaitGroup
	copyWg.Add(2)

	// Goroutine to copy from conn1 to conn2
	go func() {
		defer copyWg.Done()
		// defer conn2.Close() // Closing is handled by the caller (handleConnectTunnel/handleProxiedGRPC)
		bytesCopied, err := io.Copy(conn2, conn1)
		if err != nil && !isClosedConnError(err) && err != io.EOF {
			logger.Printf("ERROR: Proxy Copy: %s -> %s: %d bytes, err: %v", desc1, desc2, bytesCopied, err)
		} else {
			logger.Printf("Proxy Copy: Finished %s -> %s: %d bytes, err: %v", desc1, desc2, bytesCopied, err)
		}
		// Attempt to signal the other direction that this side is done, e.g., by closing the write half.
		// This can help unblock the other io.Copy if it's waiting for an EOF.
		if tcpConn, ok := conn2.(*net.TCPConn); ok {
			tcpConn.CloseWrite()
		} else if tcpConn, ok := conn1.(*net.TCPConn); ok {
			// If conn2 is not TCP, try closing read on conn1 if it's TCP
			tcpConn.CloseRead()
		}

	}()

	// Goroutine to copy from conn2 to conn1
	go func() {
		defer copyWg.Done()
		// defer conn1.Close() // Closing is handled by the caller
		bytesCopied, err := io.Copy(conn1, conn2)
		if err != nil && !isClosedConnError(err) && err != io.EOF {
			logger.Printf("ERROR: Proxy Copy: %s -> %s: %d bytes, err: %v", desc2, desc1, bytesCopied, err)
		} else {
			logger.Printf("Proxy Copy: Finished %s -> %s: %d bytes, err: %v", desc2, desc1, bytesCopied, err)
		}
		if tcpConn, ok := conn1.(*net.TCPConn); ok {
			tcpConn.CloseWrite()
		} else if tcpConn, ok := conn2.(*net.TCPConn); ok {
			tcpConn.CloseRead()
		}
	}()

	select {
	case <-ctx.Done():
		logger.Printf("Proxy Bidirectional copy for %s <-> %s cancelled by context. Initiating connection closures.", desc1, desc2)
		// Connections will be closed by the defer statements in handleConnectTunnel or handleProxiedGRPC
		// and also by the final conn1.Close()/conn2.Close() below.
		// Forcing closure here can help unblock io.Copy immediately if ctx.Done() is received.
		conn1.Close()
		conn2.Close()
	case <-waiter(&copyWg):
		logger.Printf("Proxy Bidirectional copy for %s <-> %s completed naturally.", desc1, desc2)
	}

	// Ensure connections are closed and wait for copy goroutines to exit.
	conn1.Close()
	conn2.Close()
	copyWg.Wait()
	logger.Printf("Proxy Bidirectional copy fully terminated for %s <-> %s.", desc1, desc2)
}

func isClosedConnError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "use of closed network connection") ||
		strings.Contains(errStr, "connection reset by peer") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "forcibly closed") || // Windows
		err == io.EOF // EOF is also a common way for io.Copy to end when a connection is closed.
}

func waiter(wg *sync.WaitGroup) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		wg.Wait()
		close(ch)
	}()
	return ch
}

func waitWithTimeout(wg *sync.WaitGroup, timeout time.Duration) bool {
	c := make(chan struct{})
	go func() {
		defer close(c)
		wg.Wait()
	}()
	select {
	case <-c:
		return true
	case <-time.After(timeout):
		return false
	}
}
