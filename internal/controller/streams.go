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
	logger             *zap.Logger
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
	"namespaces":                "",
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
var ErrFalcoIncompleteL3Flow = errors.New("ignoring incomplete falco l3 network flow")
var ErrFalcoIncompleteL4Flow = errors.New("ignoring incomplete falco l4 network flow")
var ErrFalcoInvalidPort = errors.New("ignoring incomplete falco flow due to bad ports")
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
		sm.logger.Error("Error getting in-cluster config", zap.Error(err))
		return err
	}
	var allResourcesSnapshotted sync.WaitGroup
	var snapshotCompleted sync.WaitGroup
	// Create a dynamic client
	dynamicClient, err := dynamic.NewForConfig(clusterConfig)
	if err != nil {
		sm.logger.Error("Error creating dynamic client", zap.Error(err))
		return err
	}

	clientset, err := NewClientSet()
	if err != nil {
		sm.logger.Error("Failed to create clientset", zap.Error(err))
		return err
	}
	apiGroups, err := clientset.Discovery().ServerGroups()
	if err != nil {
		sm.logger.Error("Failed to discover API groups", zap.Error(err))
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
		sm.logger.Error("Failed to send cluster metadata", zap.Error(err))
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
		sm.logger.Error("Failed to send resource snapshot complete", zap.Error(err))
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

// findHubbleRelay returns a *CiliumFlowCollector if hubble relay is found in the given namespace
func (sm *streamManager) findHubbleRelay(ctx context.Context, ciliumNamespace string) *CiliumFlowCollector {
	// TODO: Add logic for a discoveribility function to decide which CNI to use.
	ciliumFlowCollector, err := newCiliumFlowCollector(ctx, sm.logger, ciliumNamespace)
	if err != nil {
		return nil
	}
	return ciliumFlowCollector
}

// StreamCiliumNetworkFlows handles the cilium network flow stream.
func (sm *streamManager) StreamCiliumNetworkFlows(ctx context.Context, ciliumNamespace string) error {
	// TODO: Add logic for a discoveribility function to decide which CNI to use.
	ciliumFlowCollector := sm.findHubbleRelay(ctx, ciliumNamespace)
	if ciliumFlowCollector == nil {
		sm.logger.Info("Failed to initialize Cilium Hubble Relay flow collector; disabling flow collector")
		return errors.New("hubble relay cannot be found")
	} else {
		for {
			err := ciliumFlowCollector.exportCiliumFlows(ctx, *sm)
			if err != nil {
				sm.logger.Warn("Failed to collect and export flows from Cilium Hubble Relay", zap.Error(err))
				sm.streamClient.disableNetworkFlowsCilium = true
				return err
			}
		}
	}
}

// StreamFalcoNetworkFlows handles the falco network flow stream.
func (sm *streamManager) StreamFalcoNetworkFlows(ctx context.Context) error {
	for {
		falcoFlow := <-sm.streamClient.falcoEventChan
		if filterIllumioTraffic(falcoFlow) {
			// Extract the relevant part of the output string
			match := reIllumioTraffic.FindStringSubmatch(falcoFlow)
			if len(match) < 2 {
				continue
			}

			convertedFalcoFlow, err := parsePodNetworkInfo(match[1])
			if convertedFalcoFlow == nil {
				// If the event can't be parsed, consider that it's not a flow event and just ignore it.
				// If the event has bad ports in any way ignore it.
				// If the event has an incomplete L3/L4 layer lets just ignore it.
				continue
			} else if err != nil {
				sm.logger.Error("Failed to parse Falco event into flow", zap.Error(err))
				return err
			}
			err = sendNetworkFlowRequest(sm, convertedFalcoFlow)
			if err != nil {
				sm.logger.Error("Failed to send Falco flow", zap.Error(err))
				return err
			}
		} else {
			continue
		}
	}
}

// connectAndStreamCiliumNetworkFlows creates networkFlowsStream client and begins the streaming of network flows.
func connectAndStreamCiliumNetworkFlows(logger *zap.Logger, sm *streamManager) error {
	ciliumCtx, ciliumCancel := context.WithCancel(context.Background())
	defer ciliumCancel()

	sendCiliumNetworkFlowsStream, err := sm.streamClient.client.SendKubernetesNetworkFlows(ciliumCtx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))
		return err
	}

	sm.streamClient.networkFlowsStream = sendCiliumNetworkFlowsStream

	err = sm.StreamCiliumNetworkFlows(ciliumCtx, sm.streamClient.ciliumNamespace)
	if err != nil {
		if errors.Is(err, ErrHubbleNotFound) || errors.Is(err, ErrNoPortsAvailable) {
			logger.Warn("Disabling Cilium flow collection", zap.Error(err))
			return ErrStopRetries
		}
		return err
	}

	return nil
}

