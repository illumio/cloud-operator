// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"errors"
	"regexp"
	"sync"
	"time"

	"google.golang.org/grpc"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/logging"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

type StreamType string

const (
	STREAM_NETWORK_FLOWS = StreamType("network_flows")
	STREAM_RESOURCES     = StreamType("resources")
	STREAM_LOGS          = StreamType("logs")
	STREAM_CONFIGURATION = StreamType("configuration")
)

type streamClient struct {
	ciliumNamespaces          []string
	conn                      *grpc.ClientConn
	client                    pb.KubernetesInfoServiceClient
	falcoEventChan            chan string
	ipfixCollectorPort        string
	disableNetworkFlowsCilium bool
	tlsAuthProperties         tls.AuthProperties
	flowCollector             pb.FlowCollector
	logStream                 logging.LogStream
	networkFlowsStream        NetworkFlowsStream
	resourceStream            ResourceStream
	configStream              ConfigStream
}

type deadlockDetector struct {
	mutex               sync.RWMutex
	processingResources bool
	timeStarted         time.Time
}

type streamManager struct {
	bufferedGrpcSyncer *logging.BufferedGrpcWriteSyncer
	streamClient       *streamClient
	FlowCache          *FlowCache
	verboseDebugging   bool
	stats              *StreamStats
	k8sClient          KubernetesClient
	clock              Clock
}

type KeepalivePeriods struct {
	Configuration          time.Duration
	KubernetesNetworkFlows time.Duration
	KubernetesResources    time.Duration
	Logs                   time.Duration
}

type StreamSuccessPeriods struct {
	Auth    time.Duration
	Connect time.Duration
}

type watcherInfo struct {
	resource        string
	apiGroup        string
	resourceVersion string
}

type EnvironmentConfig struct {
	// Namespaces of Cilium.
	CiliumNamespaces []string
	// K8s cluster secret name.
	ClusterCreds string
	// HTTP Proxy URL
	HttpsProxy string
	// Port for the IPFIX collector
	IPFIXCollectorPort string
	// KeepalivePeriods specifies the period (minus jitter) between two keepalives sent on each stream
	KeepalivePeriods KeepalivePeriods
	// Client ID for onboarding. "" if not specified, i.e. if the operator is not meant to onboard itself.
	OnboardingClientID string
	// Client secret for onboarding. "" if not specified, i.e. if the operator is not meant to onboard itself.
	OnboardingClientSecret string
	// URL of the onboarding endpoint.
	OnboardingEndpoint string
	// Namespace of OVN-Kubernetes
	OVNKNamespace string
	// PodNamespace is the namespace where the cloud-operator is deployed
	PodNamespace string
	// StatsLogPeriod is the period between log entries containing stream stats.
	// Set to 0 to disable stats logging.
	StatsLogPeriod time.Duration
	// How long must a stream be in a state for our exponentialBackoff function to
	// consider it a success.
	StreamSuccessPeriods StreamSuccessPeriods
	// Whether to skip TLS certificate verification when starting a stream.
	TlsSkipVerify bool
	// URL of the token endpoint.
	TokenEndpoint string
	// Whether to enable verbose debugging.
	VerboseDebugging bool
}

var resources = []string{
	"cronjobs",
	"customresourcedefinitions",
	"daemonsets",
	"deployments",
	"endpoints",
	"gateways",
	"gatewayclasses",
	"httproutes",
	"ingresses",
	"ingressclasses",
	"jobs",
	"namespaces",
	"networkpolicies",
	"nodes",
	"pods",
	"replicasets",
	"replicationcontrollers",
	"serviceaccounts",
	"services",
	"statefulsets",
}

var resourceAPIGroupMap = make(map[string]string)

var dd = &deadlockDetector{}
var ErrStopRetries = errors.New("stop retries")
var falcoPort = ":5000"
var reIllumioTraffic *regexp.Regexp

func init() {
	// Extract the relevant part of the output string
	reIllumioTraffic = regexp.MustCompile(`\((.*?)\)`)
}
