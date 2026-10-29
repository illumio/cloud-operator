// Copyright 2026 Illumio, Inc. All Rights Reserved.

package calicoenterprise

import (
	"bufio"
	"context"
	cryptotls "crypto/tls"
	"errors"
	"net"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

// Errors for Calico syslog flow parsing.
var (
	ErrIncompleteFlow = errors.New("ignoring incomplete calico flow, missing five-tuple field")
	ErrInvalidPort    = errors.New("ignoring calico flow due to unparseable port")
	ErrUnknownProto   = errors.New("ignoring calico flow due to unknown protocol number")
	ErrInvalidIP      = errors.New("ignoring calico flow due to unparseable IP address")
)

// calicoSyslogMaxLineLength bounds a single syslog record. The LogCollector
// sends one flow per line; labels and policies can make records long, so we
// allow a generous max-token size to avoid bufio.Scanner truncating a record.
const calicoSyslogMaxLineLength = 1024 * 1024

// Bounds of the backoff between retries of a failed Accept, as in net/http.
const (
	calicoMinAcceptDelay = 5 * time.Millisecond
	calicoMaxAcceptDelay = time.Second
)

const (
	// defaultFirstByteTimeout bounds how long a new connection may stay silent
	// before the collector gives up on it.
	defaultFirstByteTimeout = 10 * time.Second

	// defaultHandshakeTimeout bounds the TLS handshake.
	defaultHandshakeTimeout = 10 * time.Second
)

// Flow holds the five-tuple of a Calico Enterprise LogCollector flow
// record.
type Flow struct {
	SourceIP        string
	DestinationIP   string
	SourcePort      uint16
	DestinationPort uint16
	Protocol        string
	IPVersion       string
	StartTimestamp  *timestamppb.Timestamp
}

// Collector collects flows from Calico Enterprise over syslog. Calico
// Enterprise's LogCollector connects over TCP, optionally with TLS,
// and sends records using RFC 6587 section 3.4.1 non-transparent
// (newline-terminated) framing.
type Collector struct {
	logger           *zap.Logger
	syslogPort       string
	tlsSettings      tls.ServerSettings
	tlsConfig        *cryptotls.Config
	firstByteTimeout time.Duration
	handshakeTimeout time.Duration
	flowSink         collector.FlowSink
	recordDropped    func()
}

// NewCollector creates a new Calico Enterprise syslog flow collector.
// recordDropped, if not nil, is called for each record that is not turned into
// a flow.
func NewCollector(logger *zap.Logger, syslogPort string, tlsSettings tls.ServerSettings, flowSink collector.FlowSink, recordDropped func()) *Collector {
	if recordDropped == nil {
		recordDropped = func() {}
	}

	return &Collector{
		logger:           logger,
		syslogPort:       syslogPort,
		tlsSettings:      tlsSettings,
		firstByteTimeout: defaultFirstByteTimeout,
		handshakeTimeout: defaultHandshakeTimeout,
		flowSink:         flowSink,
		recordDropped:    recordDropped,
	}
}

// calicoInstallationGVR identifies the Tigera operator's Installation resource.
var calicoInstallationGVR = schema.GroupVersionResource{
	Group:    "operator.tigera.io",
	Version:  "v1",
	Resource: "installations",
}

const (
	// calicoInstallationName is the name of the cluster's single Installation.
	calicoInstallationName = "default"
	// calicoEnterpriseVariant is the Installation variant of Calico Enterprise.
	calicoEnterpriseVariant = "TigeraSecureEnterprise"
)

// IsDeployed reports whether the cluster runs Calico Enterprise,
// based on the variant of the Tigera operator's default Installation. The
// open-source Calico distribution uses the same operator and namespaces but
// cannot forward flow logs, so the namespaces alone are not enough.
func IsDeployed(ctx context.Context, logger *zap.Logger, dynamicClient dynamic.Interface) bool {
	logger.Debug("Checking for Calico Enterprise deployment")

	installation, err := dynamicClient.Resource(calicoInstallationGVR).Get(ctx, calicoInstallationName, metav1.GetOptions{})
	if err != nil {
		logger.Debug("Tigera operator Installation not found", zap.Error(err))

		return false
	}

	variant, _, err := unstructured.NestedString(installation.Object, "spec", "variant")
	if err != nil || variant != calicoEnterpriseVariant {
		logger.Debug("Calico installation is not Calico Enterprise", zap.String("variant", variant))

		return false
	}

	logger.Debug("Calico Enterprise installation found")

	return true
}

// RunSyslogCollector runs the TCP listener for Calico Enterprise syslog flows.
// It accepts connections from the LogCollector and processes one flow per
// newline-delimited record. It blocks until the context is canceled.
func (c *Collector) RunSyslogCollector(ctx context.Context) error {
	logger := c.logger.With(
		zap.String("address", c.syslogPort),
	)
	logger.Info("Starting Calico Enterprise syslog collector")

	tlsConfig, err := tls.LoadServerConfig(c.tlsSettings)
	if err != nil {
		logger.Error("Failed to configure syslog TLS", zap.Error(err))

		return err
	}

	c.tlsConfig = tlsConfig

	if tlsConfig == nil {
		logger.Info("No syslog TLS certificate configured; accepting plain-text syslog only")
	} else {
		logger.Info("Syslog TLS certificate configured", zap.Bool("tls_required", c.tlsSettings.Required))
	}

	var listenerConfig net.ListenConfig

	listener, err := listenerConfig.Listen(ctx, "tcp", ":"+c.syslogPort)
	if err != nil {
		logger.Error("Failed to listen on syslog port", zap.Error(err))

		return err
	}

	defer func() {
		// The listener is already closed if the context was canceled.
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			logger.Error("Failed to close listener on syslog port", zap.Error(err))
		}
	}()

	// Unblock the blocking Accept call when the context is canceled.
	go func() {
		<-ctx.Done()

		if err := listener.Close(); err != nil {
			logger.Debug("Error closing listener on context cancellation", zap.Error(err))
		}
	}()

	logger.Info("Calico Enterprise syslog collector listening")

	var acceptDelay time.Duration

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				logger.Info("Context canceled in Calico Enterprise syslog collector")

				return ctx.Err()
			}

			// Back off before retrying so that a persistent error, such as running
			// out of file descriptors, does not spin the CPU and flood the log.
			acceptDelay = nextAcceptDelay(acceptDelay)

			logger.Error("Failed to accept syslog connection", zap.Error(err), zap.Duration("retry_in", acceptDelay))

			select {
			case <-ctx.Done():
				logger.Info("Context canceled in Calico Enterprise syslog collector")

				return ctx.Err()
			case <-time.After(acceptDelay):
			}

			continue
		}

		acceptDelay = 0

		go c.handleConn(ctx, logger, conn)
	}
}

