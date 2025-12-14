// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"errors"
	"io"
	"maps"
	"math"
	"math/rand"
	"net"
	"net/http"
	"regexp"
	"slices"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/hubble"
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
	FlowCache          *FlowCache
	verboseDebugging   bool
	networkFlowsReady  chan struct{}
}

type KeepalivePeriods struct {
	KubernetesNetworkFlows time.Duration
	Logs                   time.Duration
	KubernetesResources    time.Duration
	Configuration          time.Duration
}

type StreamSuccessPeriod struct {
	Connect time.Duration
	Auth    time.Duration
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
	// Client ID for onboarding. "" if not specified, i.e. if the operator is not meant to onboard itself.
	OnboardingClientID string
	// Client secret for onboarding. "" if not specified, i.e. if the operator is not meant to onboard itself.
	OnboardingClientSecret string
	// URL of the onboarding endpoint.
	OnboardingEndpoint string
	// Port for the IPFIX collector
	IPFIXCollectorPort string
	// Namespace of OVN-Kubernetes
	OVNKNamespace string
	// URL of the token endpoint.
	TokenEndpoint string
	// Whether to skip TLS certificate verification when starting a stream.
	TlsSkipVerify bool
	// KeepalivePeriods specifies the period (minus jitter) between two keepalives sent on each stream
	KeepalivePeriods KeepalivePeriods
	// PodNamespace is the namespace where the cloud-operator is deployed
	PodNamespace string
	// How long must a stream be in a state for our exponentialBackoff function to
	// consider it a success.
	StreamSuccessPeriod StreamSuccessPeriod
	// HTTP Proxy URL
	HttpsProxy string
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

// ServerIsHealthy checks if a deadlock has occurred within the threaded resource listing process.
func ServerIsHealthy() bool {
	dd.mutex.RLock()
	defer dd.mutex.RUnlock()

	if dd.processingResources && time.Since(dd.timeStarted) > 5*time.Minute {
		return false
	}

	return true
}

// disableSubsystemCausingError mutates the `streamManager`
// receiver by inspecting the error. Thanks to HandleTLSHandshakeError
// we have a nicely typed error. If we recognize the specific error,
// then disable JUST the subsystem that caused it. If we do not
// recognize the error, then just give up entirely on exporting Cilium flows.
func (sm *streamManager) disableSubsystemCausingError(err error) {
	switch {
	case errors.Is(err, tls.ErrTLSALPNHandshakeFailed):
		sm.streamClient.tlsAuthProperties.DisableALPN = true
	case errors.Is(err, tls.ErrNoTLSHandshakeFailed):
		sm.streamClient.tlsAuthProperties.DisableTLS = true
	default:
		sm.streamClient.disableNetworkFlowsCilium = true
	}
}

// buildResourceApiGroupMap creates a mapping between Kubernetes resources and their corresponding API groups.
// It uses the discovery client to fetch all available API groups and resources, then maps the requested resources to their API groups.
func (sm *streamManager) buildResourceApiGroupMap(resources []string, clientset *kubernetes.Clientset, logger *zap.Logger) (map[string]string, error) {
	// Map to store resource-to-API group mapping
	resourceAPIGroupMap := make(map[string]string)

	// Convert input resources list to a map for quick lookups
	resourceSet := make(map[string]struct{})
	for _, resource := range resources {
		resourceSet[resource] = struct{}{}
	}

	// Create a discovery client for fetching API groups and resources
	discoveryClient := discovery.NewDiscoveryClient(clientset.RESTClient())

	apiGroups, err := discoveryClient.ServerGroups()
	if err != nil {
		logger.Error("Error fetching API groups", zap.Error(err))

		return resourceAPIGroupMap, err
	}

	// Iterate over all API groups and versions
	for _, group := range apiGroups.Groups {
		for _, version := range group.Versions {
			resourceList, err := discoveryClient.ServerResourcesForGroupVersion(version.GroupVersion)
			if err != nil {
				if apierrors.IsForbidden(err) {
					continue
				} else {
					return nil, err
				}
			}

			// Map resources to their API groups
			for _, resource := range resourceList.APIResources {
				if _, exists := resourceSet[resource.Name]; exists {
					if group.Name == "metrics.k8s.io" {
						logger.Info("Skipping this as it causes issues with discovery",
							zap.String("group", group.Name),
							zap.String("resource", resource.Name),
						)

						continue
					}

					resourceAPIGroupMap[resource.Name] = group.Name
				}
			}
		}
	}

	return resourceAPIGroupMap, nil
}

// StreamResources handles the resource stream.
func (sm *streamManager) StreamResources(ctx context.Context, logger *zap.Logger, cancel context.CancelFunc, keepalivePeriod time.Duration) error {
	defer cancel()
	defer func() {
		dd.processingResources = false
	}()

	clusterConfig, err := rest.InClusterConfig()
	if err != nil {
		logger.Error("Error getting in-cluster config", zap.Error(err))

		return err
	}
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

	// Build resourceAPIGroupMap from Kubernetes API.
	resourceAPIGroupMap, err = sm.buildResourceApiGroupMap(resources, clientset, logger)
	if err != nil {
		logger.Error("Failed to build resource api group map", zap.Error(err))

		return err
	}

	dd.mutex.Lock()
	dd.timeStarted = time.Now()
	dd.processingResources = true
	dd.mutex.Unlock()

	err = sm.sendClusterMetadata(ctx, logger)
	if err != nil {
		logger.Error("Failed to send cluster metadata", zap.Error(err))

		return err
	}

	allWatchInfos := make([]watcherInfo, 0, len(resourceAPIGroupMap))

	// Create a single rate limiter to be shared across all resource managers
	// This ensures we don't overwhelm the k8s API server with too many concurrent watch requests
	sharedLimiter := rate.NewLimiter(1, 5)
	resourceManagers := make(map[string]*ResourceManager)

	for _, resource := range slices.Sorted(maps.Keys(resourceAPIGroupMap)) {
		apiGroup := resourceAPIGroupMap[resource]

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Create a new resource manager for each resource type
		resourceManager := NewResourceManager(ResourceManagerConfig{
			ResourceName:  resource,
			ApiGroup:      apiGroup,
			Clientset:     clientset,
			BaseLogger:    logger,
			DynamicClient: dynamicClient,
			StreamManager: sm,
			Limiter:       sharedLimiter,
		})
		resourceManagers[resource] = resourceManager

		resourceVersion, err := resourceManager.DynamicListResources(ctx, resourceManager.logger, apiGroup)
		if err != nil {
			if apierrors.IsForbidden(err) {
				logger.Warn("Access forbidden for resource", zap.String("kind", resource), zap.String("api_group", apiGroup), zap.Error(err))

				continue
			}

			return err
		}

		allWatchInfos = append(allWatchInfos, watcherInfo{
			resource:        resource,
			apiGroup:        apiGroup,
			resourceVersion: resourceVersion,
		})
	}

	// PHASE 2: Send snapshot complete
	err = sm.sendResourceSnapshotComplete(logger)
	if err != nil {
		logger.Error("Failed to send snapshot complete", zap.Error(err))

		return err
	}

	logger.Info("Successfully sent resource snapshot")

	mutationChan := make(chan *pb.KubernetesResourceMutation)
	watcherWaitGroup := sync.WaitGroup{}
	// PHASE 3: Start watchers concurrently
	for _, info := range allWatchInfos {
		resourceManager := resourceManagers[info.resource]

		watcherWaitGroup.Add(1)

		go func(info watcherInfo, manager *ResourceManager) {
			defer watcherWaitGroup.Done()

			manager.WatchK8sResources(ctx, cancel, info.resourceVersion, mutationChan)
		}(info, resourceManager)
	}

	dd.mutex.Lock()
	dd.processingResources = false
	dd.mutex.Unlock()

	ticker := time.NewTicker(jitterTime(keepalivePeriod, 0.10))
	defer ticker.Stop()

	go func() {
		// Wait for all watchers to finish before we can close the mutation channel
		// If a watcher crashes for some reason all watchers context will be cancelled
		// This will allow us to close the mutation channel and prevent a channel leak
		watcherWaitGroup.Wait()
		close(mutationChan)
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			err := sm.sendKeepalive(logger, STREAM_RESOURCES)
			if err != nil {
				return err
			}
		case mutation := <-mutationChan:
			request := &pb.SendKubernetesResourcesRequest{
				Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{
					KubernetesResourceMutation: mutation,
				},
			}

			err := sm.sendToResourceStream(logger, request)
			if err != nil {
				return err
			}
		}
	}
}

