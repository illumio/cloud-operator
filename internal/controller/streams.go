// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"errors"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"regexp"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	observer "github.com/cilium/cilium/api/v1/observer"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

type StreamType string

const (
	STREAM_NETWORK_FLOWS        = StreamType("network_flows")
	STREAM_NETWORK_FLOWS_CILIUM = StreamType("network_flows_cilium")
	STREAM_NETWORK_FLOWS_FALCO  = StreamType("network_flows_falco")
	STREAM_RESOURCES            = StreamType("resources")
	STREAM_LOGS                 = StreamType("logs")
	STREAM_CONFIGURATION        = StreamType("configuration")
)

type streamClient struct {
	ciliumNamespace           string
	conn                      *grpc.ClientConn
	client                    pb.KubernetesInfoServiceClient
	disableNetworkFlowsCilium bool
	falcoEventChan            chan string
	flowCollector             pb.FlowCollector
	logStream                 pb.KubernetesInfoService_SendLogsClient
	networkFlowsStream        pb.KubernetesInfoService_SendKubernetesNetworkFlowsClient
	resourceStream            pb.KubernetesInfoService_SendKubernetesResourcesClient
	configStream              pb.KubernetesInfoService_GetConfigurationUpdatesClient
}

type deadlockDetector struct {
	mutex               sync.RWMutex
	processingResources bool
	timeStarted         time.Time
}

type streamManager struct {
	bufferedGrpcSyncer *BufferedGrpcWriteSyncer
	streamClient       *streamClient
}

type KeepalivePeriods struct {
	KubernetesNetworkFlows time.Duration
	Logs                   time.Duration
	KubernetesResources    time.Duration
	Configuration          time.Duration
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
	// KeepalivePeriods specifies the period (minus jitter) between two keepalives sent on each stream
	KeepalivePeriods KeepalivePeriods
	// PodNamespace is the namespace where the cloud-operator is deployed
	PodNamespace string
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
var ErrFalcoEventIsNotFlow = errors.New("ignoring falco event, not a network flow")
var ErrFalcoIncompleteL3Flow = errors.New("ignoring incomplete falco l3 network flow")
var ErrFalcoIncompleteL4Flow = errors.New("ignoring incomplete falco l4 network flow")
var ErrFalcoInvalidPort = errors.New("ignoring incomplete falco flow due to bad ports")
var ErrFalcoTimestamp = errors.New("incomplete or incorrectly formatted timestamp found in Falco flow")
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
func (sm *streamManager) StreamResources(ctx context.Context, logger *zap.Logger, cancel context.CancelFunc) error {
	defer cancel()
	defer func() {
		dd.processingResources = false
	}()
	logger = logger.With(zap.String("stream", string(STREAM_RESOURCES)))
	clusterConfig, err := rest.InClusterConfig()
	if err != nil {
		logger.Error("Error getting in-cluster config", zap.Error(err))
		return err
	}
	var allResourcesSnapshotted sync.WaitGroup
	var snapshotCompleted sync.WaitGroup
	// Create a dynamic client
	dynamicClient, err := dynamic.NewForConfig(clusterConfig)
	if err != nil {
		logger.Error("Error creating dynamic client", zap.Error(err))
		return err
	}

	clientset, err := NewClientSet()
	if err != nil {
		logger.Error("Failed to create clientset", zap.Error(err))
		return err
	}
	apiGroups, err := clientset.Discovery().ServerGroups()
	if err != nil {
		logger.Error("Failed to discover API groups", zap.Error(err))
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
		logger:        logger,
		dynamicClient: dynamicClient,
		streamManager: sm,
	}
	err = sm.sendClusterMetadata(ctx, logger)
	if err != nil {
		logger.Error("Failed to send cluster metadata", zap.Error(err))
		return err
	}
	for resource, apiGroup := range resourceAPIGroupMap {
		allResourcesSnapshotted.Add(1)
		go resourceLister.DynamicListAndWatchResources(ctx, cancel, resource, apiGroup, &allResourcesSnapshotted, &snapshotCompleted)
	}
	allResourcesSnapshotted.Wait()
	err = sm.sendResourceSnapshotComplete(logger)
	dd.timeStarted = time.Now()
	dd.mutex.Lock()
	dd.processingResources = false
	dd.mutex.Unlock()
	if err != nil {
		logger.Error("Failed to send resource snapshot complete", zap.Error(err))
		return err
	}
	snapshotCompleted.Done()

	<-ctx.Done()
	return ctx.Err()
}

// StreamLogs handles the log stream.
func (sm *streamManager) StreamLogs(ctx context.Context, cancel context.CancelFunc, logger *zap.Logger) error {
	defer cancel()

	lg := logger.With(zap.String("stream", string(STREAM_LOGS)))

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			_, err := sm.streamClient.logStream.Recv()
			if err == io.EOF {
				lg.Info("Server closed the SendLogs stream")
				return nil
			}
			if err != nil {
				lg.Error("Stream terminated", zap.Error(err))
				return err
			}
		}
	}
}

