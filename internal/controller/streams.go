// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"errors"
	"math/rand"
	"net"
	"net/http"
	"regexp"
	"sync"
	"time"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

type streamClient struct {
	ciliumNamespace           string
	conn                      *grpc.ClientConn
	client                    pb.KubernetesInfoServiceClient
	disableNetworkFlowsCilium bool
	falcoEventChan            chan string
	logStream                 pb.KubernetesInfoService_SendLogsClient
	networkFlowsStream        pb.KubernetesInfoService_SendKubernetesNetworkFlowsClient
	resourceStream            pb.KubernetesInfoService_SendKubernetesResourcesClient
}

type deadlockDetector struct {
	mutex               sync.RWMutex
	processingResources bool
	timeStarted         time.Time
}

type streamManager struct {
	bufferedGrpcSyncer *BufferedGrpcWriteSyncer
	logger             *zap.SugaredLogger
	streamClient       *streamClient
}

type EnvironmentConfig struct {
	// Namspace of Cilium.
	CiliumNamespace string
	// K8s cluster secret name.
	ClusterCreds string
	// Client ID for onboarding. "" if not specified, i.e. if the operator is not meant to onboard itself.
	OnboardingClientId string
	// Client secret for onboarding. "" if not specified, i.e. if the operator is not meant to onboard itself.
	OnboardingClientSecret string
	// URL of the onboarding endpoint.
	OnboardingEndpoint string
	// URL of the token endpoint.
	TokenEndpoint string
	// Whether to skip TLS certificate verification when starting a stream.
	TlsSkipVerify bool
}

var resourceAPIGroupMap = map[string]string{
	"cronjobs":                  "batch",
	"customresourcedefinitions": "apiextensions.k8s.io",
	"daemonsets":                "apps",
	"deployments":               "apps",
	"endpoints":                 "",
	"gateways":                  "gateway.networking.k8s.io",
	"gatewayclasses":            "gateway.networking.k8s.io",
	"httproutes":                "gateway.networking.k8s.io",
	"ingresses":                 "networking.k8s.io",
	"ingressclasses":            "networking.k8s.io",
	"jobs":                      "batch",
	"networkpolicies":           "networking.k8s.io",
	"nodes":                     "",
	"pods":                      "",
	"replicasets":               "apps",
	"replicationcontrollers":    "",
	"serviceaccounts":           "",
	"services":                  "",
	"statefulsets":              "apps",
}

var dd = &deadlockDetector{}
var ErrStopRetries = errors.New("stop retries")
var ErrFalcoEventIsNotFlow = errors.New("ignoring falco event, not a network flow")
var falcoPort = ":5000"
var reIllumioTraffic *regexp.Regexp
var reParsePodNetworkInfo *regexp.Regexp

func init() {
	// Extract the relevant part of the output string
	reIllumioTraffic = regexp.MustCompile(`\((.*?)\)`)
	reParsePodNetworkInfo = regexp.MustCompile(`\b(\w+)=([^\s)]+)`)

}

// ServerIsHealthy checks if a deadlock has occured within the threaded resource listing process.
func ServerIsHealthy() bool {
	dd.mutex.RLock()
	defer dd.mutex.RUnlock()
	if dd.processingResources && time.Since(dd.timeStarted) > 5*time.Minute {
		return false
	}
	return true
}