// StreamLogs handles the log stream.
func (sm *streamManager) StreamLogs(ctx context.Context, logger *zap.Logger, keepalivePeriod time.Duration) error {
	errCh := make(chan error, 1)
	defer close(errCh)

	go func() {
		for {
			_, err := sm.streamClient.logStream.Recv()
			if errors.Is(err, io.EOF) {
				logger.Info("Server closed the SendLogs stream")

				errCh <- nil

				return
			}

			if err != nil {
				logger.Error("Stream terminated", zap.Error(err))

				errCh <- err

				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			if err != nil {
				return err
			}

			return nil
		}
	}
}

// StreamConfigurationUpdates streams configuration updates and applies them dynamically.
func (sm *streamManager) StreamConfigurationUpdates(ctx context.Context, logger *zap.Logger, keepalivePeriod time.Duration) error {
	errCh := make(chan error, 1)
	defer close(errCh)

	go func() {
		for {
			resp, err := sm.streamClient.configStream.Recv()
			if errors.Is(err, io.EOF) {
				logger.Info("Server closed the GetConfigurationUpdates stream")

				errCh <- nil

				return
			}

			if err != nil {
				logger.Error("Stream terminated", zap.Error(err))

				errCh <- err

				return
			}

			// Process the configuration update based on its type.
			switch update := resp.GetResponse().(type) {
			case *pb.GetConfigurationUpdatesResponse_UpdateConfiguration:
				logger.Info("Received configuration update",
					zap.Stringer("log_level", update.UpdateConfiguration.GetLogLevel()),
				)

				if sm.verboseDebugging {
					logger.Debug("verboseDebugging is true, setting log level to debug")
					sm.bufferedGrpcSyncer.updateLogLevel(pb.LogLevel_LOG_LEVEL_DEBUG)
				} else {
					sm.bufferedGrpcSyncer.updateLogLevel(update.UpdateConfiguration.GetLogLevel())
				}
			default:
				logger.Warn("Received unknown configuration update", zap.Any("response", resp))
			}
		}
	}()

	ticker := time.NewTicker(jitterTime(keepalivePeriod, 0.10))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			if err != nil {
				return err
			}

			return nil
		case <-ticker.C:
			err := sm.sendKeepalive(logger, STREAM_CONFIGURATION)
			if err != nil {
				return err
			}
		}
	}
}