// nextAcceptDelay returns the delay before retrying a failed Accept: it starts
// at calicoMinAcceptDelay and doubles up to calicoMaxAcceptDelay.
func nextAcceptDelay(previous time.Duration) time.Duration {
	if previous == 0 {
		return calicoMinAcceptDelay
	}

	return min(previous*2, calicoMaxAcceptDelay)
}

// prepareConn returns a connection that yields plain syslog records: the TLS
// connection after a successful handshake if the LogCollector started one, or
// the plain connection.
func (c *Collector) prepareConn(ctx context.Context, conn net.Conn) (net.Conn, error) {
	return tls.PrepareServerConn(ctx, conn, tls.ServerConnOptions{
		Config:           c.tlsConfig,
		Required:         c.tlsSettings.Required,
		FirstByteTimeout: c.firstByteTimeout,
		HandshakeTimeout: c.handshakeTimeout,
	})
}

// handleConn reads newline-delimited syslog records from a single LogCollector
// connection and caches each parsed flow.
func (c *Collector) handleConn(ctx context.Context, logger *zap.Logger, conn net.Conn) {
	logger = logger.With(zap.String("remote_addr", conn.RemoteAddr().String()))
	logger.Debug("Accepted syslog connection")

	defer func() {
		if err := conn.Close(); err != nil {
			logger.Debug("Error closing syslog connection", zap.Error(err))
		}
	}()

	syslogConn, err := c.prepareConn(ctx, conn)
	if err != nil {
		logger.Warn("Rejected syslog connection", zap.Error(err))

		return
	}

	scanner := bufio.NewScanner(syslogConn)
	// Records can exceed bufio's 64KiB default token size once labels and policies
	// metadata is attached; enlarge the buffer so long lines aren't truncated.
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), calicoSyslogMaxLineLength)

	warnedAggregated := false

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()
		if line == "" {
			continue
		}

		err := c.processLine(ctx, logger, line)
		if err == nil {
			continue
		}

		if ctx.Err() != nil {
			return
		}

		c.recordDropped()

		if errors.Is(err, ErrAggregatedFlow) && !warnedAggregated {
			// Without this setting every record is dropped, so tell the user once
			// per connection rather than only at debug level.
			logger.Warn("Calico Enterprise is sending aggregated flow logs without IP addresses or source ports, so they are dropped; " +
				"set flowLogsFileAggregationKindForAllowed and flowLogsFileAggregationKindForDenied to 0 in the FelixConfiguration")

			warnedAggregated = true

			continue
		}

		logger.Debug("Skipping syslog record", zap.Error(err))
	}

	// A connection closed mid-record yields a non-EOF scanner error; log it and
	// move on rather than treating a partial trailing line as a flow.
	if err := scanner.Err(); err != nil {
		logger.Debug("Syslog connection scan ended with error", zap.Error(err))
	}
}

// processLine parses a single syslog record and caches the resulting flow.
func (c *Collector) processLine(ctx context.Context, logger *zap.Logger, line string) error {
	flow, err := ParseFlow(line)
	if err != nil {
		return err
	}

	if err := c.flowSink.CacheFlow(ctx, flow); err != nil {
		logger.Error("Failed to cache flow from Calico Enterprise", zap.Error(err))

		return err
	}

	c.flowSink.IncrementFlowsReceived()

	return nil
}

// calicoIPVersion infers the IP version from an address, since flow records do
// not carry an explicit version field.
func calicoIPVersion(ip string) (string, error) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "", ErrInvalidIP
	}

	if parsed.To4() != nil {
		return collector.IPv4, nil
	}

	return collector.IPv6, nil
}
