// Copyright 2024 Illumio, Inc. All Rights Reserved.

package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"

	// "net/http" // http.Server is now managed by AuthService
	"sync"
	"time"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	AuthorizationHeader = "authorization"
	DefaultClientID     = "client_id_1"
	DefaultClientSecret = "client_secret_1"
)

var kaep = keepalive.EnforcementPolicy{
	MinTime:             10 * time.Second,
	PermitWithoutStream: true,
}

var kasp = keepalive.ServerParameters{
	MaxConnectionIdle: 30 * time.Second, // Should be > kaep.MinTime
	Time:              20 * time.Second, // Ping frequency
	Timeout:           10 * time.Second, // Ping timeout
}

// FakeServer manages the gRPC server and its associated HTTP auth service.
type FakeServer struct {
	address     string
	httpAddress string
	grpcServer  *grpc.Server // Renamed from 'server' for clarity
	// httpServer is now managed by authService
	grpcListener net.Listener // Renamed from 'listener'
	// httpListener is now managed by authService
	state       *ServerState
	shutdownWg  sync.WaitGroup
	token       string // This is the JWT token the gRPC server expects
	logger      *zap.Logger
	authService *AuthService // OAuth2 AuthService to handle the /authenticate and /onboard endpoints
}

// ServerState holds dynamic state for the FakeServer.
type ServerState struct {
	ConnectionSuccessful bool
	IncorrectCredentials bool
	BadInitialCommit     bool // Renamed for consistent casing
}

// LogEntry is a type for structured logging.
type LogEntry map[string]any

// Level extracts the log level from a LogEntry.
func (l LogEntry) Level() (zapcore.Level, error) {
	levelStr, found := l["level"].(string)
	if !found {
		return zapcore.InvalidLevel, errors.New("no level field found in log entry")
	}
	var level zapcore.Level
	err := level.UnmarshalText([]byte(levelStr))
	if err != nil {
		return zapcore.InvalidLevel, fmt.Errorf("invalid level field %s found in log entry: %w", levelStr, err)
	}
	return level, nil
}

// MarshalLogObject implements zapcore.ObjectMarshaler for LogEntry.
func (l LogEntry) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	for k, v := range l {
		err := enc.AddReflected(k, v)
		if err != nil {
			return err
		}
	}
	return nil
}

// grpcServiceImpl implements the gRPC service.
type grpcServiceImpl struct {
	pb.UnimplementedKubernetesInfoServiceServer
	logger      *zap.Logger
	serverState *ServerState
}

// newGRPCServiceImpl creates a new gRPC service implementation.
func newGRPCServiceImpl(logger *zap.Logger, state *ServerState) *grpcServiceImpl {
	return &grpcServiceImpl{
		logger:      logger.Named("gRPCService"),
		serverState: state,
	}
}

func (s *grpcServiceImpl) SendKubernetesResources(stream pb.KubernetesInfoService_SendKubernetesResourcesServer) error {
	s.logger.Info("gRPC stream SendKubernetesResources started.")
	for {
		// Check if context is cancelled (e.g. client disconnected or server shutting down)
		if err := stream.Context().Err(); err != nil {
			s.logger.Info("SendKubernetesResources stream context error", zap.Error(err))
			return err
		}

		req, err := stream.Recv()
		if err == io.EOF {
			s.logger.Info("SendKubernetesResources stream ended by client (EOF).")
			return nil
		}
		if err != nil {
			s.logger.Error("SendKubernetesResources stream Recv error", zap.Error(err))
			return err // Consider status.Errorf for gRPC specific errors
		}
		switch req.Request.(type) {
		case *pb.SendKubernetesResourcesRequest_ClusterMetadata:
			s.logger.Info("Cluster metadata received")
		case *pb.SendKubernetesResourcesRequest_ResourceData:
			s.logger.Info("Initial inventory data received")
		case *pb.SendKubernetesResourcesRequest_ResourceSnapshotComplete:
			s.logger.Info("Initial inventory complete received")
			if s.serverState.BadInitialCommit {
				s.logger.Warn("Simulating bad initial commit, closing stream.")
				s.serverState.BadInitialCommit = false // Reset for next attempt
				// Returning EOF might be interpreted as success by some clients.
				// Consider a specific gRPC error if applicable.
				return status.Error(codes.Aborted, "simulated bad initial commit")
			}
			s.serverState.ConnectionSuccessful = true
		case *pb.SendKubernetesResourcesRequest_KubernetesResourceMutation:
			s.logger.Info("Mutation Detected")
		default:
			s.logger.Warn("Received unknown SendKubernetesResourcesRequest type")
		}
		if err := stream.Send(&pb.SendKubernetesResourcesResponse{}); err != nil {
			s.logger.Error("SendKubernetesResources stream Send error", zap.Error(err))
			return err
		}
	}
}

