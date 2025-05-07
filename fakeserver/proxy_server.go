// Copyright 2024 Illumio, Inc. All Rights Reserved.
// Simplified to focus on HTTP CONNECT tunneling, with Hijack buffering fix.

package main

import (
	// Needed for the Hijack ReadWriter
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ProxyServer represents a proxy server focused on HTTP CONNECT.
type ProxyServer struct {
	httpServer *http.Server
	wg         sync.WaitGroup
}

// NewProxyServer creates and initializes a new ProxyServer.
func NewProxyServer(httpAddress string) (*ProxyServer, error) {
	httpServer := &http.Server{
		Addr: httpAddress,
	}
	proxyServer := &ProxyServer{
		httpServer: httpServer,
	}
	httpServer.Handler = proxyServer
	return proxyServer, nil
}

// Start launches the ProxyServer's HTTP listener.
func (p *ProxyServer) Start() error {
	log.Println("Starting Simplified ProxyServer (with Hijack fix)...")
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		log.Printf("Proxy HTTP server starting on %s (CONNECT Tunneling Only)", p.httpServer.Addr)
		if err := p.httpServer.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("ERROR: Proxy HTTP server error: %v", err)
		}
		log.Println("Proxy HTTP server stopped.")
	}()
	log.Printf("Simplified ProxyServer ready: HTTP Proxy for CONNECT on %s", p.httpServer.Addr)
	return nil
}

// Stop gracefully shuts down the ProxyServer.
func (p *ProxyServer) Stop() error {
	log.Println("Stopping Simplified ProxyServer...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.httpServer.Shutdown(ctx); err != nil {
		log.Printf("ERROR: Proxy HTTP server shutdown error: %v", err)
	}
	p.wg.Wait()
	log.Println("Simplified ProxyServer stopped.")
	return nil
}

// ServeHTTP is the entry point for all HTTP requests made to the proxy server.
func (p *ProxyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("Proxy ServeHTTP: Method=%s, URL=%s, Host=%s, RemoteAddr=%s, Proto=%s", r.Method, r.URL.String(), r.Host, r.RemoteAddr, r.Proto)
	if r.Method == http.MethodConnect {
		log.Printf("Proxy ServeHTTP: Handling HTTPS CONNECT request for target %s", r.Host)
		p.handleConnect(w, r)
		return
	}
	log.Printf("Proxy ServeHTTP: Received non-CONNECT method %s. This proxy only handles CONNECT.", r.Method)
	http.Error(w, "This proxy only handles HTTP CONNECT requests", http.StatusMethodNotAllowed)
}

