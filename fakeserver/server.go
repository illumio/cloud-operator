// Copyright 2024 Illumio, Inc. All Rights Reserved.

package fakeserver

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

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
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
	pb.UnimplementedKubernetesInfoServiceServer

	Address         string
	HTTPAddress     string
	server          *grpc.Server
	httpServer      *http.Server
	listener        net.Listener
	httpListener    net.Listener
	State           *ServerState
	StopChan        chan struct{}
	Token           string
	Logger          *zap.Logger
	authService     *AuthService // OAuth2 AuthService to handle the /authenticate and /onboard endpoints
	ConfigResponses chan *pb.GetConfigurationUpdatesResponse
}

// RecordCiliumFlow increments the Cilium flow counter.
func (s *ServerState) RecordCiliumFlow() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.CiliumFlowsReceived++
}

// RecordFiveTupleFlow increments the FiveTuple flow counter.
func (s *ServerState) RecordFiveTupleFlow() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.FiveTupleFlowsReceived++
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

func (fs *FakeServer) SendKubernetesResources(stream pb.KubernetesInfoService_SendKubernetesResourcesServer) error {
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			logger.Info("Client has closed the KubernetesResources stream")

			return nil
		}

		if err != nil {
			return err
		}

		switch req.GetRequest().(type) {
		case *pb.SendKubernetesResourcesRequest_Keepalive:
			logger.Info("Received Keepalive for resources stream")
		case *pb.SendKubernetesResourcesRequest_ClusterMetadata:
			logger.Info("Cluster metadata received")

			serverState.ResourcesReceived++
		case *pb.SendKubernetesResourcesRequest_ResourceData:
			logger.Info("Initial inventory data")

			serverState.ResourcesReceived++
		case *pb.SendKubernetesResourcesRequest_ResourceSnapshotComplete:
			logger.Info("Initial inventory complete")

			if serverState.BadIntialCommit {
				serverState.BadIntialCommit = false

				return io.EOF
			}

			serverState.ConnectionSuccessful = true
			serverState.ResourceSnapshotComplete = true
		case *pb.SendKubernetesResourcesRequest_KubernetesResourceMutation:
			logger.Info("Mutation Detected")

			serverState.ResourcesReceived++
		}

		if err := stream.Send(&pb.SendKubernetesResourcesResponse{}); err != nil {
			return err
		}
	}
}

func (fs *FakeServer) SendLogs(stream pb.KubernetesInfoService_SendLogsServer) error {
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			// The client has closed the stream
			logger.Info("Client has closed the logs stream")

			return nil
		}

		if err != nil {
			return err
		}

		switch req.GetRequest().(type) {
		case *pb.SendLogsRequest_LogEntry:
			logEntry := req.GetLogEntry()

			err = logReceivedLogEntry(logEntry, logger)
			if err != nil {
				logger.Error("Error recording logs from operator", zap.Error(err))
			}
		default:
			// Ignore other types.
		}
	}
}

func logReceivedLogEntry(log *pb.LogEntry, logger *zap.Logger) error {
	// Decode the JSON-encoded string into a LogEntry struct
	var logEntry LogEntry = make(map[string]any)
	if err := json.Unmarshal([]byte(log.GetJsonMessage()), &logEntry); err != nil {
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

func (fs *FakeServer) GetConfigurationUpdates(stream pb.KubernetesInfoService_GetConfigurationUpdatesServer) error {
	logger.Info("GetConfigurationUpdates stream started")

	// Read keepalives in the background so the stream stays alive.
	go func() {
		for {
			req, err := stream.Recv()
			if err != nil {
				return
			}

			switch req.GetRequest().(type) {
			case *pb.GetConfigurationUpdatesRequest_Keepalive:
				logger.Info("Received GetConfigurationUpdates keepalive request")
			default:
				// Ignore other types.
			}
		}
	}()

	// Send responses from the channel. The test (or default setup) controls what gets sent.
	for resp := range fs.ConfigResponses {
		if err := stream.Send(resp); err != nil {
			logger.Error("Failed to send config response", zap.Error(err))

			return err
		}
	}

	return nil
}

func (fs *FakeServer) SendKubernetesNetworkFlows(stream pb.KubernetesInfoService_SendKubernetesNetworkFlowsServer) error {
	logger.Info("SendKubernetesNetworkFlows stream started")

	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			// The client has closed the stream
			logger.Info("Client has closed the flows stream")

			return nil
		}

		if err != nil {
			return err
		}

		switch req.GetRequest().(type) {
		case *pb.SendKubernetesNetworkFlowsRequest_Keepalive:
			logger.Info("Received Keepalive for flows stream")
		case *pb.SendKubernetesNetworkFlowsRequest_CiliumFlow:
			serverState.RecordCiliumFlow()
			logger.Info("Received CiliumFlow")
		case *pb.SendKubernetesNetworkFlowsRequest_FiveTupleFlow:
			serverState.RecordFiveTupleFlow()
			logger.Info("Received FiveTupleFlow")
		}
	}
}