func (s *grpcServiceImpl) SendLogs(stream pb.KubernetesInfoService_SendLogsServer) error {
	s.logger.Info("gRPC stream SendLogs started.")
	for {
		if err := stream.Context().Err(); err != nil {
			s.logger.Info("SendLogs stream context error", zap.Error(err))
			return err
		}
		req, err := stream.Recv()
		if err == io.EOF {
			s.logger.Info("SendLogs stream ended by client (EOF).")
			return nil
		}
		if err != nil {
			s.logger.Error("SendLogs stream Recv error", zap.Error(err))
			return err
		}

		switch req.Request.(type) {
		case *pb.SendLogsRequest_LogEntry:
			logEntryMsg := req.GetLogEntry()
			err = s.logReceivedLogEntry(logEntryMsg) // Use method on s
			if err != nil {
				s.logger.Error("Error recording logs from operator", zap.Error(err))
				// Potentially send an error back to client if the RPC expects a response per message
			}
		default:
			s.logger.Warn("Received unknown SendLogsRequest type")
		}
	}
}

func (s *grpcServiceImpl) logReceivedLogEntry(logMsg *pb.LogEntry) error {
	var logEntryData LogEntry = make(map[string]any)
	if err := json.Unmarshal([]byte(logMsg.JsonMessage), &logEntryData); err != nil {
		s.logger.Error("Error decoding JSON log message", zap.Error(err))
		return status.Errorf(codes.Internal, "internal error decoding log message: %v", err)
	}

	if len(logEntryData) == 0 {
		s.logger.Warn("Received empty log entry")
		return status.Errorf(codes.InvalidArgument, "empty log entry received")
	}

	level, err := logEntryData.Level()
	if err != nil {
		s.logger.Error("Error converting log level from received entry", zap.Error(err), zap.Any("log_data", logEntryData))
		return status.Errorf(codes.Internal, "internal error converting log level: %v", err)
	}

	if ce := s.logger.Check(level, "Received log entry from cloud-operator"); ce != nil {
		ce.Write(zap.Object("entry", logEntryData))
	}
	return nil
}

func (s *grpcServiceImpl) GetConfigurationUpdates(stream pb.KubernetesInfoService_GetConfigurationUpdatesServer) error {
	s.logger.Info("gRPC stream GetConfigurationUpdates started.")
	for {
		if err := stream.Context().Err(); err != nil {
			s.logger.Info("GetConfigurationUpdates stream context error", zap.Error(err))
			return err
		}
		req, err := stream.Recv()
		if err == io.EOF {
			s.logger.Info("GetConfigurationUpdates stream ended by client (EOF).")
			return nil
		}
		if err != nil {
			s.logger.Error("GetConfigurationUpdates stream Recv error", zap.Error(err))
			return err
		}

		switch req.Request.(type) {
		case *pb.GetConfigurationUpdatesRequest_Keepalive:
			s.logger.Info("Received GetConfigurationUpdates keepalive request")
			// Optionally send a response if the proto defines one for keepalive
		default:
			s.logger.Warn("Received unknown GetConfigurationUpdatesRequest type")
		}
	}
}