// handleConnect handles HTTP CONNECT requests.
func (p *ProxyServer) handleConnect(w http.ResponseWriter, r *http.Request) {
	originalTargetHost := r.Host
	effectiveTargetHost := r.Host

	log.Printf("Proxy handleConnect: Initial Target %s (Client %s)", originalTargetHost, r.RemoteAddr)

	var targetPort string
	if hostPortParts := strings.Split(effectiveTargetHost, ":"); len(hostPortParts) == 2 {
		targetPort = hostPortParts[1]
	} else {
		log.Printf("ERROR: Proxy handleConnect: Target %s for client %s is missing an explicit port.", originalTargetHost, r.RemoteAddr)
		http.Error(w, fmt.Sprintf("Target %s is missing an explicit port", originalTargetHost), http.StatusBadRequest)
		return
	}

	if strings.HasPrefix(effectiveTargetHost, "host.docker.internal:") ||
		(strings.HasPrefix(effectiveTargetHost, "192.168.65.") && (strings.HasSuffix(effectiveTargetHost, ":50051") || strings.HasSuffix(effectiveTargetHost, ":50053"))) {
		newDialTarget := fmt.Sprintf("127.0.0.1:%s", targetPort)
		log.Printf("Proxy handleConnect: Remapping target from %s to %s for local dialing by proxy", originalTargetHost, newDialTarget)
		effectiveTargetHost = newDialTarget
	}

	log.Printf("Proxy handleConnect: Effective Target for Dial %s (Client %s)", effectiveTargetHost, r.RemoteAddr)
	destConn, err := net.DialTimeout("tcp", effectiveTargetHost, 15*time.Second)
	if err != nil {
		errMsgBody := fmt.Sprintf("Failed to connect to destination %s (dialed as %s): %v", originalTargetHost, effectiveTargetHost, err)
		http.Error(w, errMsgBody, http.StatusServiceUnavailable)
		log.Printf("ERROR: Proxy handleConnect: Dial error to %s (dialed as %s) for client %s: %v", originalTargetHost, effectiveTargetHost, r.RemoteAddr, err)
		return
	}
	defer destConn.Close()

	log.Printf("Proxy handleConnect: TCP connected to target %s (dialed as %s). Sending 200 OK to client %s.", originalTargetHost, effectiveTargetHost, r.RemoteAddr)
	w.WriteHeader(http.StatusOK)

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		log.Printf("ERROR: Proxy handleConnect: Hijacking not supported for target %s. Client %s.", originalTargetHost, r.RemoteAddr)
		return
	}
	// Hijack the connection. clientUnderlyingConn is the raw TCP connection.
	// clientRW is a bufio.ReadWriter that the HTTP server was using; its Reader might contain
	// bytes already read from clientUnderlyingConn (e.g., the start of the TLS ClientHello).
	clientUnderlyingConn, clientRW, err := hijacker.Hijack()
	if err != nil {
		log.Printf("ERROR: Proxy handleConnect: Hijack error for target %s, client %s: %v", originalTargetHost, r.RemoteAddr, err)
		return
	}
	defer clientUnderlyingConn.Close() // Ensure the underlying connection is closed.

	log.Printf("Proxy handleConnect: Hijacked. Client remote: %s, local: %s. Dest remote: %s, local: %s",
		clientUnderlyingConn.RemoteAddr(), clientUnderlyingConn.LocalAddr(), destConn.RemoteAddr(), destConn.LocalAddr())
	log.Printf("Proxy handleConnect: Tunneling data between client (via buffered RW) and target %s (dialed as %s).", originalTargetHost, effectiveTargetHost)

	var tunnelWg sync.WaitGroup
	tunnelWg.Add(2)

	// Example manual loop (replace io.Copy)
	go func() {
		defer tunnelWg.Done()
		buf := make([]byte, 4096) // Or smaller buffer
		for {
			log.Printf("Proxy handleConnect: Client->Target attempting read...")
			nr, er := clientRW.Reader.Read(buf)
			if nr > 0 {
				log.Printf("Proxy handleConnect: Client->Target read %d bytes", nr)
				nw, ew := destConn.Write(buf[0:nr])
				log.Printf("Proxy handleConnect: Client->Target wrote %d bytes", nw)
				if ew != nil {
					log.Printf("Proxy handleConnect: Client->Target write error: %v", ew)
					break
				}
				if nr != nw {
					log.Printf("Proxy handleConnect: Client->Target short write: %v", io.ErrShortWrite)
					break
				}
			}
			if er != nil {
				if er != io.EOF {
					log.Printf("Proxy handleConnect: Client->Target read error: %v", er)
				} else {
					log.Printf("Proxy handleConnect: Client->Target read EOF")
				}
				break
			}
		}
		log.Printf("Proxy handleConnect: Client->Target loop finished.")
		// ... CloseWrite logic ...
	}()
	// Similar loop for the other direction

	// Goroutine to copy data from destination to client.
	// IMPORTANT: Write to clientRW.Writer and remember to Flush.
	go func() {
		defer tunnelWg.Done()
		// Using clientRW.Writer which wraps clientUnderlyingConn.
		bytesCopied, copyErr := io.Copy(clientRW.Writer, destConn)
		if copyErr == nil { // Only flush if copy was successful or EOF
			if err := clientRW.Writer.Flush(); err != nil {
				log.Printf("ERROR: Proxy handleConnect: Flushing to client writer error: %v", err)
			}
		}
		log.Printf("Proxy handleConnect: Target (%s/%s) -> Client (buffered): %d bytes, err: %v", originalTargetHost, effectiveTargetHost, bytesCopied, copyErr)
		if tcpConn, ok := clientUnderlyingConn.(*net.TCPConn); ok { // Though CloseWrite on client side is less common
			_ = tcpConn.CloseWrite()
		}
	}()

	tunnelWg.Wait()
	log.Printf("Proxy handleConnect: Tunnel finished for %s (Client %s)", originalTargetHost, r.RemoteAddr)
}
