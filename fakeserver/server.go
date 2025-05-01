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
	"net/http"
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
	MaxConnectionIdle: 30 * time.Second,
	Time:              30 * time.Second,
	Timeout:           10 * time.Second,
}

type FakeServer struct {
	address      string
	httpAddress  string
	server       *grpc.Server
	httpServer   *http.Server
	listener     net.Listener
	httpListener net.Listener
	state        *ServerState
	stopChan     chan struct{}
	token        string
	logger       *zap.Logger
	authService  *AuthService // OAuth2 AuthService to handle the /authenticate and /onboard endpoints
}

type ServerState struct {
	ConnectionSuccessful bool
	IncorrectCredentials bool
	BadIntialCommit      bool
}

var (
	serverState *ServerState
	logger      *zap.Logger
)

type LogEntry map[string]any

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

func (l LogEntry) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	for k, v := range l {
		err := enc.AddReflected(k, v)
		if err != nil {
			return err
		}
	}
	return nil
}

type server struct {
	pb.UnimplementedKubernetesInfoServiceServer
}

func (s *server) SendKubernetesResources(stream pb.KubernetesInfoService_SendKubernetesResourcesServer) error {
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch req.Request.(type) {
		case *pb.SendKubernetesResourcesRequest_ClusterMetadata:
			logger.Info("Cluster metadata received")
		case *pb.SendKubernetesResourcesRequest_ResourceData:
			logger.Info("Initial inventory data")
		case *pb.SendKubernetesResourcesRequest_ResourceSnapshotComplete:
			logger.Info("Initial inventory complete")
			if serverState.BadIntialCommit {
				serverState.BadIntialCommit = false
				return io.EOF
			}
			serverState.ConnectionSuccessful = true
		case *pb.SendKubernetesResourcesRequest_KubernetesResourceMutation:
			logger.Info("Mutation Detected")
		}
		if err := stream.Send(&pb.SendKubernetesResourcesResponse{}); err != nil {
			return err
		}
	}
}

func (s *server) SendLogs(stream pb.KubernetesInfoService_SendLogsServer) error {
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		switch req.Request.(type) {
		case *pb.SendLogsRequest_LogEntry:
			logEntry := req.GetLogEntry()
			err = logReceivedLogEntry(logEntry, logger)
			if err != nil {
				logger.Error("Error recording logs from operator", zap.Error(err))
			}
		}
	}
}

func logReceivedLogEntry(log *pb.LogEntry, logger *zap.Logger) error {
	var logEntry LogEntry = make(map[string]any)
	if err := json.Unmarshal([]byte(log.JsonMessage), &logEntry); err != nil {
		logger.Error("Error decoding JSON log message", zap.Error(err))
	}

	level, err := logEntry.Level()
	if err != nil {
		logger.Error("Error converting log level", zap.Error(err))
		return err
	}

	if ce := logger.Check(level, "Received log entry from cloud-operator"); ce != nil {
		ce.Write(zap.Object("entry", logEntry))
	}
	return nil
}

func tokenAuthStreamInterceptor(expectedToken string) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		md, ok := metadata.FromIncomingContext(ss.Context())
		if !ok {
			logger.Error("Metadata not provided")
			return status.Errorf(codes.Unauthenticated, "Metadata not provided")
		}
		tokens := md["authorization"]
		if len(tokens) == 0 || tokens[0] != fmt.Sprintf("Bearer %s", expectedToken) {
			logger.Error("Authorization token missing")
			return status.Errorf(codes.Unauthenticated, "Authorization token missing")
		}
		logger.Info("Token received", zap.String("token", tokens[0]))
		if tokens[0] != fmt.Sprintf("Bearer %s", expectedToken) {
			logger.Error("Invalid token in request", zap.String("received_token", tokens[0]), zap.String("expected_token", fmt.Sprintf("Bearer %s", expectedToken)))
			return status.Errorf(codes.Unauthenticated, "Invalid token in request")
		}
		return handler(srv, ss)
	}
}

