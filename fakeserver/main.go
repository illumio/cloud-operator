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
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"time"

	"github.com/golang-jwt/jwt"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

var kaep = keepalive.EnforcementPolicy{
	MinTime:             10 * time.Second, // Instruct the client to start sending keepalives after 30s (default is 5m, which may be too long for some proxies)
	PermitWithoutStream: true,             // Allow pings even when there are no active streams
}

var kasp = keepalive.ServerParameters{
	MaxConnectionIdle: 30 * time.Second, // If a client is idle (i.e. doesn't send an RPC) for 30 seconds, send a GoAway to close the connection.
	Time:              30 * time.Second, // Ping the client if it is idle for 30 seconds to ensure the connection is still active
	Timeout:           10 * time.Second, // Wait 10 second for the ping ack before assuming the connection is dead
}

const (
	AuthorizationHeader = "authorization"
	DefaultClientID     = "client_id_1"
	DefaultClientSecret = "client_secret_1"
)

var (
	// Version of network to listen to (TCP or UDP)
	network string
	// Address for gRPC requests
	address string
	// Token used to verify the clients JWT
	token string
	// Address for OAuth token endpoint
	tokenEndpoint string
	// Value passed in JWT to client
	aud string
)

type FakeServerTestDriver struct {
	Logger zap.Logger
	S      server
}

type server struct {
	pb.UnimplementedKubernetesInfoServiceServer
}

// LogEntry represents the structure of a zapcore.Entry encoded using Zap's JSON encoder with the production encoder config.
type LogEntry map[string]any

// Level returns the log level of the LogEntry.
func (l LogEntry) Level() (zapcore.Level, error) {
	var levelStr string
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

// MarshalLogObject implements the zapcore.ObjectMarshaler interface
func (l LogEntry) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	for k, v := range l {
		err := enc.AddReflected(k, v)
		if err != nil {
			return err
		}
	}
	return nil
}

// SendKubernetesResources handles all gPRC requests related to streaming resources
func (s *server) SendKubernetesResources(stream pb.KubernetesInfoService_SendKubernetesResourcesServer) error {
	logger, err := zap.NewDevelopment()
	if err != nil {
		panic("Failed to build zap logger: " + err.Error())
	}
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			// The client has closed the stream
			return nil
		}
		if err != nil {
			return err // Return the error to terminate the stream
		}
		switch req.Request.(type) {
		case *pb.SendKubernetesResourcesRequest_ClusterMetadata:
			logger.Info("Cluster metadata received")
		case *pb.SendKubernetesResourcesRequest_ResourceData:
			logger.Info("Intial inventory data")
		case *pb.SendKubernetesResourcesRequest_ResourceSnapshotComplete:
			logger.Info("Initial inventory complete")
		case *pb.SendKubernetesResourcesRequest_KubernetesResourceMutation:
			logger.Info("Mutation Detected")
			logger.Info(req.String())
		}
		logger.Debug("Received message from client")
		if err := stream.Send(&pb.SendKubernetesResourcesResponse{}); err != nil {
			return err
		}
	}
}

func (s *server) SendLogs(stream pb.KubernetesInfoService_SendLogsServer) error {
	logger, err := zap.NewDevelopment()
	if err != nil {
		panic("Failed to build zap logger: " + err.Error())
	}
	// Set the client's log level to DEBUG.
	setLogLevelMsg := &pb.SendLogsResponse{
		Response: &pb.SendLogsResponse_SetLogLevel{
			SetLogLevel: &pb.SetLogLevel{
				Level: pb.LogLevel_LOG_LEVEL_DEBUG,
			},
		},
	}
	if err := stream.Send(setLogLevelMsg); err != nil {
		logger.Error("Failed to send message to set log level", zap.Error(err))
		return err
	}
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			// The client has closed the stream
			logger.Info("Client has closed the SendLogs stream")
			return nil
		}
		if err != nil {
			logger.Error("Error from stream", zap.Error(err))
			return err // Return the error to terminate the stream
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

// SendKubernetesNetworkFlows handles all gPRC requests related to streaming network flows
func (s *server) SendKubernetesNetworkFlows(stream pb.KubernetesInfoService_SendKubernetesNetworkFlowsServer) error {
	logger, err := zap.NewDevelopment()
	if err != nil {
		panic("Failed to build zap logger: " + err.Error())
	}
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			// The client has closed the stream
			return nil
		}
		if err != nil {
			// Return the error to terminate the stream
			return err
		}
		// Logging for demo purposes
		logger.Info("Received flow", zap.Stringer("flow", req))
		if err := stream.Send(&pb.SendKubernetesNetworkFlowsResponse{}); err != nil {
			return err
		}
	}
}