// StreamConfigurationUpdates streams configuration updates and applies them dynamically.
func (sm *streamManager) StreamConfigurationUpdates(ctx context.Context, cancel context.CancelFunc, logger *zap.Logger) error {
	defer cancel()

	lg := logger.With(zap.String("stream", string(STREAM_CONFIGURATION)))

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			cfgUpdate, err := sm.streamClient.configStream.Recv()
			if err == io.EOF {
				lg.Info("Server closed the GetConfigurationUpdates stream")
				return nil
			}
			if err != nil {
				lg.Error("Stream terminated", zap.Error(err))
				return err
			}

			sm.handleConfigurationUpdate(lg, cfgUpdate)
		}
	}
}

func (sm *streamManager) handleConfigurationUpdate(logger *zap.Logger, cfgUpdate *pb.GetConfigurationUpdatesResponse) {
	// Process the configuration update based on its type.
	switch update := cfgUpdate.Response.(type) {
	case *pb.GetConfigurationUpdatesResponse_UpdateConfiguration:
		logger.Info("Received configuration update",
			zap.Stringer("log_level", update.UpdateConfiguration.LogLevel),
		)
		sm.bufferedGrpcSyncer.updateLogLevel(update.UpdateConfiguration.LogLevel)
	default:
		logger.Warn("Received unknown configuration update", zap.Any("cfgUpdate", cfgUpdate))
	}
}

// findHubbleRelay returns a *CiliumFlowCollector if hubble relay is found in the given namespace
func (sm *streamManager) findHubbleRelay(ctx context.Context, logger *zap.Logger, ciliumNamespace string) *CiliumFlowCollector {
	// TODO: Add logic for a discoveribility function to decide which CNI to use.
	ciliumFlowCollector, err := newCiliumFlowCollector(ctx, logger, ciliumNamespace)
	if err != nil {
		return nil
	}
	return ciliumFlowCollector
}

// StreamCiliumNetworkFlows handles the cilium network flow stream.
func (sm *streamManager) StreamCiliumNetworkFlows(ctx context.Context, cancel context.CancelFunc, logger *zap.Logger, ciliumNamespace string) error {
	defer cancel()

	lg := logger.With(zap.String("stream", string(STREAM_NETWORK_FLOWS_CILIUM)))

	// TODO: Add logic for a discoveribility function to decide which CNI to use.
	ciliumFlowCollector := sm.findHubbleRelay(ctx, lg, ciliumNamespace)
	if ciliumFlowCollector == nil {
		lg.Info("Failed to initialize Cilium Hubble Relay flow collector; disabling flow collector")
		return ErrHubbleNotFound
	}

	stream, err := ciliumFlowCollector.client.GetFlows(ctx, &observer.GetFlowsRequest{
		Number: ciliumHubbleRelayMaxFlowCount,
		Follow: true,
	})
	if err != nil {
		ciliumFlowCollector.logger.Error("Error getting network flows", zap.Error(err))
		sm.streamClient.disableNetworkFlowsCilium = true
		return err
	}
	defer func() {
		err = stream.CloseSend()
		if err != nil {
			ciliumFlowCollector.logger.Error("Error closing observerClient stream", zap.Error(err))
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			flow, err := stream.Recv()
			if err != nil {
				ciliumFlowCollector.logger.Warn("Failed to get flow log from stream", zap.Error(err))
				sm.streamClient.disableNetworkFlowsCilium = true
				return err
			}
			ciliumFlow := convertCiliumFlow(flow)
			if ciliumFlow == nil {
				continue
			}
			err = sm.sendNetworkFlowRequest(ciliumFlowCollector.logger, ciliumFlow)
			if err != nil {
				ciliumFlowCollector.logger.Error("Cannot send cilium flow", zap.Error(err))
				sm.streamClient.disableNetworkFlowsCilium = true
				return err
			}
		}
	}
}

