// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	cryptotls "crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"strconv"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	goldmanepb "github.com/illumio/cloud-operator/api/illumio/cloud/goldmane/v1"
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

const (
	// goldmaneServiceName is the name of the Goldmane service in Kubernetes.
	goldmaneServiceName = "goldmane"
	// goldmaneServicePort is the port of the Goldmane service.
	goldmaneServicePort = 7443
	// goldmaneMTLSSecretName is the name of the secret containing the mTLS certificates.
	goldmaneMTLSSecretName = "goldmane-key-pair"
)

var (
	// ErrGoldmaneNotFound indicates that the Goldmane service was not found in the cluster.
	ErrGoldmaneNotFound = errors.New("goldmane service not found")
)

// CalicoFlowCollector collects flows from Calico Goldmane running in this cluster.
type CalicoFlowCollector struct {
	logger *zap.Logger
	client goldmanepb.FlowsClient
}

// newCalicoFlowCollector connects to Calico Goldmane, sets up a Flows client,
// and returns a new Collector using it.
func newCalicoFlowCollector(ctx context.Context, logger *zap.Logger, calicoNamespace string) (*CalicoFlowCollector, error) {
	clientset, err := NewClientSet()
	if err != nil {
		return nil, fmt.Errorf("failed to create new client set: %w", err)
	}

	// Step 1: Discover Goldmane service
	goldmaneAddress, err := discoverGoldmane(ctx, logger, calicoNamespace, clientset)
	if err != nil {
		return nil, fmt.Errorf("failed to discover Goldmane: %w", err)
	}

	// Step 2: Get TLS config from secret
	tlsConfig, err := getGoldmaneTLSConfig(ctx, clientset, logger, calicoNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get Goldmane TLS config: %w", err)
	}

	// Step 3: Connect to Goldmane
	conn, err := connectToGoldmane(ctx, logger, goldmaneAddress, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Goldmane: %w", err)
	}

	logger.Info("Successfully connected to Calico Goldmane",
		zap.String("address", goldmaneAddress))

	flowsClient := goldmanepb.NewFlowsClient(conn)

	return &CalicoFlowCollector{logger: logger, client: flowsClient}, nil
}

// discoverGoldmane discovers the Goldmane service in the given namespace.
func discoverGoldmane(ctx context.Context, logger *zap.Logger, calicoNamespace string, clientset *kubernetes.Clientset) (string, error) {
	logger.Debug("Discovering Goldmane service", zap.String("namespace", calicoNamespace))

	service, err := clientset.CoreV1().Services(calicoNamespace).Get(ctx, goldmaneServiceName, metav1.GetOptions{})
	if err != nil {
		logger.Debug("Goldmane service not found", zap.String("namespace", calicoNamespace), zap.Error(err))

		return "", ErrGoldmaneNotFound
	}

	// Construct the service address
	address := fmt.Sprintf("%s.%s.svc:%d", service.Name, service.Namespace, goldmaneServicePort)
	logger.Debug("Goldmane service discovered", zap.String("address", address))

	return address, nil
}

// getGoldmaneTLSConfig retrieves the TLS configuration from the Goldmane secret.
func getGoldmaneTLSConfig(ctx context.Context, clientset *kubernetes.Clientset, logger *zap.Logger, calicoNamespace string) (*cryptotls.Config, error) {
	logger.Debug("Getting Goldmane TLS config", zap.String("namespace", calicoNamespace))

	secret, err := clientset.CoreV1().Secrets(calicoNamespace).Get(ctx, goldmaneMTLSSecretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get Goldmane secret: %w", err)
	}

	// Extract certificate and key from secret
	certPEM, ok := secret.Data["tls.crt"]
	if !ok {
		return nil, fmt.Errorf("tls.crt not found in secret %s", goldmaneMTLSSecretName)
	}

	keyPEM, ok := secret.Data["tls.key"]
	if !ok {
		return nil, fmt.Errorf("tls.key not found in secret %s", goldmaneMTLSSecretName)
	}

	// Parse the certificate and key
	cert, err := cryptotls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate and key: %w", err)
	}

	// Create a CA cert pool using the same certificate as the CA
	// (Goldmane uses self-signed certificates)
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(certPEM) {
		return nil, errors.New("failed to add CA certificate to pool")
	}

	tlsConfig := &cryptotls.Config{
		Certificates: []cryptotls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   cryptotls.VersionTLS12,
	}

	return tlsConfig, nil
}

// connectToGoldmane establishes a gRPC connection to the Goldmane service.
func connectToGoldmane(_ context.Context, logger *zap.Logger, address string, tlsConfig *cryptotls.Config) (*grpc.ClientConn, error) {
	logger.Debug("Connecting to Goldmane", zap.String("address", address))

	creds := credentials.NewTLS(tlsConfig)

	conn, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(creds),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to dial Goldmane: %w", err)
	}

	return conn, nil
}

