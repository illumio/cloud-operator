// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"bufio"
	"context"
	"errors"
	"net"
	"regexp"
	"strconv"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// Errors for Calico syslog flow parsing.
var (
	ErrCalicoIncompleteFlow = errors.New("ignoring incomplete calico flow, missing five-tuple field")
	ErrCalicoInvalidPort    = errors.New("ignoring calico flow due to unparseable port")
	ErrCalicoUnknownProto   = errors.New("ignoring calico flow due to unknown protocol number")
	ErrCalicoInvalidIP      = errors.New("ignoring calico flow due to unparseable IP address")
)

// calicoSyslogMaxLineLength bounds a single syslog record. Calico Enterprise
// emits one flow per line as key="value" pairs; the decoration metadata
// (policy/tier/rule) can make these long, so we allow a generous max-token
// size to avoid bufio.Scanner truncating a record.
const calicoSyslogMaxLineLength = 1024 * 1024

// reCalicoKeyValue matches both quoted (key="value") and bare (key=value)
// pairs emitted by Felix's syslog flow reporter. Calico sorts fields
// alphabetically and may reorder across versions, so the parser keys by name
// rather than position.
var reCalicoKeyValue = regexp.MustCompile(`(\w+)=(?:"([^"]*)"|(\S+))`)

// CalicoFlow represents a network flow captured from a Calico Enterprise
// (Felix) syslog flow log record.
type CalicoFlow struct {
	SourceIP        string
	DestinationIP   string
	SourcePort      uint16
	DestinationPort uint16
	Protocol        string
	IPVersion       string
	StartTimestamp  *timestamppb.Timestamp
}

// CalicoCollector collects flows from Calico Enterprise via the Felix syslog
// flow reporter. Felix dials the operator over plain TCP and emits records
// using RFC 6587 section 3.4.1 non-transparent (newline-terminated) framing.
type CalicoCollector struct {
	logger     *zap.Logger
	syslogPort string
	flowSink   FlowSink
}

// NewCalicoCollector creates a new Calico Enterprise syslog flow collector.
func NewCalicoCollector(logger *zap.Logger, syslogPort string, flowSink FlowSink) *CalicoCollector {
	return &CalicoCollector{
		logger:     logger,
		syslogPort: syslogPort,
		flowSink:   flowSink,
	}
}

// IsCalicoEnterpriseDeployed checks for the presence of the Calico Enterprise
// (Tigera) namespace. Calico Enterprise installs its components into
// calico-system; the open-source Calico CNI does not emit syslog flow logs, so
// the namespace check gates on the Enterprise distribution.
func IsCalicoEnterpriseDeployed(ctx context.Context, logger *zap.Logger, calicoNamespace string, clientset kubernetes.Interface) bool {
	logger.Debug("Checking for Calico Enterprise deployment")

	_, err := clientset.CoreV1().Namespaces().Get(ctx, calicoNamespace, metav1.GetOptions{})
	if err != nil {
		logger.Debug("Calico Enterprise namespace not found", zap.Error(err))

		return false
	}

	logger.Debug("Calico Enterprise namespace found")

	return true
}

// RunSyslogCollector runs the TCP listener for Calico Enterprise syslog flows.
// It accepts connections from Felix on each node and processes one flow per
// newline-delimited record. It blocks until the context is canceled.
func (c *CalicoCollector) RunSyslogCollector(ctx context.Context) error {
	logger := c.logger.With(
		zap.String("address", c.syslogPort),
	)
	logger.Info("Starting Calico Enterprise syslog collector")

	var listenerConfig net.ListenConfig

	listener, err := listenerConfig.Listen(ctx, "tcp", ":"+c.syslogPort)
	if err != nil {
		logger.Error("Failed to listen on syslog port", zap.Error(err))

		return err
	}

	defer func() {
		if err := listener.Close(); err != nil {
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

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				logger.Info("Context canceled in Calico Enterprise syslog collector")

				return ctx.Err()
			default:
				logger.Error("Failed to accept syslog connection", zap.Error(err))

				continue
			}
		}

		go c.handleConn(ctx, logger, conn)
	}
}

// handleConn reads newline-delimited syslog records from a single Felix
// connection and caches each parsed flow.
func (c *CalicoCollector) handleConn(ctx context.Context, logger *zap.Logger, conn net.Conn) {
	logger = logger.With(zap.String("remote_addr", conn.RemoteAddr().String()))
	logger.Debug("Accepted syslog connection")

	defer func() {
		if err := conn.Close(); err != nil {
			logger.Debug("Error closing syslog connection", zap.Error(err))
		}
	}()

	scanner := bufio.NewScanner(conn)
	// Felix records can exceed bufio's 64KiB default token size once decoration
	// metadata is attached; enlarge the buffer so long lines aren't truncated.
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), calicoSyslogMaxLineLength)

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

		if err := c.processLine(ctx, logger, line); err != nil {
			logger.Debug("Skipping syslog record", zap.Error(err))
		}
	}

	// A connection closed mid-record yields a non-EOF scanner error; log it and
	// move on rather than treating a partial trailing line as a flow.
	if err := scanner.Err(); err != nil {
		logger.Debug("Syslog connection scan ended with error", zap.Error(err))
	}
}