// StreamFalcoNetworkFlows handles the falco network flow stream.
func (sm *streamManager) StreamFalcoNetworkFlows(ctx context.Context, cancel context.CancelFunc, logger *zap.Logger) error {
	defer cancel()

	lg := logger.With(zap.String("stream", string(STREAM_NETWORK_FLOWS_FALCO)))

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case falcoFlow := <-sm.streamClient.falcoEventChan:
			if !filterIllumioTraffic(falcoFlow) {
				continue
			}

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
				lg.Error("Failed to parse Falco event into flow", zap.Error(err))
				return err
			}
			err = sm.sendNetworkFlowRequest(lg, convertedFalcoFlow)
			if err != nil {
				lg.Error("Failed to send Falco flow", zap.Error(err))
				return err
			}
		}
	}
}

// StreamKeepalives loops infinitely as long as the keepalives are working. This
// should be run inside a goroutine in every `connectAndStream*` function
func (sm *streamManager) StreamKeepalives(
	ctx context.Context,
	cancel context.CancelFunc,
	logger *zap.Logger,
	period time.Duration,
	streamType StreamType,
) error {
	timer := time.NewTimer(jitterTime(period, 0.10))
	defer timer.Stop()
	defer cancel()

	lg := logger.With(zap.String("stream", string(streamType)))

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			err := sm.sendKeepalive(logger, streamType)
			if err != nil {
				lg.Error("Failed to send keepalives; canceling stream", zap.Error(err))
			}
			timer.Reset(jitterTime(period, 0.10))
		}
	}
}

// connectAndStreamCiliumNetworkFlows creates networkFlowsStream client and
// begins the streaming of network flows. Also starts a goroutine to send
// keepalives at the configured period
func (sm *streamManager) connectAndStreamCiliumNetworkFlows(logger *zap.Logger, keepalivePeriod time.Duration) error {
	ciliumCtx, ciliumCancel := context.WithCancel(context.Background())

	sendCiliumNetworkFlowsStream, err := sm.streamClient.client.SendKubernetesNetworkFlows(ciliumCtx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))
		return err
	}
	sm.streamClient.networkFlowsStream = sendCiliumNetworkFlowsStream

	go sm.StreamKeepalives(ciliumCtx, ciliumCancel, logger, keepalivePeriod, STREAM_NETWORK_FLOWS)
	go sm.StreamCiliumNetworkFlows(ciliumCtx, ciliumCancel, logger, sm.streamClient.ciliumNamespace)

	<-ciliumCtx.Done()
	return nil
}

// connectAndStreamFalcoNetworkFlows creates networkFlowsStream client and
// begins the streaming of network flows. Also starts a goroutine to send
// keepalives at the configured period
func (sm *streamManager) connectAndStreamFalcoNetworkFlows(logger *zap.Logger, keepalivePeriod time.Duration) error {
	falcoCtx, falcoCancel := context.WithCancel(context.Background())

	sendFalcoNetworkFlows, err := sm.streamClient.client.SendKubernetesNetworkFlows(falcoCtx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))
		return err
	}
	sm.streamClient.networkFlowsStream = sendFalcoNetworkFlows

	go sm.StreamKeepalives(falcoCtx, falcoCancel, logger, keepalivePeriod, STREAM_NETWORK_FLOWS)
	go sm.StreamFalcoNetworkFlows(falcoCtx, falcoCancel, logger)

	<-falcoCtx.Done()
	return nil
}