// connectAndStreamFalcoNetworkFlows creates networkFlowsStream client and begins the streaming of network flows.
func connectAndStreamFalcoNetworkFlows(logger *zap.Logger, sm *streamManager) error {
	falcoCtx, falcoCancel := context.WithCancel(context.Background())
	defer falcoCancel()
	sendFalcoNetworkFlows, err := sm.streamClient.client.SendKubernetesNetworkFlows(falcoCtx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))
		return err
	}

	sm.streamClient.networkFlowsStream = sendFalcoNetworkFlows

	err = sm.StreamFalcoNetworkFlows(falcoCtx)
	if err != nil {
		logger.Error("Failed to stream Falco network flows", zap.Error(err))
		return err
	}

	return nil
}

// connectAndStreamResources creates resourceStream client and begins the streaming of resources.
func connectAndStreamResources(logger *zap.Logger, sm *streamManager) error {
	resourceCtx, resourceCancel := context.WithCancel(context.Background())
	defer resourceCancel()

	SendKubernetesResourcesStream, err := sm.streamClient.client.SendKubernetesResources(resourceCtx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))
		return err
	}

	sm.streamClient.resourceStream = SendKubernetesResourcesStream

	err = sm.StreamResources(resourceCtx, resourceCancel)
	if err != nil {
		logger.Error("Failed to bootup and stream resources", zap.Error(err))
		return err
	}

	return nil
}

// connectAndStreamLogs creates sendLogs client and begins the streaming of logs.
func connectAndStreamLogs(logger *zap.Logger, sm *streamManager) error {
	logCtx, logCancel := context.WithCancel(context.Background())
	defer logCancel()

	SendLogsStream, err := sm.streamClient.client.SendLogs(logCtx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))
		return err
	}

	sm.streamClient.logStream = SendLogsStream
	sm.bufferedGrpcSyncer.UpdateClient(sm.streamClient.logStream, sm.streamClient.conn)

	err = sm.StreamLogs(logCtx)
	if err != nil {
		logger.Error("Failed to bootup and stream logs", zap.Error(err))
		return err
	}

	return nil
}

// connectAndStreamConfigurationUpdates creates a configuration update stream client and listens for configuration changes.
func connectAndStreamConfigurationUpdates(logger *zap.Logger, sm *streamManager) error {
	configCtx, configCancel := context.WithCancel(context.Background())
	defer configCancel()

	logger.Info("Opening configuration update stream...")

	configStream, err := sm.streamClient.client.GetConfigurationUpdates(configCtx)
	if err != nil {
		logger.Error("Failed to open configuration update stream", zap.Error(err))
		return err
	}
	errCh := make(chan error)
	// Run the listener in a separate goroutine
	go func() {
		logger.Info("Configuration update stream listener started")

		if err := ListenToConfigurationStream(configStream, sm.bufferedGrpcSyncer); err != nil {
			logger.Error("Configuration update stream listener encountered an error", zap.Error(err))
		}
		close(errCh) //  Close channel to prevent blocking
	}()

	// Keep track of the stream and return any errors
	select {
	case <-configCtx.Done():
		logger.Warn("Configuration update stream context canceled")
		return configCtx.Err()
	case err := <-errCh:
		if err != nil {
			logger.Error("Configuration update stream failed", zap.Error(err))
			return err //Return listener error to the caller
		}
	}
	return nil
}