func (s *grpcServiceImpl) SendKubernetesNetworkFlows(stream pb.KubernetesInfoService_SendKubernetesNetworkFlowsServer) error {
	s.logger.Info("gRPC stream SendKubernetesNetworkFlows started.")
	for {
		if err := stream.Context().Err(); err != nil {
			s.logger.Info("SendKubernetesNetworkFlows stream context error", zap.Error(err))
			return err
		}
		req, err := stream.Recv()
		if err == io.EOF {
			s.logger.Info("SendKubernetesNetworkFlows stream ended by client (EOF).")
			return nil
		}
		if err != nil {
			s.logger.Error("SendKubernetesNetworkFlows stream Recv error", zap.Error(err))
			return err
		}

		switch req.Request.(type) {
		case *pb.SendKubernetesNetworkFlowsRequest_CiliumFlow:
			s.logger.Info("Received CiliumFlow")
		case *pb.SendKubernetesNetworkFlowsRequest_FalcoFlow:
			s.logger.Info("Received FalcoFlow")
		default:
			s.logger.Warn("Received unknown SendKubernetesNetworkFlowsRequest type")
		}
	}
}

// tokenAuthStreamInterceptor validates the JWT token from the stream's context.
func tokenAuthStreamInterceptor(expectedTokenValue string, logger *zap.Logger) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		md, ok := metadata.FromIncomingContext(ss.Context())
		if !ok {
			logger.Warn("Authentication failed: Metadata not provided", zap.String("method", info.FullMethod))
			return status.Errorf(codes.Unauthenticated, "metadata not provided")
		}
		authHeaders := md[AuthorizationHeader] // "authorization"
		if len(authHeaders) == 0 {
			logger.Warn("Authentication failed: Authorization header not provided", zap.String("method", info.FullMethod))
			return status.Errorf(codes.Unauthenticated, "authorization token not provided")
		}
		// The token should be "Bearer <actual_token_value>"
		parts := strings.SplitN(authHeaders[0], " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			logger.Warn("Authentication failed: Authorization header format is not 'Bearer <token>'", zap.String("method", info.FullMethod))
			return status.Errorf(codes.Unauthenticated, "authorization header format must be Bearer token")
		}
		receivedToken := parts[1]

		if receivedToken != expectedTokenValue {
			logger.Warn("Authentication failed: Invalid token",
				zap.String("method", info.FullMethod),
				// Avoid logging tokens unless in a very controlled debug environment
				// zap.String("received_token", receivedToken),
			)
			return status.Errorf(codes.Unauthenticated, "invalid token")
		}
		logger.Debug("Token authentication successful", zap.String("method", info.FullMethod))
		return handler(srv, ss)
	}
}

// Start initializes and starts the FakeServer's components.
// It's non-blocking. Call Stop for graceful shutdown.
func (fs *FakeServer) Start() error {
	fs.logger.Info("Starting FakeServer",
		zap.String("grpc_address", fs.address),
		zap.String("http_address", fs.httpAddress))
	// zap.String("expected_token_value", fs.token)) // Avoid logging token value

	// 1. Generate self-signed certificate
	tlsCert, err := generateSelfSignedCert()
	if err != nil {
		fs.logger.Error("Failed to generate self-signed certificate", zap.Error(err))
		return fmt.Errorf("generating self-signed cert: %w", err)
	}

	// 2. Start gRPC server
	fs.grpcListener, err = net.Listen("tcp", fs.address)
	if err != nil {
		fs.logger.Error("Failed to start gRPC listener", zap.String("address", fs.address), zap.Error(err))
		return fmt.Errorf("gRPC listener on %s: %w", fs.address, err)
	}
	fs.logger.Info("gRPC listener started", zap.String("address", fs.grpcListener.Addr().String()))

	grpcCreds := credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{tlsCert}, MinVersion: tls.VersionTLS12})
	fs.grpcServer = grpc.NewServer(
		grpc.Creds(grpcCreds),
		grpc.KeepaliveEnforcementPolicy(kaep),
		grpc.KeepaliveParams(kasp),
		grpc.StreamInterceptor(tokenAuthStreamInterceptor(fs.token, fs.logger)), // Pass logger
	)
	pb.RegisterKubernetesInfoServiceServer(fs.grpcServer, newGRPCServiceImpl(fs.logger, fs.state))

	fs.shutdownWg.Add(1) // For gRPC server goroutine
	go func() {
		defer fs.shutdownWg.Done()
		fs.logger.Info("Starting gRPC server to serve requests", zap.String("address", fs.address))
		if err := fs.grpcServer.Serve(fs.grpcListener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			fs.logger.Error("gRPC server failed to serve", zap.Error(err))
			// Consider signaling main application to stop if gRPC server fails critically
		}
		fs.logger.Info("gRPC server stopped serving.")
	}()

	// 3. Create and initialize the AuthService
	// The fs.token is the JWT that the gRPC server expects.
	// The AuthService will issue this token upon successful client_id/secret authentication.
	fs.authService = newAuthService(fs.logger, DefaultClientID, DefaultClientSecret, fs.token)

	// 4. Start the AuthService HTTP server
	fs.shutdownWg.Add(1) // For AuthService HTTP server goroutine
	go func() {
		defer fs.shutdownWg.Done()
		if err := fs.authService.startHTTPServer(fs.httpAddress, tlsCert); err != nil {
			fs.logger.Error("Failed to start AuthService HTTP server", zap.Error(err))
			// Consider signaling main application to stop
		}
		// The startHTTPServer itself has a goroutine for ServeTLS,
		// this goroutine here is mainly to manage the shutdownWg.
		// We need to wait for the authService's internal server to stop, which is done in fs.Stop()
	}()

	fs.logger.Info("FakeServer is ready to accept connections.")
	return nil
}