// startFlowCacheOutReader starts a goroutine that reads flows from the flow cache
// and sends them to the cloud secure. Also sends keepalives at the configured period.
func (sm *streamManager) startFlowCacheOutReader(ctx context.Context, logger *zap.Logger, keepalivePeriod time.Duration) error {
	ticker := time.NewTicker(jitterTime(keepalivePeriod, 0.10))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			err := sm.sendKeepalive(logger, STREAM_NETWORK_FLOWS)
			if err != nil {
				return err
			}
		case flow := <-sm.FlowCache.outFlows:
			err := sm.sendNetworkFlowRequest(logger, flow)
			if err != nil {
				return err
			}
		}
	}
}

// findHubbleRelay returns a *CiliumFlowCollector if hubble relay is found in the given namespace.
func (sm *streamManager) findHubbleRelay(ctx context.Context, logger *zap.Logger) *CiliumFlowCollector {
	ciliumFlowCollector, err := newCiliumFlowCollector(ctx, logger, sm.streamClient.ciliumNamespaces, sm.streamClient.tlsAuthProperties)
	if err != nil {
		logger.Error("Failed to create Cilium flow collector", zap.Error(err))

		return nil
	}

	return ciliumFlowCollector
}

// StreamCiliumNetworkFlows handles the cilium network flow stream.
func (sm *streamManager) StreamCiliumNetworkFlows(ctx context.Context, logger *zap.Logger) error {
	// TODO: Add logic for a discoveribility function to decide which CNI to use.
	ciliumFlowCollector := sm.findHubbleRelay(ctx, logger)
	if ciliumFlowCollector == nil {
		logger.Info("Failed to initialize Cilium Hubble Relay flow collector; disabling flow collector")

		return errors.New("hubble relay cannot be found")
	}

	err := ciliumFlowCollector.exportCiliumFlows(ctx, sm)
	if err != nil {
		logger.Warn("Failed to collect and export flows from Cilium Hubble Relay", zap.Error(err))
		sm.disableSubsystemCausingError(err)

		return err
	}

	return nil
}

