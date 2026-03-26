// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	"errors"
	"sync"
	"time"

	"google.golang.org/grpc"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/k8sclient"
	"github.com/illumio/cloud-operator/internal/controller/logging"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

type Type string

const (
	TypeNetworkFlows  = Type("network_flows")
	TypeResources     = Type("resources")
	TypeLogs          = Type("logs")
	TypeConfiguration = Type("configuration")
)

// Client holds the gRPC connection and stream clients.
type Client struct {
	CiliumNamespaces             []string
	Conn                         *grpc.ClientConn
	GrpcClient                   pb.KubernetesInfoServiceClient
	FalcoEventChan               chan string
	IPFIXCollectorPort           string
	DisableNetworkFlowsCilium    bool
	TlsAuthProperties            tls.AuthProperties
	FlowCollector                pb.FlowCollector
	LogStream                    logging.LogStream
	KubernetesNetworkFlowsStream KubernetesNetworkFlowsStream
	KubernetesResourcesStream    KubernetesResourcesStream
	ConfigurationStream          ConfigurationStream
}

// Manager orchestrates all stream operations.
type Manager struct {
	BufferedGrpcSyncer *logging.BufferedGrpcWriteSyncer
	Client             *Client
	FlowCache          *FlowCache
	VerboseDebugging   bool
	Stats              *Stats
	K8sClient          k8sclient.Client
}

// KeepalivePeriods specifies the period between keepalives for each stream type.
type KeepalivePeriods struct {
	Configuration          time.Duration
	KubernetesNetworkFlows time.Duration
	KubernetesResources    time.Duration
	Logs                   time.Duration
}

// SuccessPeriods defines how long a stream must be active to be considered successful.
type SuccessPeriods struct {
	Auth    time.Duration
	Connect time.Duration
}

// EnvironmentConfig holds all configuration from environment variables.
type EnvironmentConfig struct {
	CiliumNamespaces       []string
	ClusterCreds           string
	HttpsProxy             string
	IPFIXCollectorPort     string
	KeepalivePeriods       KeepalivePeriods
	OnboardingClientID     string
	OnboardingClientSecret string
	OnboardingEndpoint     string
	OVNKNamespace          string
	PodNamespace           string
	StatsLogPeriod         time.Duration
	SuccessPeriods         SuccessPeriods
	TlsSkipVerify          bool
	TokenEndpoint          string
	VerboseDebugging       bool
}

type deadlockDetector struct {
	mutex               sync.RWMutex
	processingResources bool
	timeStarted         time.Time
}

var dd = &deadlockDetector{}

// ErrStopRetries signals that retries should stop.
var ErrStopRetries = errors.New("stop retries")

// SetProcessingResources updates the deadlock detector state.
func SetProcessingResources(processing bool) {
	dd.mutex.Lock()
	defer dd.mutex.Unlock()

	dd.processingResources = processing
	if processing {
		dd.timeStarted = time.Now()
	}
}

// FalcoPort is the port for the Falco HTTP server.
const FalcoPort = ":5000"