// Stop gracefully shuts down the FakeServer's components.
func (fs *FakeServer) Stop(ctx context.Context) error {
	fs.logger.Info("Stopping FakeServer...")
	var firstErr error

	// 1. Stop gRPC server
	if fs.grpcServer != nil {
		fs.logger.Info("Gracefully stopping gRPC server...")
		// fs.grpcServer.GracefulStop() will close the listener automatically.
		fs.grpcServer.GracefulStop()
		fs.logger.Info("gRPC server GracefulStop initiated.")
	} else {
		fs.logger.Warn("gRPC server was nil during shutdown.")
	}

	// 2. Stop AuthService HTTP server
	if fs.authService != nil {
		fs.logger.Info("Shutting down AuthService HTTP server...")
		// Use the provided context for HTTP server shutdown.
		if err := fs.authService.stopHTTPServer(ctx); err != nil {
			fs.logger.Error("Failed to gracefully shut down AuthService HTTP server", zap.Error(err))
			if firstErr == nil {
				firstErr = fmt.Errorf("authService HTTP server shutdown: %w", err)
			}
		}
	} else {
		fs.logger.Warn("AuthService was nil during shutdown.")
	}

	// 3. Wait for server goroutines to finish
	fs.logger.Info("Waiting for FakeServer components to shut down...")
	waitDone := make(chan struct{})
	go func() {
		fs.shutdownWg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		fs.logger.Info("All FakeServer components shut down.")
	case <-ctx.Done(): // Use the passed-in context for overall timeout
		fs.logger.Error("Timed out waiting for FakeServer components to shut down.", zap.Error(ctx.Err()))
		if firstErr == nil {
			firstErr = fmt.Errorf("timeout waiting for components: %w", ctx.Err())
		}
	}

	// Reset HTTP handlers if necessary (though authService uses its own mux)
	// http.DefaultServeMux = http.NewServeMux() // Generally not needed if not using DefaultServeMux directly

	fs.logger.Info("FakeServer stop process completed.")
	return firstErr
}

// generateSelfSignedCert creates a new self-signed TLS certificate.
func generateSelfSignedCert() (tls.Certificate, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generating RSA key: %w", err)
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(time.Hour * 24 * 365) // 1 year validity

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generating serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Fake Server Co"},
			CommonName:   "fakeserver.example.com",
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IsCA:                  true, // Mark as CA
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost", "fakeserver.example.com"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("creating certificate: %w", err)
	}

	certBuf := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyBuf := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})

	cert, err := tls.X509KeyPair(certBuf, keyBuf)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("loading X509 key pair: %w", err)
	}
	return cert, nil
}

// Note: The original fs.handleSignals() and os.Exit(0) are removed.
// Signal handling should be done in main.go for better control over all servers.