// StreamFalcoNetworkFlows handles the falco network flow stream.
func (sm *streamManager) StreamFalcoNetworkFlows(ctx context.Context, logger *zap.Logger) error {
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

			convertedFiveTupleFlow, err := parsePodNetworkInfo(match[1])
			if convertedFiveTupleFlow == nil {
				// If the event can't be parsed, consider that it's not a flow event and just ignore it.
				// If the event has bad ports in any way ignore it.
				// If the event has an incomplete L3/L4 layer lets just ignore it.
				continue
			} else if err != nil {
				logger.Error("Failed to parse Falco event into flow", zap.Error(err))

				return err
			}

			err = sm.FlowCache.CacheFlow(ctx, convertedFiveTupleFlow)
			if err != nil {
				logger.Error("Failed to cache flow", zap.Error(err))

				return err
			}
		}
	}
}

// StreamOVNKNetworkFlows handles the OVN-K network flow stream.
func (sm *streamManager) StreamOVNKNetworkFlows(ctx context.Context, logger *zap.Logger) error {
	err := sm.startOVNKIPFIXCollector(ctx, logger)
	if err != nil {
		logger.Error("Failed to listen for OVN-K IPFIX flows", zap.Error(err))

		return err
	}

	return nil
}

// connectAndStreamCiliumNetworkFlows creates networkFlowsStream client and
// begins the streaming of network flows.
func (sm *streamManager) connectAndStreamCiliumNetworkFlows(logger *zap.Logger, _ time.Duration) error {
	ciliumCtx, ciliumCancel := context.WithCancel(context.Background())
	defer ciliumCancel()

	sendCiliumNetworkFlowsStream, err := sm.streamClient.client.SendKubernetesNetworkFlows(ciliumCtx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))

		return err
	}

	sm.streamClient.networkFlowsStream = sendCiliumNetworkFlowsStream
	close(sm.networkFlowsReady)

	logger.Debug("Starting to stream cilium network flows")

	err = sm.StreamCiliumNetworkFlows(ciliumCtx, logger)
	if err != nil {
		if errors.Is(err, hubble.ErrHubbleNotFound) || errors.Is(err, hubble.ErrNoPortsAvailable) {
			logger.Warn("Disabling Cilium flow collection", zap.Error(err))

			return ErrStopRetries
		}

		return err
	}

	return nil
}

// connectAndStreamFalcoNetworkFlows creates networkFlowsStream client and
// begins the streaming of network flows.
func (sm *streamManager) connectAndStreamFalcoNetworkFlows(logger *zap.Logger, _ time.Duration) error {
	falcoCtx, falcoCancel := context.WithCancel(context.Background())
	defer falcoCancel()

	sendFalcoNetworkFlows, err := sm.streamClient.client.SendKubernetesNetworkFlows(falcoCtx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))

		return err
	}

	sm.streamClient.networkFlowsStream = sendFalcoNetworkFlows
	close(sm.networkFlowsReady)

	err = sm.StreamFalcoNetworkFlows(falcoCtx, logger)
	if err != nil {
		logger.Error("Failed to stream Falco network flows", zap.Error(err))

		return err
	}

	return nil
}