// connectAndStreamResources creates resourceStream client and begins the
// streaming of resources. Also starts a goroutine to send keepalives at the
// configured period
func (sm *streamManager) connectAndStreamResources(logger *zap.Logger, keepalivePeriod time.Duration) error {
	resourceCtx, resourceCancel := context.WithCancel(context.Background())

	sendKubernetesResourcesStream, err := sm.streamClient.client.SendKubernetesResources(resourceCtx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))
		return err
	}
	sm.streamClient.resourceStream = sendKubernetesResourcesStream

	go sm.StreamKeepalives(resourceCtx, resourceCancel, logger, keepalivePeriod, STREAM_RESOURCES)
	go sm.StreamResources(resourceCtx, logger, resourceCancel)

	<-resourceCtx.Done()
	return nil
}

// connectAndStreamLogs creates sendLogs client and begins the streaming of
// logs. Also starts a goroutine to send keepalives at the configured period
func (sm *streamManager) connectAndStreamLogs(logger *zap.Logger, keepalivePeriod time.Duration) error {
	logCtx, logCancel := context.WithCancel(context.Background())

	sendLogsStream, err := sm.streamClient.client.SendLogs(logCtx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))
		return err
	}
	sm.streamClient.logStream = sendLogsStream
	sm.bufferedGrpcSyncer.UpdateClient(sm.streamClient.logStream, sm.streamClient.conn)

	go sm.StreamKeepalives(logCtx, logCancel, logger, keepalivePeriod, STREAM_LOGS)
	go sm.StreamLogs(logCtx, logCancel, logger)

	<-logCtx.Done()
	return nil
}

// connectAndStreamConfigurationUpdates creates a configuration update stream client and listens for configuration changes.
func (sm *streamManager) connectAndStreamConfigurationUpdates(logger *zap.Logger, keepalivePeriod time.Duration) error {
	configCtx, configCancel := context.WithCancel(context.Background())

	getConfigurationUpdatesStream, err := sm.streamClient.client.GetConfigurationUpdates(configCtx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))
		return err
	}
	sm.streamClient.configStream = getConfigurationUpdatesStream

	go sm.StreamKeepalives(configCtx, configCancel, logger, keepalivePeriod, STREAM_CONFIGURATION)
	go sm.StreamConfigurationUpdates(configCtx, configCancel, logger)

	<-configCtx.Done()
	return nil
}

// Generic function to manage any stream with backoff and reconnection logic.
func (sm *streamManager) manageStream(
	logger *zap.Logger,
	connectAndStream func(*zap.Logger, time.Duration) error,
	done chan struct{},
	keepalivePeriod time.Duration,
) {
	defer close(done)

	f := func() error {
		return connectAndStream(logger, keepalivePeriod)
	}

	// If a stream goes down, try to reconnect. With exponential backoff
	funcWithBackoff := func() error {
		return exponentialBackoff(backoffOpts{
			InitialBackoff:       1 * time.Second,
			MaxBackoff:           1 * time.Minute,
			MaxJitterPct:         0.20,
			SevereErrorThreshold: 10,
			ExponentialFactor:    2.0,
			Logger: logger.With(
				zap.String("name", "retry_connect_and_stream"),
			),
		}, f)
	}

	// If reapated attempts to connect fail, that is, the "SevereErrorThreshold"
	// in the above backoff is triggered, then wait and try again. By setting
	// ExponentialFactor to 1, we will wait the same amount of time between every
	// attempt. This is desirable
	funcWithBackoffAndReset := func() error {
		return exponentialBackoff(backoffOpts{
			InitialBackoff:       10 * time.Minute,
			MaxBackoff:           10 * time.Second,
			MaxJitterPct:         0.10,
			SevereErrorThreshold: math.MaxInt,
			// Setting ExponentialFactor 1 will cause the backoff timer to stay
			// constant.
			ExponentialFactor: 1,
			Logger: logger.With(
				zap.String("name", "reset_retry_connect_and_stream"),
			),
		}, funcWithBackoff)
	}

	err := funcWithBackoffAndReset()
	if err != nil {
		logger.Error("Failed to reset connectAndStream. Something is very wrong", zap.Error(err))
		return
	}
}