// StreamResources handles the resource stream.
func (sm *streamManager) StreamResources(ctx context.Context, cancel context.CancelFunc) error {
	defer func() {
		dd.processingResources = false
	}()
	clusterConfig, err := rest.InClusterConfig()
	if err != nil {
		sm.logger.Errorw("Error getting in-cluster config", "error", err)
		return err
	}
	var allResourcesSnapshotted sync.WaitGroup
	var snapshotCompleted sync.WaitGroup
	// Create a dynamic client
	dynamicClient, err := dynamic.NewForConfig(clusterConfig)
	if err != nil {
		sm.logger.Errorw("Error creating dynamic client", "error", err)
		return err
	}

	clientset, err := NewClientSet()
	if err != nil {
		sm.logger.Errorw("Failed to create clientset", "error", err)
		return err
	}
	apiGroups, err := clientset.Discovery().ServerGroups()
	if err != nil {
		sm.logger.Error("Failed to discover API groups", "error", err)
	}
	foundGatewayAPIGroup := false

	// Check if the "gateway.networking.k8s.io" API group is not available, if it is not delete those resources and groups
	for _, group := range apiGroups.Groups {
		if group.Name == "gateway.networking.k8s.io" {
			foundGatewayAPIGroup = true
			break
		}
	}

	// If the "gateway.networking.k8s.io" API group is not found, remove the resources
	if !foundGatewayAPIGroup {
		gatewayResources := []string{"gateways", "gatewayclasses", "httproutes"}
		for _, resource := range gatewayResources {
			delete(resourceAPIGroupMap, resource)
		}
	}

	snapshotCompleted.Add(1)
	dd.mutex.Lock()
	dd.timeStarted = time.Now()
	dd.processingResources = true
	dd.mutex.Unlock()
	resourceLister := &ResourceManager{
		clientset:     clientset,
		logger:        sm.logger,
		dynamicClient: dynamicClient,
		streamManager: sm,
	}
	err = sendClusterMetadata(ctx, sm)
	if err != nil {
		sm.logger.Errorw("Failed to send cluster metadata", "error", err)
		return err
	}
	for resource, apiGroup := range resourceAPIGroupMap {
		allResourcesSnapshotted.Add(1)
		go resourceLister.DyanmicListAndWatchResources(ctx, cancel, resource, apiGroup, &allResourcesSnapshotted, &snapshotCompleted)
	}
	allResourcesSnapshotted.Wait()
	err = sendResourceSnapshotComplete(sm)
	dd.timeStarted = time.Now()
	dd.mutex.Lock()
	dd.processingResources = false
	dd.mutex.Unlock()
	if err != nil {
		sm.logger.Errorw("Failed to send resource snapshot complete", "error", err)
		return err
	}
	snapshotCompleted.Done()

	<-ctx.Done()
	return ctx.Err()
}

// StreamLogs handles the log stream.
func (sm *streamManager) StreamLogs(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		errCh <- sm.bufferedGrpcSyncer.ListenToLogStream()
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		if err != nil {
			return err
		}
	}
	return nil
}

// StreamCiliumNetworkFlows handles the cilium network flow stream.
func (sm *streamManager) StreamCiliumNetworkFlows(ctx context.Context, ciliumNamespace string) error {
	// TODO: Add logic for a discoveribility function to decide which CNI to use.
	ciliumFlowCollector, err := newCiliumFlowCollector(ctx, sm.logger, ciliumNamespace)
	if err != nil {
		sm.logger.Infow("Failed to initialize Cilium Hubble Relay flow collector; disabling flow collector", "error", err)
		return err
	}
	if ciliumFlowCollector != nil {
		for {
			err = ciliumFlowCollector.exportCiliumFlows(ctx, *sm)
			if err != nil {
				sm.logger.Warnw("Failed to collect and export flows from Cilium Hubble Relay", "error", err)
				sm.streamClient.disableNetworkFlowsCilium = true
				return err
			}
		}
	}
	return nil
}

// StreamFalcoNetworkFlows handles the falco network flow stream.
func (sm *streamManager) StreamFalcoNetworkFlows(ctx context.Context) error {
	for {
		falcoFlow := <-sm.streamClient.falcoEventChan
		if filterIllumioTraffic(falcoFlow) {
			// Extract the relevant part of the output string
			match := reIllumioTraffic.FindStringSubmatch(falcoFlow)
			if len(match) < 2 {
				return nil
			}

			convertedFalcoFlow, err := parsePodNetworkInfo(match[1])
			if errors.Is(err, ErrFalcoEventIsNotFlow) {
				// If the event can't be parsed, consider that it's not a flow event and just ignore it.
				return nil
			} else if err != nil {
				sm.logger.Errorw("Failed to parse Falco event into flow", "error", err)
				return err
			}
			err = sendNetworkFlowRequest(sm, convertedFalcoFlow)
			if err != nil {
				sm.logger.Errorw("Failed to send Falco flow", "errors", err)
				return err
			}
		} else {
			continue
		}
	}
}

// connectAndStreamCiliumNetworkFlows creates networkFlowsStream client and begins the streaming of network flows.
func connectAndStreamCiliumNetworkFlows(logger *zap.SugaredLogger, sm *streamManager) error {
	ciliumCtx, ciliumCancel := context.WithCancel(context.Background())
	defer ciliumCancel()

	sendCiliumNetworkFlowsStream, err := sm.streamClient.client.SendKubernetesNetworkFlows(ciliumCtx)
	if err != nil {
		logger.Errorw("Failed to connect to server", "error", err)
		return err
	}

	sm.streamClient.networkFlowsStream = sendCiliumNetworkFlowsStream

	err = sm.StreamCiliumNetworkFlows(ciliumCtx, sm.streamClient.ciliumNamespace)
	if err != nil {
		if errors.Is(err, ErrHubbleNotFound) || errors.Is(err, ErrNoPortsAvailable) {
			logger.Warnw("Disabling Cilium flow collection", "error", err)
			return ErrStopRetries
		}
		return err
	}

	return nil
}