func init() {
	flag.StringVar(&network, "network", "tcp", "network of the address of the gRPC server, e.g., \"tcp\" or \"unix\"")
	flag.StringVar(&address, "address", "127.0.0.1:50051", "address of the gRPC server to start at")
	flag.StringVar(&tokenEndpoint, "tokenEndpoint", "127.0.0.1:50053", "address of the OAuth endpoint to start at")
	flag.StringVar(&aud, "aud", "192.168.65.254:50051", "address of the OAuth endpoint to send to operator")
}

// generateSelfSignedCert creates a local cert that can be used for our mocking of TLS
func generateSelfSignedCert() (tls.Certificate, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(365 * 24 * time.Hour) // 1 year

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).SetInt64(1<<62))
	if err != nil {
		return tls.Certificate{}, err
	}
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Illumio-CloudSecure"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	key := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	tlsCert, err := tls.X509KeyPair(cert, key)

	if err != nil {
		return tls.Certificate{}, err
	}

	return tlsCert, nil
}

// tokenAuthStreamInterceptor checks the token in the authorization header before allowing the stream to be created
func tokenAuthStreamInterceptor(expectedToken string) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// Extract the incoming metadata from the context.
		md, ok := metadata.FromIncomingContext(ss.Context())
		if !ok {
			return status.Errorf(codes.Unauthenticated, "Metadata not provided")
		}

		// Extract the token from the metadata.
		tokens := md["authorization"]
		if len(tokens) == 0 {
			return status.Errorf(codes.Unauthenticated, "Authorization token not provided")
		}

		// Validate the token.
		if tokens[0] != fmt.Sprintf("Bearer %s", expectedToken) {
			return status.Errorf(codes.Unauthenticated, "Invalid token in request")
		}
		// Call the handler if the token is valid.
		return handler(srv, ss)
	}
}

func logReceivedLogEntry(log *pb.LogEntry, logger *zap.Logger) error {
	// Decode the JSON-encoded string into a LogEntry struct
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
		ce.Write(
			//zap.String("cluster_id", ...),
			zap.Object("entry", logEntry),
		)
	}
	return nil
}

// unaryInterceptor is a generic message handler that DOES NOT check for any access token
func unaryInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	return handler(ctx, req)
}

func (f *FakeServerTestDriver) runFakeServer() {
	flag.Parse()

	logger, err := zap.NewDevelopment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to configure logger: %s", err)
		os.Exit(1)
	}
	token = "token1"
	// Example of generating a JWT with an "aud" claim
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": token,
		"aud": []string{aud},
		"exp": time.Now().Add(time.Hour * 72).Unix(),
	})
	// Just using "secret" for test signing
	mySigningKey := []byte("secret")

	// Sign and get the complete encoded token as a string
	// nosemgrep: jwt.hardcoded-jwt-key
	token, err := jwtToken.SignedString(mySigningKey)
	if err != nil {
		logger.Error("Token could not be signed with fake secret key")
	}

	listener, err := net.Listen(network, address)
	if err != nil {
		logger.Fatal("Failed to open network port", zap.Error(err))
	}
	cert, err := generateSelfSignedCert()
	if err != nil {
		logger.Fatal("Failed to generate self-signed cert", zap.Error(err))
	}
	creds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12})
	s := grpc.NewServer(grpc.Creds(creds), grpc.KeepaliveEnforcementPolicy(kaep), grpc.KeepaliveParams(kasp), grpc.StreamInterceptor(tokenAuthStreamInterceptor(token)), grpc.UnaryInterceptor(unaryInterceptor))
	pb.RegisterKubernetesInfoServiceServer(s, &server{})
	logger.Info("Server listening", zap.String("network", listener.Addr().Network()), zap.String("address", listener.Addr().String()))

	reflection.Register(s)
	go startHTTPServer(
		tokenEndpoint,
		logger,
		DefaultClientID,
		DefaultClientSecret,
		token,
		cert,
	)

	logger.Info("Token endpoint listening", zap.String("address", tokenEndpoint))
	if err = s.Serve(listener); err != nil {
		logger.Fatal("Server failed", zap.Error(err))
	}
	return FakeServerTestDriver{Logger: logger, S: s}
}

func main() {

}