// connectAndStreamResources creates resourceStream client and begins the
// streaming of resources. Also starts a goroutine to send keepalives at the
// configured period.
func (sm *streamManager) connectAndStreamResources(logger *zap.Logger, keepalivePeriod time.Duration) error {
	resourceCtx, resourceCancel := context.WithCancel(context.Background())
	defer resourceCancel()

	sendKubernetesResourcesStream, err := sm.streamClient.client.SendKubernetesResources(resourceCtx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))

		return err
	}

	sm.streamClient.resourceStream = sendKubernetesResourcesStream

	err = sm.StreamResources(resourceCtx, logger, resourceCancel, keepalivePeriod)
	if err != nil {
		logger.Error("Failed to bootup and stream resources", zap.Error(err))

		return err
	}

	return nil
}

// connectAndStreamLogs creates sendLogs client and begins the streaming of
// logs. Also starts a goroutine to send keepalives at the configured period.
func (sm *streamManager) connectAndStreamLogs(logger *zap.Logger, keepalivePeriod time.Duration) error {
	logCtx, logCancel := context.WithCancel(context.Background())
	defer logCancel()

	sendLogsStream, err := sm.streamClient.client.SendLogs(logCtx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))

		return err
	}

	sm.streamClient.logStream = sendLogsStream
	sm.bufferedGrpcSyncer.UpdateClient(sm.streamClient.logStream, sm.streamClient.conn)

	err = sm.StreamLogs(logCtx, logger, keepalivePeriod)
	if err != nil {
		logger.Error("Failed to bootup and stream logs", zap.Error(err))

		return err
	}

	return nil
}

// connectAndStreamConfigurationUpdates creates a configuration update stream client and listens for configuration changes.
func (sm *streamManager) connectAndStreamConfigurationUpdates(logger *zap.Logger, keepalivePeriod time.Duration) error {
	configCtx, configCancel := context.WithCancel(context.Background())
	defer configCancel()

	getConfigurationUpdatesStream, err := sm.streamClient.client.GetConfigurationUpdates(configCtx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))

		return err
	}

	sm.streamClient.configStream = getConfigurationUpdatesStream

	err = sm.StreamConfigurationUpdates(configCtx, logger, keepalivePeriod)
	if err != nil {
		logger.Error("Configuration update stream encountered an error", zap.Error(err))

		return err
	}

	return nil
}

// connectAndStreamOVNKNetworkFlows creates OVN-K networkFlowsStream client and
// begins the streaming of OVN-K network flows.
func (sm *streamManager) connectAndStreamOVNKNetworkFlows(logger *zap.Logger, _ time.Duration) error {
	ovnkContext, ovnkCancel := context.WithCancel(context.Background())
	defer ovnkCancel()

	sendOVNKNetworkFlows, err := sm.streamClient.client.SendKubernetesNetworkFlows(ovnkContext)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))

		return err
	}

	sm.streamClient.networkFlowsStream = sendOVNKNetworkFlows
	close(sm.networkFlowsReady)

	err = sm.StreamOVNKNetworkFlows(ovnkContext, logger)
	if err != nil {
		logger.Error("Failed to stream OVN-K network flows", zap.Error(err))

		return err
	}

	return nil
}

