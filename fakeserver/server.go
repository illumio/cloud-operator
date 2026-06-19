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
	"sync"
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
	configMu        sync.Mutex   // guards ConfigResponses against the close+reassign in DisconnectConfigStream
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
			fs.Logger.Info("Client has closed the KubernetesResources stream")

			return nil
		}

		if err != nil {
			return err
		}

		switch req.GetRequest().(type) {
		case *pb.SendKubernetesResourcesRequest_Keepalive:
			fs.Logger.Info("Received Keepalive for resources stream")
		case *pb.SendKubernetesResourcesRequest_ClusterMetadata:
			fs.Logger.Info("Cluster metadata received")

			fs.State.ResourcesReceived++
		case *pb.SendKubernetesResourcesRequest_ResourceData:
			fs.Logger.Info("Initial inventory data")

			fs.State.ResourcesReceived++
		case *pb.SendKubernetesResourcesRequest_ResourceSnapshotComplete:
			fs.Logger.Info("Initial inventory complete")

			if fs.State.BadIntialCommit {
				fs.State.BadIntialCommit = false

				return io.EOF
			}

			fs.State.ConnectionSuccessful = true
			fs.State.ResourceSnapshotComplete = true
		case *pb.SendKubernetesResourcesRequest_KubernetesResourceMutation:
			fs.Logger.Info("Mutation Detected")

			fs.State.ResourcesReceived++
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
			fs.Logger.Info("Client has closed the logs stream")

			return nil
		}

		if err != nil {
			return err
		}

		switch req.GetRequest().(type) {
		case *pb.SendLogsRequest_LogEntry:
			logEntry := req.GetLogEntry()

			err = logReceivedLogEntry(logEntry, fs.Logger)
			if err != nil {
				fs.Logger.Error("Error recording logs from operator", zap.Error(err))
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
	fs.Logger.Info("GetConfigurationUpdates stream started")

	// Read keepalives in the background so the stream stays alive.
	go func() {
		for {
			req, err := stream.Recv()
			if err != nil {
				return
			}

			switch req.GetRequest().(type) {
			case *pb.GetConfigurationUpdatesRequest_Keepalive:
				fs.Logger.Info("Received GetConfigurationUpdates keepalive request")
			default:
				// Ignore other types.
			}
		}
	}()

	// Snapshot the channel under the lock so a concurrent DisconnectConfigStream
	// (which closes and reassigns the field) can't race this read.
	fs.configMu.Lock()
	ch := fs.ConfigResponses
	fs.configMu.Unlock()

	// Send responses from the channel. The test (or default setup) controls what gets sent.
	for resp := range ch {
		if err := stream.Send(resp); err != nil {
			fs.Logger.Error("Failed to send config response", zap.Error(err))

			return err
		}
	}

	return nil
}

func (fs *FakeServer) SendKubernetesNetworkFlows(stream pb.KubernetesInfoService_SendKubernetesNetworkFlowsServer) error {
	fs.Logger.Info("SendKubernetesNetworkFlows stream started")

	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			// The client has closed the stream
			fs.Logger.Info("Client has closed the flows stream")

			return nil
		}

		if err != nil {
			return err
		}

		switch req.GetRequest().(type) {
		case *pb.SendKubernetesNetworkFlowsRequest_Keepalive:
			fs.Logger.Info("Received Keepalive for flows stream")
		case *pb.SendKubernetesNetworkFlowsRequest_CiliumFlow:
			fs.State.RecordCiliumFlow()
			fs.Logger.Info("Received CiliumFlow")
		case *pb.SendKubernetesNetworkFlowsRequest_FiveTupleFlow:
			fs.State.RecordFiveTupleFlow()
			fs.Logger.Info("Received FiveTupleFlow")
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

// SendConfig sends a response to the current config responses channel. It copies the
// channel pointer under the lock, then sends on the local copy outside the lock, so a
// concurrent DisconnectConfigStream reassign can't race the field read.
func (fs *FakeServer) SendConfig(resp *pb.GetConfigurationUpdatesResponse) {
	fs.configMu.Lock()
	ch := fs.ConfigResponses
	fs.configMu.Unlock()

	ch <- resp
}

// DisconnectConfigStream closes the current config responses channel, causing the
// active GetConfigurationUpdates stream to end (client sees EOF). A new channel is
// created so subsequent sends to ConfigResponses work for the next connected client.
// The close+reassign is done under the lock to protect the shared field access.
func (fs *FakeServer) DisconnectConfigStream() {
	fs.configMu.Lock()
	defer fs.configMu.Unlock()

	close(fs.ConfigResponses)
	fs.ConfigResponses = make(chan *pb.GetConfigurationUpdatesResponse, 10)
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
	fs.Logger.Info("Starting FakeServer", zap.String("address", fs.Address), zap.String("httpAddress", fs.HTTPAddress), zap.String("token", fs.Token))

	// Start gRPC server
	var err error

	var listenerConfig net.ListenConfig

	fs.listener, err = listenerConfig.Listen(context.Background(), "tcp", fs.Address)
	if err != nil {
		fs.Logger.Error("Failed to start gRPC listener", zap.String("address", fs.Address), zap.Error(err))

		return err
	}

	fs.Logger.Info("gRPC listener started", zap.String("address", fs.listener.Addr().String()))

	creds, err := generateSelfSignedCert()
	if err != nil {
		fs.Logger.Error("Failed to generate self-signed certificate", zap.Error(err))

		return err
	}

	credsTLS := credentials.NewTLS(
		&tls.Config{
			Certificates: []tls.Certificate{creds},
			MinVersion:   tls.VersionTLS12,
		})
	fs.server = grpc.NewServer(grpc.Creds(credsTLS), grpc.KeepaliveEnforcementPolicy(kaep), grpc.KeepaliveParams(kasp), grpc.StreamInterceptor(tokenAuthStreamInterceptor(fs.Token)))
	pb.RegisterKubernetesInfoServiceServer(fs.server, fs)

	// Handle termination signals for graceful shutdown
	go fs.handleSignals()

	go func() {
		fs.Logger.Info("Starting gRPC server", zap.String("address", fs.Address))

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

	fs.Logger.Info("HTTP server started", zap.String("httpAddress", fs.HTTPAddress))

	return nil
}

func (fs *FakeServer) Stop() {
	fs.Logger.Info("Stopping FakeServer")

	defer func() {
		_ = recover() // Ignore the returned value of recover()
	}()

	// Shutdown gRPC server
	if fs.server != nil {
		fs.Logger.Info("Stopping gRPC server")
		fs.server.Stop()
	} else {
		fs.Logger.Warn("gRPC server was nil during shutdown")
	}

	if fs.listener != nil {
		fs.Logger.Info("Closing gRPC listener")

		err := fs.listener.Close()
		if err != nil {
			fs.Logger.Error("Error closing gRPC listener", zap.Error(err))
		}
	} else {
		fs.Logger.Warn("gRPC listener was nil during shutdown")
	}

	// Shutdown HTTP server (if any)
	if fs.httpServer != nil {
		fs.Logger.Info("Shutting down HTTP server")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := fs.httpServer.Shutdown(ctx)
		if err != nil {
			fs.Logger.Error("Failed to gracefully shut down HTTP server", zap.Error(err))
		}
	} else {
		fs.Logger.Warn("HTTP server was nil during shutdown")
	}

	if fs.httpListener != nil {
		fs.Logger.Info("Closing HTTP listener")

		err := fs.httpListener.Close()
		if err != nil {
			fs.Logger.Error("Error closing HTTP listener", zap.Error(err))
		}
	} else {
		fs.Logger.Warn("HTTP listener was nil during shutdown")
	}

	// Reset HTTP handlers before restarting the server
	http.DefaultServeMux = http.NewServeMux()

	if fs.StopChan != nil {
		fs.Logger.Info("Closing stop channel")
		close(fs.StopChan)
	} else {
		fs.Logger.Warn("Stop channel was nil during shutdown")
	}

	fs.Logger.Info("FakeServer stopped successfully")
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
	fs.Logger.Info("Received termination signal, shutting down FakeServer")
	fs.Stop()
	os.Exit(0)
}