func tokenAuthStreamInterceptor(expectedToken string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		md, ok := metadata.FromIncomingContext(ss.Context())
		if !ok {
			return status.Errorf(codes.Unauthenticated, "Metadata not provided")
		}

		tokens := md["authorization"]
		if len(tokens) == 0 || tokens[0] != "Bearer "+expectedToken {
			return status.Errorf(codes.Unauthenticated, "Invalid token in request")
		}

		return handler(srv, ss)
	}
}

// SendConfigResponse sends a configuration response to the connected config stream client.
func (fs *FakeServer) SendConfigResponse(resp *pb.GetConfigurationUpdatesResponse) {
	fs.ConfigResponses <- resp
}

// GRPCAddress returns the actual address the gRPC server is listening on.
// Useful when the server is started with ":0" to get the OS-assigned port.
func (fs *FakeServer) GRPCAddress() string {
	if fs.listener != nil {
		return fs.listener.Addr().String()
	}

	return fs.Address
}

func (fs *FakeServer) Start() error {
	logger = fs.Logger
	logger.Info("Starting FakeServer", zap.String("address", fs.Address), zap.String("httpAddress", fs.HTTPAddress), zap.String("token", fs.Token))

	// Start gRPC server
	var err error

	var listenerConfig net.ListenConfig

	fs.listener, err = listenerConfig.Listen(context.Background(), "tcp", fs.Address)
	if err != nil {
		logger.Error("Failed to start gRPC listener", zap.String("address", fs.Address), zap.Error(err))

		return err
	}

	logger.Info("gRPC listener started", zap.String("address", fs.listener.Addr().String()))

	creds, err := generateSelfSignedCert()
	if err != nil {
		logger.Error("Failed to generate self-signed certificate", zap.Error(err))

		return err
	}

	credsTLS := credentials.NewTLS(
		&tls.Config{
			Certificates: []tls.Certificate{creds},
			MinVersion:   tls.VersionTLS12,
		})
	serverState = fs.State
	fs.server = grpc.NewServer(grpc.Creds(credsTLS), grpc.KeepaliveEnforcementPolicy(kaep), grpc.KeepaliveParams(kasp), grpc.StreamInterceptor(tokenAuthStreamInterceptor(fs.Token)))
	pb.RegisterKubernetesInfoServiceServer(fs.server, fs)

	// Handle termination signals for graceful shutdown
	go fs.handleSignals()

	go func() {
		logger.Info("Starting gRPC server", zap.String("address", fs.Address))

		if err := fs.server.Serve(fs.listener); err != nil {
			fs.Logger.Fatal("Failed to start gRPC server", zap.Error(err))
		}
	}()

	// Create and initialize the AuthService (OAuth2)
	fs.authService = &AuthService{
		logger:       fs.Logger,
		clientID:     DefaultClientID,
		clientSecret: DefaultClientSecret,
		token:        fs.Token,
	}

	// Start the OAuth2 HTTP server
	fs.httpServer = startHTTPServer(fs.HTTPAddress, creds, fs.authService)

	logger.Info("HTTP server started", zap.String("httpAddress", fs.HTTPAddress))

	return nil
}

func (fs *FakeServer) Stop() {
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

	if fs.StopChan != nil {
		logger.Info("Closing stop channel")
		close(fs.StopChan)
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
	fs.Stop()
	os.Exit(0)
}