// Generic function to manage any stream with backoff and reconnection logic.
func (sm *streamManager) manageStream(
	logger *zap.Logger,
	connectAndStream func(*zap.Logger, time.Duration) error,
	done chan struct{},
	keepalivePeriod time.Duration,
	streamSuccessPeriod StreamSuccessPeriod,
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
			ActionTimeToConsiderSuccess: streamSuccessPeriod.Connect,
		}, f)
	}

	// If repeated attempts to connect fail, that is, the "SevereErrorThreshold"
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
			ActionTimeToConsiderSuccess: streamSuccessPeriod.Auth,
		}, funcWithBackoff)
	}

	err := funcWithBackoffAndReset()
	if err != nil {
		logger.Error("Failed to reset connectAndStream. Something is very wrong", zap.Error(err))

		return
	}
}

// determineFlowCollector determines the flow collector type and returns the flow collector type, stream function, and the corresponding done channel.
func determineFlowCollector(ctx context.Context, logger *zap.Logger, sm *streamManager, envMap EnvironmentConfig, clientset *kubernetes.Clientset) (pb.FlowCollector, func(*zap.Logger, time.Duration) error, chan struct{}) {
	switch {
	case sm.findHubbleRelay(ctx, logger) != nil && !sm.streamClient.disableNetworkFlowsCilium:
		sm.streamClient.tlsAuthProperties.DisableALPN = false
		sm.streamClient.tlsAuthProperties.DisableTLS = false

		return pb.FlowCollector_FLOW_COLLECTOR_CILIUM, sm.connectAndStreamCiliumNetworkFlows, make(chan struct{})
	case sm.isOVNKDeployed(ctx, logger, envMap.OVNKNamespace, clientset):
		return pb.FlowCollector_FLOW_COLLECTOR_OVNK, sm.connectAndStreamOVNKNetworkFlows, make(chan struct{})
	default:
		return pb.FlowCollector_FLOW_COLLECTOR_FALCO, sm.connectAndStreamFalcoNetworkFlows, make(chan struct{})
	}
}

