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
	"os"
	"os/signal"
	"syscall"
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
			// The client has closed the stream
			logger.Info("Client has closed the KubernetesResources stream")
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
			// The client has closed the stream
			logger.Info("Client has closed the logs stream")
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
	// Decode the JSON-encoded string into a LogEntry struct
	var logEntry LogEntry = make(map[string]any)
	if err := json.Unmarshal([]byte(log.JsonMessage), &logEntry); err != nil {
		logger.Error("Error decoding JSON log message", zap.Error(err))
		return status.Error(codes.InvalidArgument, "Invalid JSON message")
	}

	// Check for an empty log entry
	if len(logEntry) == 0 {
		logger.Warn("Received empty log entry")
		return status.Error(codes.InvalidArgument, "Empty log entry")
	}

	level, err := logEntry.Level()
	if err != nil {
		logger.Error("Error converting log level", zap.Error(err))
		return status.Error(codes.InvalidArgument, "Invalid log level")
	}

	if ce := logger.Check(level, "Received log entry from cloud-operator"); ce != nil {
		ce.Write(
			zap.Object("entry", logEntry),
		)
	}
	return nil
}

// Stub implementation for GetConfigurationUpdates
func (s *server) GetConfigurationUpdates(stream pb.KubernetesInfoService_GetConfigurationUpdatesServer) error {
	logger.Info("GetConfigurationUpdates stream started")
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			// The client has closed the stream
			logger.Info("Client has closed the ConfigurationUpdates stream")
			return nil
		}
		if err != nil {
			return err
		}

		switch req.Request.(type) {
		case *pb.GetConfigurationUpdatesRequest_Keepalive:
			logger.Info("Received GetConfigurationUpdates keepalive request")
		}
	}
}

func (s *server) SendKubernetesNetworkFlows(stream pb.KubernetesInfoService_SendKubernetesNetworkFlowsServer) error {
	logger.Info("SendKubernetesNetworkFlows stream started")
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			// The client has closed the stream
			logger.Info("Client has closed the flows stream")
			return nil
		}
		if err != nil {
			return err
		}

		switch req.Request.(type) {
		case *pb.SendKubernetesNetworkFlowsRequest_CiliumFlow:
			logger.Info("Received CiliumFlow")
		case *pb.SendKubernetesNetworkFlowsRequest_FiveTupleFlow:
			logger.Info("Received FalcoFlow")
		}
	}
}

func tokenAuthStreamInterceptor(expectedToken string) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		md, ok := metadata.FromIncomingContext(ss.Context())
		if !ok {
			return status.Errorf(codes.Unauthenticated, "Metadata not provided")
		}
		tokens := md["authorization"]
		if len(tokens) == 0 || tokens[0] != fmt.Sprintf("Bearer %s", expectedToken) {
			return status.Errorf(codes.Unauthenticated, "Invalid token in request")
		}
		return handler(srv, ss)
	}
}

func (fs *FakeServer) start() error {
	logger = fs.logger
	logger.Info("Starting FakeServer", zap.String("address", fs.address), zap.String("httpAddress", fs.httpAddress), zap.String("token", fs.token))

	// Start gRPC server
	var err error
	fs.listener, err = net.Listen("tcp", fs.address)
	if err != nil {
		logger.Error("Failed to start gRPC listener", zap.String("address", fs.address), zap.Error(err))
		return err
	}
	logger.Info("gRPC listener started", zap.String("address", fs.listener.Addr().String()))

	creds, err := generateSelfSignedCert()
	if err != nil {
		logger.Error("Failed to generate self-signed certificate", zap.Error(err))
		return err
	}

	credsTLS := credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{creds}, MinVersion: tls.VersionTLS12})
	serverState = fs.state
	fs.server = grpc.NewServer(grpc.Creds(credsTLS), grpc.KeepaliveEnforcementPolicy(kaep), grpc.KeepaliveParams(kasp), grpc.StreamInterceptor(tokenAuthStreamInterceptor(fs.token)))
	pb.RegisterKubernetesInfoServiceServer(fs.server, &server{})

	// Handle termination signals for graceful shutdown
	go fs.handleSignals()

	go func() {
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
		_ = recover() // Ignore the returned value of recover()
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

func (fs *FakeServer) handleSignals() {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	<-signalChan
	logger.Info("Received termination signal, shutting down FakeServer")
	fs.stop()
	os.Exit(0)
}