func (fs *FakeServer) start() error {
	fmt.Println("Starting FakeServer") // Temporary print statement
	logger = fs.logger
	logger.Info("Starting FakeServer", zap.String("address", fs.address), zap.String("httpAddress", fs.httpAddress), zap.String("token", fs.token))
	var err error

	// Start gRPC server
	fs.listener, err = net.Listen("tcp", fs.address)
	if err != nil {
		fmt.Println("Failed to start gRPC listener") // Temporary print statement
		logger.Error("Failed to start gRPC listener", zap.String("address", fs.address), zap.Error(err))
		return err
	}
	fmt.Println("gRPC listener started") // Temporary print statement
	logger.Info("gRPC listener started", zap.String("address", fs.listener.Addr().String()))

	creds, err := generateSelfSignedCert()
	if err != nil {
		fmt.Println("Failed to generate self-signed certificate") // Temporary print statement
		logger.Error("Failed to generate self-signed certificate", zap.Error(err))
		return err
	}

	credsTLS := credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{creds}, MinVersion: tls.VersionTLS12})
	serverState = fs.state
	fs.server = grpc.NewServer(grpc.Creds(credsTLS), grpc.KeepaliveEnforcementPolicy(kaep), grpc.KeepaliveParams(kasp), grpc.StreamInterceptor(tokenAuthStreamInterceptor(fs.token)))
	pb.RegisterKubernetesInfoServiceServer(fs.server, &server{})

	go func() {
		fmt.Println("Starting gRPC server") // Temporary print statement
		logger.Info("Starting gRPC server", zap.String("address", fs.address))
		if err := fs.server.Serve(fs.listener); err != nil {
			fs.logger.Fatal("Failed to start gRPC server", zap.Error(err))
		}
	}()

	// Create and initialize the AuthService (OAuth2)
	fs.authService = &AuthService{
		logger:       fs.logger,
		clientID:     DefaultClientID,
		clientSecret: DefaultClientSecret,
		token:        fs.token,
	}

	// Start the OAuth2 HTTP server
	fs.httpServer, err = startHTTPServer(fs.httpAddress, creds, fs.authService)
	if err != nil {
		logger.Error("Failed to start HTTP server", zap.Error(err))
		return err
	}
	logger.Info("HTTP server started", zap.String("httpAddress", fs.httpAddress))

	logger.Info("FakeServer is ready to accept connections")
	return nil
}

func (fs *FakeServer) stop() {
	logger.Info("Stopping FakeServer")
	defer func() {
		if r := recover(); r != nil {
			logger.Error("Recovered from panic during server shutdown", zap.Any("panic", r))
		}
	}()

	// Shutdown gRPC server
	if fs.server != nil {
		logger.Info("Stopping gRPC server")
		fs.server.Stop()
	} else {
		logger.Warn("gRPC server was nil during shutdown")
	}

	if fs.listener != nil {
		logger.Info("Closing gRPC listener")
		err := fs.listener.Close()
		if err != nil {
			logger.Error("Error closing gRPC listener", zap.Error(err))
		}
	} else {
		logger.Warn("gRPC listener was nil during shutdown")
	}

	// Shutdown HTTP server (if any)
	if fs.httpServer != nil {
		logger.Info("Shutting down HTTP server")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := fs.httpServer.Shutdown(ctx)
		if err != nil {
			logger.Error("Failed to gracefully shut down HTTP server", zap.Error(err))
		}
	} else {
		logger.Warn("HTTP server was nil during shutdown")
	}

	if fs.httpListener != nil {
		logger.Info("Closing HTTP listener")
		err := fs.httpListener.Close()
		if err != nil {
			logger.Error("Error closing HTTP listener", zap.Error(err))
		}
	} else {
		logger.Warn("HTTP listener was nil during shutdown")
	}

	// Reset HTTP handlers before restarting the server
	http.DefaultServeMux = http.NewServeMux()

	if fs.stopChan != nil {
		logger.Info("Closing stop channel")
		close(fs.stopChan)
	} else {
		logger.Warn("Stop channel was nil during shutdown")
	}

	logger.Info("FakeServer stopped successfully")
}

func generateSelfSignedCert() (tls.Certificate, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(time.Hour * 24 * 365)

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Fake Server"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	certBuf := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyBuf := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})

	cert, err := tls.X509KeyPair(certBuf, keyBuf)
	if err != nil {
		return tls.Certificate{}, err
	}

	return cert, nil
}

// Stub implementation for GetConfigurationUpdates
func (s *server) GetConfigurationUpdates(stream pb.KubernetesInfoService_GetConfigurationUpdatesServer) error {
	logger.Info("GetConfigurationUpdates stream started")
	for {
		// Simulate receiving updates (placeholder logic)
		select {
		case <-stream.Context().Done():
			logger.Error("GetConfigurationUpdates stream context canceled", zap.Error(stream.Context().Err()))
			return stream.Context().Err()
		case <-time.After(10 * time.Second):
			logger.Info("Sending keepalive on GetConfigurationUpdates stream")
			// Placeholder: Send a keepalive or response
		}
	}
}

func (s *server) SendKubernetesNetworkFlows(stream pb.KubernetesInfoService_SendKubernetesNetworkFlowsServer) error {
	logger.Info("SendKubernetesNetworkFlows stream started")
	for {
		select {
		case <-stream.Context().Done():
			logger.Error("SendKubernetesNetworkFlows stream context canceled", zap.Error(stream.Context().Err()))
			return stream.Context().Err()
		case <-time.After(2 * time.Minute):
			logger.Info("Sending keepalive on SendKubernetesNetworkFlows stream")
			// Placeholder: Send a keepalive or response
		}
	}
}