// connectAndStreamFalcoNetworkFlows creates networkFlowsStream client and begins the streaming of network flows.
func connectAndStreamFalcoNetworkFlows(logger *zap.SugaredLogger, sm *streamManager) error {
	falcoCtx, falcoCancel := context.WithCancel(context.Background())
	defer falcoCancel()
	sendFalcoNetworkFlows, err := sm.streamClient.client.SendKubernetesNetworkFlows(falcoCtx)
	if err != nil {
		logger.Errorw("Failed to connect to server", "error", err)
		return err
	}

	sm.streamClient.networkFlowsStream = sendFalcoNetworkFlows

	err = sm.StreamFalcoNetworkFlows(falcoCtx)
	if err != nil {
		logger.Errorw("Failed to stream Falco network flows", "error", err)
		return err
	}

	return nil
}

// connectAndStreamResources creates resourceStream client and begins the streaming of resources.
func connectAndStreamResources(logger *zap.SugaredLogger, sm *streamManager) error {
	resourceCtx, resourceCancel := context.WithCancel(context.Background())
	defer resourceCancel()

	SendKubernetesResourcesStream, err := sm.streamClient.client.SendKubernetesResources(resourceCtx)
	if err != nil {
		logger.Errorw("Failed to connect to server", "error", err)
		return err
	}

	sm.streamClient.resourceStream = SendKubernetesResourcesStream

	err = sm.StreamResources(resourceCtx, resourceCancel)
	if err != nil {
		logger.Errorw("Failed to bootup and stream resources", "error", err)
		return err
	}

	return nil
}

// connectAndStreamLogs creates sendLogs client and begins the streaming of logs.
func connectAndStreamLogs(logger *zap.SugaredLogger, sm *streamManager) error {
	logCtx, logCancel := context.WithCancel(context.Background())
	defer logCancel()

	SendLogsStream, err := sm.streamClient.client.SendLogs(logCtx)
	if err != nil {
		logger.Errorw("Failed to connect to server", "error", err)
		return err
	}

	sm.streamClient.logStream = SendLogsStream
	sm.bufferedGrpcSyncer.UpdateClient(sm.streamClient.logStream, sm.streamClient.conn)

	err = sm.StreamLogs(logCtx)
	if err != nil {
		logger.Errorw("Failed to bootup and stream logs", "error", err)
		return err
	}

	return nil
}

// Generic function to manage any stream with backoff and reconnection logic.
func manageStream(logger *zap.SugaredLogger, connectAndStream func(*zap.SugaredLogger, *streamManager) error, sm *streamManager, done chan struct{}) {
	defer close(done)
	const (
		initialBackoff       = 1 * time.Second
		maxBackoff           = 1 * time.Minute
		maxJitterPct         = 0.20
		resetPeriod          = 10 * time.Minute
		severeErrorThreshold = 10 // Define what constitutes a severe error.
	)

	var (
		backoff             = initialBackoff
		consecutiveFailures = 0
	)

	resetTimer := time.NewTimer(resetPeriod)
	sleepTimer := time.NewTimer(initialBackoff) // Start with an initial backoff interval
	defer sleepTimer.Stop()
	for {
		select {
		case <-resetTimer.C:
			consecutiveFailures = 0
			backoff = initialBackoff
			resetTimer.Reset(resetPeriod)
		case <-sleepTimer.C:
			err := connectAndStream(logger, sm)
			if err != nil {
				if errors.Is(err, ErrStopRetries) {
					logger.Info("Stopping retries for this stream as instructed.")
					return
				}
				logger.Errorw("Failed to establish stream connection; will retry", "error", err)
				switch {
				case consecutiveFailures == 0:
					resetTimer.Reset(resetPeriod)
				case consecutiveFailures >= severeErrorThreshold:
					return
				}

				consecutiveFailures++

				jitterPct := rand.Float64() * maxJitterPct // [0, maxJitterPct)
				sleep := time.Duration(float64(backoff) * (1. - jitterPct))

				if sleep < initialBackoff {
					sleep = initialBackoff
				}
				if sleep > maxBackoff {
					sleep = maxBackoff
				}

				logger.Infow("Sleeping before retrying connection", "backoff", sleep)
				sleepTimer.Reset(sleep) // Reset sleep timer with calculated backoff delay

				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			} else {
				consecutiveFailures = 0
				backoff = initialBackoff
				// Reset sleep timer back to initial backoff interval
				sleepTimer.Reset(initialBackoff)
				resetTimer.Reset(resetPeriod)
			}
		}
	}
}