// exportCalicoFlows streams flows from Goldmane and sends them to the flow cache.
func (c *CalicoFlowCollector) exportCalicoFlows(ctx context.Context, sm *streamManager) error {
	req := &goldmanepb.StreamRequest{}

	stream, err := c.client.Stream(ctx, req)
	if err != nil {
		c.logger.Error("Error starting Goldmane flow stream", zap.Error(err))

		return err
	}

	for {
		select {
		case <-ctx.Done():
			c.logger.Warn("Context cancelled, stopping Calico flow export")

			return ctx.Err()
		default:
		}

		resp, err := stream.Recv()
		if err != nil {
			c.logger.Warn("Failed to receive flow from Goldmane stream", zap.Error(err))

			return err
		}

		calicoFlow := convertGoldmaneFlow(resp)
		if calicoFlow == nil {
			continue
		}

		err = sm.FlowCache.CacheFlow(ctx, calicoFlow)
		if err != nil {
			c.logger.Error("Failed to cache Calico flow", zap.Error(err))

			return err
		}
	}
}

// convertGoldmaneFlow converts a Goldmane StreamResponse to a CalicoFlow.
func convertGoldmaneFlow(resp *goldmanepb.StreamResponse) *pb.CalicoFlow {
	if resp == nil || resp.GetFlow() == nil || resp.GetFlow().GetKey() == nil {
		return nil
	}

	flow := resp.GetFlow()
	key := flow.GetKey()

	// Parse timestamps (Goldmane returns Unix timestamps as strings)
	startTime := parseUnixTimestamp(flow.GetStartTime())
	endTime := parseUnixTimestamp(flow.GetEndTime())

	// Parse destination port
	destPort := parseUint32(key.GetDestPort())
	destServicePort := parseUint32(key.GetDestServicePort())

	// Parse statistics
	packetsIn := parseUint64(flow.GetPacketsIn())
	packetsOut := parseUint64(flow.GetPacketsOut())
	bytesIn := parseUint64(flow.GetBytesIn())
	bytesOut := parseUint64(flow.GetBytesOut())
	numConnectionsStarted := parseUint64(flow.GetNumConnectionsStarted())
	numConnectionsCompleted := parseUint64(flow.GetNumConnectionsCompleted())
	numConnectionsLive := parseUint64(flow.GetNumConnectionsLive())

	// Convert policies
	var policies *pb.CalicoPolicies
	if key.GetPolicies() != nil {
		policies = convertGoldmanePolicies(key.GetPolicies())
	}

	calicoFlow := &pb.CalicoFlow{
		StartTime:               startTime,
		EndTime:                 endTime,
		SourceName:              key.GetSourceName(),
		SourceNamespace:         key.GetSourceNamespace(),
		SourceType:              key.GetSourceType(),
		DestName:                key.GetDestName(),
		DestNamespace:           key.GetDestNamespace(),
		DestType:                key.GetDestType(),
		DestPort:                destPort,
		DestServiceName:         key.GetDestServiceName(),
		DestServiceNamespace:    key.GetDestServiceNamespace(),
		DestServicePortName:     key.GetDestServicePortName(),
		DestServicePort:         destServicePort,
		Proto:                   key.GetProto(),
		Reporter:                key.GetReporter(),
		Action:                  key.GetAction(),
		SourceLabels:            flow.GetSourceLabels(),
		DestLabels:              flow.GetDestLabels(),
		PacketsIn:               packetsIn,
		PacketsOut:              packetsOut,
		BytesIn:                 bytesIn,
		BytesOut:                bytesOut,
		NumConnectionsStarted:   numConnectionsStarted,
		NumConnectionsCompleted: numConnectionsCompleted,
		NumConnectionsLive:      numConnectionsLive,
		Policies:                policies,
	}

	return calicoFlow
}

// convertGoldmanePolicies converts Goldmane Policies to CalicoPolices.
func convertGoldmanePolicies(policies *goldmanepb.Policies) *pb.CalicoPolicies {
	if policies == nil {
		return nil
	}

	enforcedPolicies := make([]*pb.CalicoPolicy, 0, len(policies.GetEnforcedPolicies()))
	for _, p := range policies.GetEnforcedPolicies() {
		enforcedPolicies = append(enforcedPolicies, &pb.CalicoPolicy{
			Kind:      p.GetKind(),
			Namespace: p.GetNamespace(),
			Name:      p.GetName(),
			Tier:      p.GetTier(),
			Action:    p.GetAction(),
		})
	}

	pendingPolicies := make([]*pb.CalicoPolicy, 0, len(policies.GetPendingPolicies()))
	for _, p := range policies.GetPendingPolicies() {
		pendingPolicies = append(pendingPolicies, &pb.CalicoPolicy{
			Kind:      p.GetKind(),
			Namespace: p.GetNamespace(),
			Name:      p.GetName(),
			Tier:      p.GetTier(),
			Action:    p.GetAction(),
		})
	}

	return &pb.CalicoPolicies{
		EnforcedPolicies: enforcedPolicies,
		PendingPolicies:  pendingPolicies,
	}
}

// parseUnixTimestamp parses a Unix timestamp string to a protobuf Timestamp.
func parseUnixTimestamp(s string) *timestamppb.Timestamp {
	if s == "" {
		return nil
	}

	seconds, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}

	return &timestamppb.Timestamp{Seconds: seconds}
}

// parseUint32 parses a string to uint32.
func parseUint32(s string) uint32 {
	if s == "" {
		return 0
	}

	val, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0
	}

	return uint32(val)
}

// parseUint64 parses a string to uint64.
func parseUint64(s string) uint64 {
	if s == "" {
		return 0
	}

	val, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}

	return val
}