// ConnectStreams will continue to reboot and restart the main operations within
// the operator if any disconnects or errors occur.
func ConnectStreams(ctx context.Context, logger *zap.Logger, envMap EnvironmentConfig, bufferedGrpcSyncer *BufferedGrpcWriteSyncer) {
	// Falco channels communicate news events between http server and our network flows stream,
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
				bufferedGrpcSyncer: bufferedGrpcSyncer,
			}
			ciliumFlowCollector := sm.findHubbleRelay(ctx, logger, sm.streamClient.ciliumNamespace)
			if ciliumFlowCollector == nil {
				sm.streamClient.disableNetworkFlowsCilium = true
				sm.streamClient.flowCollector = pb.FlowCollector_FLOW_COLLECTOR_FALCO
			} else {
				sm.streamClient.disableNetworkFlowsCilium = false
				sm.streamClient.flowCollector = pb.FlowCollector_FLOW_COLLECTOR_CILIUM
			}

			resourceDone := make(chan struct{})
			logDone := make(chan struct{})
			var ciliumDone, falcoDone chan struct{}
			configDone := make(chan struct{})

			sm.bufferedGrpcSyncer.done = logDone

			go sm.manageStream(
				logger.With(zap.String("stream", "SendKubernetesResources")),
				sm.connectAndStreamResources,
				resourceDone,
				envMap.KeepalivePeriods.KubernetesResources,
			)

			go sm.manageStream(
				logger.With(zap.String("stream", "SendLogs")),
				sm.connectAndStreamLogs,
				logDone,
				envMap.KeepalivePeriods.Logs,
			)

			go sm.manageStream(
				logger.With(zap.String("stream", "GetConfigurationUpdates")),
				sm.connectAndStreamConfigurationUpdates,
				configDone,
				envMap.KeepalivePeriods.Configuration,
			)

			// Only start network flows stream if not disabled
			if !sm.streamClient.disableNetworkFlowsCilium {
				ciliumDone = make(chan struct{})
				go sm.manageStream(
					logger.With(zap.String("stream", "SendKubernetesNetworkFlows")),
					sm.connectAndStreamCiliumNetworkFlows,
					ciliumDone,
					envMap.KeepalivePeriods.KubernetesNetworkFlows,
				)
			}

			if sm.streamClient.disableNetworkFlowsCilium {
				falcoDone := make(chan struct{})
				go sm.manageStream(
					logger.With(zap.String("stream", "SendKubernetesNetworkFlows")),
					sm.connectAndStreamFalcoNetworkFlows,
					falcoDone,
					envMap.KeepalivePeriods.KubernetesNetworkFlows,
				)
			}

			// Block until one of the streams fail. Then we will jump to the top of
			// this loop & try again: authenticate and open the streams.
			logger.Info("All streams are open and running")
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

	clientID, clientSecret, err := authn.ReadCredentialsK8sSecrets(ctx, envMap.ClusterCreds, envMap.PodNamespace)
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
		err = authn.WriteK8sSecret(ctx, responseData, envMap.ClusterCreds, envMap.PodNamespace)
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
			clientID, clientSecret, err = authn.ReadCredentialsK8sSecrets(ctx, envMap.ClusterCreds, envMap.PodNamespace)
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

// jitterTime subtracts a percentage from the base time, in order to introduce
// jitter. maxJitterPct must be in the range [0, 1).
//
// jitter is a technical term, meaning "a signal's deviation from true
// periodicity". This is desirable in distributed systems, because if all agents
// synchronize their messages, we stop calling that API requests and start
// calling that a DDoS attack.
func jitterTime(base time.Duration, maxJitterPct float64) time.Duration {
	jitterPct := rand.Float64() * maxJitterPct // [0, maxJitterPct)
	return time.Duration(float64(base) * (1. - jitterPct))
}