// ConnectStreams will continue to reboot and restart the main operations within the operator if any disconnects or errors occur.
func ConnectStreams(ctx context.Context, logger *zap.SugaredLogger, envMap EnvironmentConfig, bufferedGrpcSyncer *BufferedGrpcWriteSyncer) {
	// Falco channels communicate news events between http server and our network flows strea,
	falcoEventChan := make(chan string)
	http.HandleFunc("/", NewFalcoEventHandler(falcoEventChan))
	// Start our falco server and have it passively listen, if it fails, try to just restart it.
	go func() {
		for {
			// Create a custom listener, this listener has SO_REUSEADDR option set by default
			listener, err := net.Listen("tcp", falcoPort)
			if err != nil {
				logger.Fatalf("Failed to listen on %s: %v", falcoPort, err)
			}

			// Create the HTTP server
			falcoEvent := &http.Server{Addr: falcoPort}

			logger.Infof("Falco server listening on %s", falcoPort)
			err = falcoEvent.Serve(listener)
			if err != nil && err != http.ErrServerClosed {
				logger.Errorf("Falco server failed, restarting in 5 seconds... Error: %v", err)
				time.Sleep(5 * time.Second)
			}
		}
	}()
	// Timer channel for 5 seconds
	timer := time.After(5 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer:
			authConn, client, err := NewAuthenticatedConnection(ctx, logger, envMap)
			if err != nil {
				logger.Errorw("Failed to establish initial connection; will retry", "error", err)
				continue
			}

			streamClient := &streamClient{
				conn:            authConn,
				client:          client,
				ciliumNamespace: envMap.CiliumNamespace,
				falcoEventChan:  falcoEventChan,
			}

			sm := &streamManager{
				streamClient:       streamClient,
				logger:             logger,
				bufferedGrpcSyncer: bufferedGrpcSyncer,
			}

			resourceDone := make(chan struct{})
			logDone := make(chan struct{})
			falcoDone := make(chan struct{})
			var ciliumDone chan struct{}
			sm.bufferedGrpcSyncer.done = logDone

			go manageStream(logger, connectAndStreamResources, sm, resourceDone)
			go manageStream(logger, connectAndStreamLogs, sm, logDone)
			// Only start network flows stream if not disabled
			if !sm.streamClient.disableNetworkFlowsCilium {
				ciliumDone = make(chan struct{})
				go manageStream(logger, connectAndStreamCiliumNetworkFlows, sm, ciliumDone)
				if !sm.streamClient.disableNetworkFlowsCilium {
					falcoDone = nil
				}
			}
			if !sm.streamClient.disableNetworkFlowsCilium {
				ciliumDone = nil
				go manageStream(logger, connectAndStreamFalcoNetworkFlows, sm, falcoDone)
			}
			select {
			case <-ciliumDone:
			case <-falcoDone:
			case <-resourceDone:
			case <-logDone:
			}

			logger.Warn("One or more streams have been closed; closing and reopening the connection to CloudSecure")
		}
	}
}

// NewAuthenticatedConnection gets a valid token and creats a connection to CloudSecure.
func NewAuthenticatedConnection(ctx context.Context, logger *zap.SugaredLogger, envMap EnvironmentConfig) (*grpc.ClientConn, pb.KubernetesInfoServiceClient, error) {
	authn := Authenticator{Logger: logger}

	clientID, clientSecret, err := authn.ReadCredentialsK8sSecrets(ctx, envMap.ClusterCreds)
	if err != nil {
		logger.Errorw("Could not read K8s credentials", "error", err)
	}

	if clientID == "" && clientSecret == "" {
		OnboardingCredentials, err := authn.GetOnboardingCredentials(ctx, envMap.OnboardingClientId, envMap.OnboardingClientSecret)
		if err != nil {
			logger.Errorw("Failed to get onboarding credentials", "error", err)
		}
		responseData, err := Onboard(ctx, envMap.TlsSkipVerify, envMap.OnboardingEndpoint, OnboardingCredentials, logger)
		if err != nil {
			logger.Errorw("Failed to register cluster", "error", err)
		}
		err = authn.WriteK8sSecret(ctx, responseData, envMap.ClusterCreds)
		time.Sleep(1 * time.Second)
		if err != nil {
			logger.Errorw("Failed to write secret to Kubernetes", "error", err)
		}
		clientID, clientSecret, err = authn.ReadCredentialsK8sSecrets(ctx, envMap.ClusterCreds)
		if err != nil {
			logger.Errorw("Could not read K8s credentials", "error", err)
		}
	}

	conn, err := SetUpOAuthConnection(ctx, logger, envMap.TokenEndpoint, envMap.TlsSkipVerify, clientID, clientSecret)
	if err != nil {
		logger.Errorw("Failed to set up an OAuth connection", "error", err)
		return nil, nil, err
	}

	client := pb.NewKubernetesInfoServiceClient(conn)

	return conn, client, err
}
