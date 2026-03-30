// Copyright 2024 Illumio, Inc. All Rights Reserved.

package main

import (
	"sync"
	"time"
)

// StreamState tracks the state of individual streams.
type StreamState struct {
	Opened           bool
	LastActivity     time.Time
	MessagesReceived int
	KeepalivesRecv   int
}

// EnhancedServerState provides detailed tracking of all server activity.
type EnhancedServerState struct {
	mu sync.RWMutex

	// Legacy fields for backward compatibility
	ConnectionSuccessful bool
	IncorrectCredentials bool
	BadIntialCommit      bool

	// Stream-specific state
	ConfigStream    StreamState
	LogsStream      StreamState
	ResourcesStream StreamState
	FlowsStream     StreamState

	// Auth state
	AuthRequests     int
	OnboardRequests  int
	LastAuthTime     time.Time
	LastOnboardTime  time.Time

	// Resource tracking
	ResourceSnapshotComplete bool
	ResourcesReceived        int
	MutationsReceived        int

	// Flow tracking
	CiliumFlowsReceived    int
	FiveTupleFlowsReceived int
}

// NewEnhancedServerState creates a new state tracker.
func NewEnhancedServerState() *EnhancedServerState {
	return &EnhancedServerState{}
}

// MarkConfigStreamOpened marks the config stream as opened.
func (s *EnhancedServerState) MarkConfigStreamOpened() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ConfigStream.Opened = true
	s.ConfigStream.LastActivity = time.Now()
}

// MarkLogsStreamOpened marks the logs stream as opened.
func (s *EnhancedServerState) MarkLogsStreamOpened() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LogsStream.Opened = true
	s.LogsStream.LastActivity = time.Now()
}

// MarkResourcesStreamOpened marks the resources stream as opened.
func (s *EnhancedServerState) MarkResourcesStreamOpened() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ResourcesStream.Opened = true
	s.ResourcesStream.LastActivity = time.Now()
}

// MarkFlowsStreamOpened marks the flows stream as opened.
func (s *EnhancedServerState) MarkFlowsStreamOpened() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.FlowsStream.Opened = true
	s.FlowsStream.LastActivity = time.Now()
}

// RecordKeepalive records a keepalive for the specified stream.
func (s *EnhancedServerState) RecordKeepalive(stream string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	switch stream {
	case "config":
		s.ConfigStream.KeepalivesRecv++
		s.ConfigStream.LastActivity = now
	case "logs":
		s.LogsStream.KeepalivesRecv++
		s.LogsStream.LastActivity = now
	case "resources":
		s.ResourcesStream.KeepalivesRecv++
		s.ResourcesStream.LastActivity = now
	case "flows":
		s.FlowsStream.KeepalivesRecv++
		s.FlowsStream.LastActivity = now
	}
}

// RecordResourceSnapshot marks resource snapshot as complete.
func (s *EnhancedServerState) RecordResourceSnapshot() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ResourceSnapshotComplete = true
	s.ConnectionSuccessful = true // Legacy compatibility
}

// RecordAuthRequest records an authentication request.
func (s *EnhancedServerState) RecordAuthRequest() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.AuthRequests++
	s.LastAuthTime = time.Now()
}

// RecordOnboardRequest records an onboard request.
func (s *EnhancedServerState) RecordOnboardRequest() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.OnboardRequests++
	s.LastOnboardTime = time.Now()
}

// IsConnectionSuccessful returns whether the connection was successful.
func (s *EnhancedServerState) IsConnectionSuccessful() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ConnectionSuccessful
}

// AllStreamsOpened returns true if all streams have been opened.
func (s *EnhancedServerState) AllStreamsOpened() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ConfigStream.Opened &&
		s.LogsStream.Opened &&
		s.ResourcesStream.Opened
	// Flows stream is optional (depends on flow collector availability)
}

// GetSummary returns a summary of the current state.
func (s *EnhancedServerState) GetSummary() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return map[string]interface{}{
		"connection_successful":     s.ConnectionSuccessful,
		"config_stream_opened":      s.ConfigStream.Opened,
		"logs_stream_opened":        s.LogsStream.Opened,
		"resources_stream_opened":   s.ResourcesStream.Opened,
		"flows_stream_opened":       s.FlowsStream.Opened,
		"resource_snapshot_complete": s.ResourceSnapshotComplete,
		"auth_requests":             s.AuthRequests,
		"onboard_requests":          s.OnboardRequests,
		"resources_received":        s.ResourcesReceived,
		"mutations_received":        s.MutationsReceived,
	}
}
