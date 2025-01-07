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
	// injectToken           string
	// FailOnFirstConnection bool
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

type server struct {
	pb.UnimplementedKubernetesInfoServiceServer
}

func (s *server) SendKubernetesResources(stream pb.KubernetesInfoService_SendKubernetesResourcesServer) error {
	logger, _ := zap.NewDevelopment()
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
		case *pb.SendKubernetesResourcesRequest_KubernetesResourceMutation:
			logger.Info("Mutation Detected")
			logger.Info(req.String())
		}
		if err := stream.Send(&pb.SendKubernetesResourcesResponse{}); err != nil {
			return err
		}
	}
}

func (s *server) SendLogs(stream pb.KubernetesInfoService_SendLogsServer) error {
	logger, _ := zap.NewDevelopment()
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
			logReceivedLogEntry(logEntry, logger)
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
	var err error
	// Start gRPC server
	fs.listener, err = net.Listen("tcp", fs.address)
	if err != nil {
		return err
	}

	creds, err := generateSelfSignedCert()
	if err != nil {
		return err
	}
	credsTLS := credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{creds}, MinVersion: tls.VersionTLS12})

	fs.server = grpc.NewServer(grpc.Creds(credsTLS), grpc.KeepaliveEnforcementPolicy(kaep), grpc.KeepaliveParams(kasp), grpc.StreamInterceptor(tokenAuthStreamInterceptor(fs.token)))
	pb.RegisterKubernetesInfoServiceServer(fs.server, &server{})

	go func() {
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
	fs.httpServer, err = startOAuth2HTTPServer(fs.httpAddress, creds, fs.authService)
	if err != nil {
		return err
	}

	return nil
}

func (fs *FakeServer) stop() {
	// Graceful shutdown for gRPC server
	fs.server.GracefulStop()
	fs.listener.Close()

	// Graceful shutdown for HTTP server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fs.httpServer.Shutdown(ctx); err != nil {
		fs.logger.Fatal("Failed to gracefully shut down HTTP server", zap.Error(err))
	}
	fs.httpListener.Close()

	close(fs.stopChan)
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