// ConnectStreams will continue to reboot and restart the main operations within
// the operator if any disconnects or errors occur.
//
//nolint:gocognit // ConnectStreams is complex due to orchestration; refactor pending
func ConnectStreams(ctx context.Context, logger *zap.Logger, envMap EnvironmentConfig, bufferedGrpcSyncer *BufferedGrpcWriteSyncer) {
	// Falco channels communicate news events between http server and our network flows stream,
	falcoEventChan := make(chan string)
	http.HandleFunc("/", NewFalcoEventHandler(falcoEventChan))

	// Start our falco server and have it passively listen, if it fails, try to just restart it.
	go func() {
		for {
			// Create a custom listener, this listener has SO_REUSEADDR option set by default
			var listenerConfig net.ListenConfig

			listener, err := listenerConfig.Listen(ctx, "tcp", falcoPort)
			if err != nil {
				logger.Fatal("Failed to listen on Falco port", zap.String("address", falcoPort), zap.Error(err))
			}

			// Create the HTTP server
			falcoEvent := &http.Server{
				Addr:              falcoPort,
				ReadHeaderTimeout: 5 * time.Second,
				ReadTimeout:       5 * time.Second,
			}

			logger.Info("Falco server listening", zap.String("address", falcoPort))

			err = falcoEvent.Serve(listener)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
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
		var failureReason string

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
				conn:               authConn,
				client:             client,
				ciliumNamespaces:   envMap.CiliumNamespaces,
				falcoEventChan:     falcoEventChan,
				ipfixCollectorPort: envMap.IPFIXCollectorPort,
			}

			sm := &streamManager{
				verboseDebugging:   envMap.VerboseDebugging,
				streamClient:       streamClient,
				bufferedGrpcSyncer: bufferedGrpcSyncer,
				FlowCache: NewFlowCache(
					20*time.Second, // TODO: Make the active timeout configurable.
					1000,           // TODO: Make the maxFlows capacity configurable.
					make(chan pb.Flow, 100),
				),
				networkFlowsReady: make(chan struct{}),
			}

			resourceDone := make(chan struct{})
			logDone := make(chan struct{})
			configDone := make(chan struct{})

			sm.bufferedGrpcSyncer.done = logDone

			go sm.manageStream(
				logger.With(zap.String("stream", "SendKubernetesResources")),
				sm.connectAndStreamResources,
				resourceDone,
				envMap.KeepalivePeriods.KubernetesResources,
				envMap.StreamSuccessPeriod,
			)

			go sm.manageStream(
				logger.With(zap.String("stream", "SendLogs")),
				sm.connectAndStreamLogs,
				logDone,
				envMap.KeepalivePeriods.Logs,
				envMap.StreamSuccessPeriod,
			)

			go sm.manageStream(
				logger.With(zap.String("stream", "GetConfigurationUpdates")),
				sm.connectAndStreamConfigurationUpdates,
				configDone,
				envMap.KeepalivePeriods.Configuration,
				envMap.StreamSuccessPeriod,
			)

			ovnkDone := make(chan struct{})
			falcoDone := make(chan struct{})
			ciliumDone := make(chan struct{})
			flowCacheRunDone := make(chan struct{})
			flowCacheOutReaderDone := make(chan struct{})

			go func() {
				defer close(flowCacheRunDone)

				ctxFlowCacheRun, ctxCancelFlowCacheRun := context.WithCancel(authConContext)
				defer ctxCancelFlowCacheRun()

				err := sm.FlowCache.Run(ctxFlowCacheRun, logger)
				if err != nil {
					logger.Info("Failed to execute flow caching and eviction", zap.Error(err))

					return
				}
			}()

			clientset, err := NewClientSet()
			if err != nil {
				logger.Error("Failed to create clientset", zap.Error(err))
				authConContextCancel()

				return
			}

			flowCollector, streamFunc, doneChannel := determineFlowCollector(ctx, logger, sm, envMap, clientset)
			sm.streamClient.flowCollector = flowCollector

			go sm.manageStream(
				logger.With(zap.String("stream", "SendKubernetesNetworkFlows")),
				streamFunc,
				doneChannel,
				envMap.KeepalivePeriods.KubernetesNetworkFlows,
				envMap.StreamSuccessPeriod,
			)

			go func() {
				defer close(flowCacheOutReaderDone)

				// wait until the flow collector is initialized, or bail if the context is canceled
				select {
				case <-sm.networkFlowsReady:
					// proceed
				case <-authConContext.Done():
					logger.Info("Failed to start sending network flows from cache", zap.Error(authConContext.Err()))
					return
				}

				ctxFlowCacheOutReader, ctxCancelFlowCacheOutReader := context.WithCancel(authConContext)
				defer ctxCancelFlowCacheOutReader()

				err := sm.startFlowCacheOutReader(ctxFlowCacheOutReader, logger, envMap.KeepalivePeriods.KubernetesNetworkFlows)
				if err != nil {
					logger.Info("Failed to send network flow from cache", zap.Error(err))

					return
				}
			}()

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
			case <-ovnkDone:
				failureReason = "OVN-K network flow stream closed"
			case <-flowCacheOutReaderDone:
				failureReason = "Flow cache reader failed"
			case <-flowCacheRunDone:
				failureReason = "Flow cache running process failed"
			}

			authConContextCancel()
		}

		logger.Warn("One or more streams have been closed; closing and reopening the connection to CloudSecure",
			zap.String("failureReason", failureReason),
			zap.Int("attempt", attempt),
		)
		resetTimer.Reset(jitterTime(5*time.Second, 0.20))
	}
}

// NewAuthenticatedConnection gets a valid token and creats a connection to CloudSecure.
func NewAuthenticatedConnection(ctx context.Context, logger *zap.Logger, envMap EnvironmentConfig) (*grpc.ClientConn, pb.KubernetesInfoServiceClient, error) {
	clientID, clientSecret, err := GetClusterCredentials(ctx, logger, envMap)
	if err != nil {
		return nil, nil, err
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
	// Jitter percentage is in range [0, maxJitterPct)
	jitterPct := rand.Float64() * maxJitterPct //nolint:gosec

	return time.Duration(float64(base) * (1. - jitterPct))
}