// processLine parses a single syslog record and caches the resulting flow,
// reusing the same downstream path as the OVN-K and Falco collectors.
func (c *CalicoCollector) processLine(ctx context.Context, logger *zap.Logger, line string) error {
	flow, err := ParseSyslogFlow(line)
	if err != nil {
		return err
	}

	layer3Message, err := CreateLayer3Message(flow.SourceIP, flow.DestinationIP, flow.IPVersion)
	if err != nil {
		return err
	}

	layer4Message, err := CreateLayer4Message(flow.Protocol, uint32(flow.SourcePort), uint32(flow.DestinationPort), flow.IPVersion)
	if err != nil {
		return err
	}

	convertCalicoFlow := &pb.FiveTupleFlow{
		Layer3: layer3Message,
		Layer4: layer4Message,
		Ts: &pb.FiveTupleFlow_Timestamp{
			Timestamp: flow.StartTimestamp,
		},
	}

	if err := c.flowSink.CacheFlow(ctx, convertCalicoFlow); err != nil {
		logger.Error("Failed to cache flow from Calico Enterprise", zap.Error(err))

		return err
	}

	c.flowSink.IncrementFlowsReceived()

	return nil
}

// ParseSyslogFlow parses a single Calico Enterprise (Felix) syslog flow record
// into a CalicoFlow. Records look like:
//
//	<14>2026-06-29T22:45:59Z minikube calico-felix[51097]: INFO <nil> <nil>:  action="allow" srcIP="10.244.120.78" srcPort="48710" dstIP="10.244.120.74" dstPort="8080" proto="6" ... updateType=report
//
// Fields are tokenized by key name (not offset) since Felix sorts them
// alphabetically and the ordering can change across versions. The syslog
// <PRI>/timestamp/host/tag header is ignored; the five-tuple is taken from the
// key="value" body.
func ParseSyslogFlow(line string) (CalicoFlow, error) {
	flow := CalicoFlow{}

	var (
		haveSrcIP, haveDstIP     bool
		haveSrcPort, haveDstPort bool
		haveProto                bool
	)

	for _, match := range reCalicoKeyValue.FindAllStringSubmatch(line, -1) {
		key := match[1]
		// Group 2 is the quoted value, group 3 the bare value; exactly one is set.
		value := match[2]
		if value == "" {
			value = match[3]
		}

		switch key {
		case "srcIP":
			flow.SourceIP = value
			haveSrcIP = true
		case "dstIP":
			flow.DestinationIP = value
			haveDstIP = true
		case "srcPort":
			port, err := parseCalicoPort(value)
			if err != nil {
				return CalicoFlow{}, err
			}

			flow.SourcePort = port
			haveSrcPort = true
		case "dstPort":
			port, err := parseCalicoPort(value)
			if err != nil {
				return CalicoFlow{}, err
			}

			flow.DestinationPort = port
			haveDstPort = true
		case "proto":
			proto, err := parseCalicoProtocol(value)
			if err != nil {
				return CalicoFlow{}, err
			}

			flow.Protocol = proto
			haveProto = true
		}
	}

	if !haveSrcIP || !haveDstIP || !haveSrcPort || !haveDstPort || !haveProto {
		return CalicoFlow{}, ErrCalicoIncompleteFlow
	}

	ipVersion, err := calicoIPVersion(flow.SourceIP)
	if err != nil {
		return CalicoFlow{}, err
	}

	flow.IPVersion = ipVersion
	flow.StartTimestamp = parseCalicoTimestamp(line)

	return flow, nil
}

// parseCalicoPort parses a string-encoded port into a uint16.
func parseCalicoPort(value string) (uint16, error) {
	port, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return 0, ErrCalicoInvalidPort
	}

	return uint16(port), nil
}

// parseCalicoProtocol maps a Felix numeric IANA protocol number (string-encoded,
// e.g. "6"/"17") to the protocol name expected by CreateLayer4Message.
func parseCalicoProtocol(value string) (string, error) {
	switch value {
	case "1":
		return ICMP, nil
	case "6":
		return TCP, nil
	case "17":
		return UDP, nil
	case "132":
		return SCTP, nil
	default:
		return "", ErrCalicoUnknownProto
	}
}

// calicoIPVersion infers the IP version from an address, since Felix flow
// records do not carry an explicit version field.
func calicoIPVersion(ip string) (string, error) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "", ErrCalicoInvalidIP
	}

	if parsed.To4() != nil {
		return IPv4, nil
	}

	return IPv6, nil
}

// calicoSyslogTimestampFormat is the RFC3339 timestamp emitted in the syslog
// header (e.g. "2026-06-29T22:45:59Z").
const calicoSyslogTimestampFormat = time.RFC3339

// reCalicoTimestamp extracts the RFC3339 timestamp that follows the syslog
// <PRI> at the start of each record.
var reCalicoTimestamp = regexp.MustCompile(`^<\d+>(\S+)`)

// parseCalicoTimestamp extracts the record timestamp from the syslog header.
// A missing or malformed timestamp is non-fatal: the flow is still useful, so
// this returns nil (CloudSecure stamps on receipt) rather than dropping it.
func parseCalicoTimestamp(line string) *timestamppb.Timestamp {
	match := reCalicoTimestamp.FindStringSubmatch(line)
	if match == nil {
		return nil
	}

	ts, err := time.Parse(calicoSyslogTimestampFormat, match[1])
	if err != nil {
		return nil
	}

	return timestamppb.New(ts)
}