// Generic function to manage any stream with backoff and reconnection logic.
func manageStream(logger *zap.Logger, connectAndStream func(*zap.Logger, *streamManager) error, sm *streamManager, done chan struct{}) {
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
				logger.Error("Failed to establish stream connection; will retry", zap.Error(err))
				switch {
				case consecutiveFailures == 0:
					resetTimer.Reset(resetPeriod)
				case consecutiveFailures >= severeErrorThreshold:
					sleepTimer.Reset(resetPeriod)
					<-resetTimer.C // Wait for reset timer to reset the failure count
					consecutiveFailures = 0
					backoff = initialBackoff
					resetTimer.Reset(resetPeriod)
					continue
				}

				consecutiveFailures++

				sleep := jitterTime(backoff, maxJitterPct)
				if sleep < initialBackoff {
					sleep = initialBackoff
				}
				if sleep > maxBackoff {
					sleep = maxBackoff
				}

				logger.Info("Sleeping before retrying connection", zap.Duration("backoff", sleep))
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

// ConnectStreams will continue to reboot and restart the main operations within
// the operator if any disconnects or errors occur.
func ConnectStreams(ctx context.Context, logger *zap.Logger, envMap EnvironmentConfig, bufferedGrpcSyncer *BufferedGrpcWriteSyncer) {
	// Falco channels communicate news events between http server and our network flows strea,
	falcoEventChan := make(chan string)
	http.HandleFunc("/", NewFalcoEventHandler(falcoEventChan))

	// Start our falco server and have it passively listen, if it fails, try to just restart it.
	go func() {
		for {
			// Create a custom listener, this listener has SO_REUSEADDR option set by default
			listener, err := net.Listen("tcp", falcoPort)
			if err != nil {
				logger.Fatal("Failed to listen on Falco port", zap.String("address", falcoPort), zap.Error(err))
			}

			// Create the HTTP server
			falcoEvent := &http.Server{Addr: falcoPort}

			logger.Info("Falco server listening", zap.String("address", falcoPort))
			err = falcoEvent.Serve(listener)
			if err != nil && err != http.ErrServerClosed {
				logger.Error("Falco server failed, restarting in 5 seconds", zap.Error(err))
				// Giving some time before attempting to restart.....
				time.Sleep(5 * time.Second)
			}
		}
	}()

	// We want to avoid many cloud-operator instances all trying to authenticate
	// at the same time.
	//
	// To that effect, we add a 5 second sleep with 20% jitter here. That way even
	// if you onboard 100 cloud-operators at the same time, they won't all be
	// synced up. Normal network delays may do this for us, but we don't want to
	// rely on that.
	resetTimer := time.NewTimer(jitterTime(5*time.Second, 0.20))
	attempt := 0

	// The happy path blocks inside the for loop.
	// The unhappy path exits the for loop and hits the top-level select.
	for {
		failureReason := ""
		attempt++
		logger.Debug("Trying to authenticate and open streams", zap.Int("attempt", attempt))

		select {
		case <-ctx.Done():
			logger.Warn("Context canceled while trying to authenticate and open streams")
			return
		case <-resetTimer.C:
			authConContext, authConContextCancel := context.WithCancel(ctx)
			authConn, client, err := NewAuthenticatedConnection(authConContext, logger, envMap)
			if err != nil {
				logger.Error("Failed to establish initial connection; will retry", zap.Error(err))
				// When we try this loop again, we wait 10 seconds with 20% jitter.
				resetTimer.Reset(jitterTime(10*time.Second, 0.20))
				authConContextCancel()
				failureReason = "Failed to establish initial connection"
				break
			}

			streamClient := &streamClient{
				conn:                      authConn,
				client:                    client,
				ciliumNamespace:           envMap.CiliumNamespace,
				disableNetworkFlowsCilium: false,
				falcoEventChan:            falcoEventChan,
			}

			sm := &streamManager{
				streamClient:       streamClient,
				logger:             logger,
				bufferedGrpcSyncer: bufferedGrpcSyncer,
			}
			ciliumFlowCollector := sm.findHubbleRelay(ctx, sm.streamClient.ciliumNamespace)
			if ciliumFlowCollector == nil {
				sm.streamClient.disableNetworkFlowsCilium = true
			} else {
				sm.streamClient.disableNetworkFlowsCilium = false
			}

			resourceDone := make(chan struct{})
			logDone := make(chan struct{})
			falcoDone := make(chan struct{})
			configDone := make(chan struct{})
			var ciliumDone chan struct{}
			sm.bufferedGrpcSyncer.done = logDone

			go manageStream(logger, connectAndStreamResources, sm, resourceDone)
			go manageStream(logger, connectAndStreamLogs, sm, logDone)
			go manageStream(logger, connectAndStreamConfigurationUpdates, sm, configDone)

			// Only start network flows stream if not disabled
			if !sm.streamClient.disableNetworkFlowsCilium {
				ciliumDone = make(chan struct{})
				go manageStream(logger, connectAndStreamCiliumNetworkFlows, sm, ciliumDone)
				if !sm.streamClient.disableNetworkFlowsCilium {
					falcoDone = nil
				}
			}
			if sm.streamClient.disableNetworkFlowsCilium {
				ciliumDone = nil
				go manageStream(logger, connectAndStreamFalcoNetworkFlows, sm, falcoDone)
			}

			// Block until one of the streams fail. Then we will jump to the top of
			// this loop & try again: authenticate and open the streams.
			select {
			case <-ciliumDone:
				failureReason = "Cilium network flow stream closed"
			case <-falcoDone:
				failureReason = "Falco network flow stream closed"
			case <-resourceDone:
				failureReason = "Resource stream closed"
			case <-logDone:
				failureReason = "Log stream closed"
			case <-configDone:
				failureReason = "Configuration update stream closed"
			}
			authConContextCancel()
		}

		logger.Warn("One or more streams have been closed; closing and reopening the connection to CloudSecure",
			zap.String("failureReason", failureReason),
			zap.Int("attempt", attempt),
		)
	}
}

// NewAuthenticatedConnection gets a valid token and creats a connection to CloudSecure.
func NewAuthenticatedConnection(ctx context.Context, logger *zap.Logger, envMap EnvironmentConfig) (*grpc.ClientConn, pb.KubernetesInfoServiceClient, error) {
	authn := Authenticator{Logger: logger}

	clientID, clientSecret, err := authn.ReadCredentialsK8sSecrets(ctx, envMap.ClusterCreds)
	if errors.Is(err, ErrCredentialNotFoundInK8sSecret) {
		logger.Debug("Secret is not populated yet", zap.Error(err))
	} else if err != nil {
		logger.Error("Could not read K8s credentials", zap.Error(err))
	}

	// At the end of this block, have the clientID and clientSecret variables
	// populated. If not, we should have returned. A comment like this is
	// code-smell, meaning that this block should be hoisted to a function
	if clientID == "" && clientSecret == "" {
		OnboardingCredentials, err := authn.GetOnboardingCredentials(ctx, envMap.OnboardingClientId, envMap.OnboardingClientSecret)
		if err != nil {
			logger.Error("Failed to get onboarding credentials", zap.Error(err))
		}
		responseData, err := Onboard(ctx, envMap.TlsSkipVerify, envMap.OnboardingEndpoint, OnboardingCredentials, logger)
		if err != nil {
			logger.Error("Failed to register cluster", zap.Error(err))
			return nil, nil, err
		}
		err = authn.WriteK8sSecret(ctx, responseData, envMap.ClusterCreds)
		if err != nil {
			logger.Error("Failed to write secret to Kubernetes", zap.Error(err))
		}

		// k8s may take some time writing the secret. Here we will try 'maxRetries'
		// times, waiting 'waitDuration' seconds between each try. Even a single 1
		// second wait is probably fine, but just to be semantic this wait is done
		// as a poll.
		maxRetries := 5
		waitDuration := 1 * time.Second
		for i := 0; i < maxRetries; i++ {
			clientID, clientSecret, err = authn.ReadCredentialsK8sSecrets(ctx, envMap.ClusterCreds)
			if errors.Is(err, ErrCredentialNotFoundInK8sSecret) {
				logger.Debug("Secret is not populated yet", zap.Error(err))
			}
			if clientID != "" && clientSecret != "" {
				err = nil
				break
			}
			time.Sleep(waitDuration)
		}
		if err != nil {
			logger.Error("Could not read K8s credentials", zap.Error(err))
			return nil, nil, err
		}
	}
	conn, err := SetUpOAuthConnection(ctx, logger, envMap.TokenEndpoint, envMap.TlsSkipVerify, clientID, clientSecret)
	if err != nil {
		logger.Error("Failed to set up an OAuth connection", zap.Error(err))
		return nil, nil, err
	}

	client := pb.NewKubernetesInfoServiceClient(conn)

	return conn, client, err
}

func jitterTime(base time.Duration, maxJitterPct float64) time.Duration {
	jitterPct := rand.Float64() * maxJitterPct // [0, maxJitterPct)
	return time.Duration(float64(base) * (1. - jitterPct))
}
