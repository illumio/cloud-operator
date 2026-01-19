// Copyright 2024 Illumio, Inc. All Rights Reserved.

package k8sclustersyncv1

import (
	"time"
)

var _ Flow = &CalicoFlow{}

// CalicoFlowKey uniquely identifies a Calico flow for deduplication purposes.
type CalicoFlowKey struct {
	SourceName           string
	SourceNamespace      string
	SourceType           string
	DestName             string
	DestNamespace        string
	DestType             string
	DestPort             uint32
	DestServiceName      string
	DestServiceNamespace string
	Proto                string
	Reporter             string
	Action               string
}

// StartTimestamp returns the start time of the flow.
func (flow *CalicoFlow) StartTimestamp() time.Time {
	return flow.GetStartTime().AsTime()
}

// Key returns a comparable key for this flow used for deduplication.
func (flow *CalicoFlow) Key() any {
	if flow == nil {
		return nil
	}

	return CalicoFlowKey{
		SourceName:           flow.GetSourceName(),
		SourceNamespace:      flow.GetSourceNamespace(),
		SourceType:           flow.GetSourceType(),
		DestName:             flow.GetDestName(),
		DestNamespace:        flow.GetDestNamespace(),
		DestType:             flow.GetDestType(),
		DestPort:             flow.GetDestPort(),
		DestServiceName:      flow.GetDestServiceName(),
		DestServiceNamespace: flow.GetDestServiceNamespace(),
		Proto:                flow.GetProto(),
		Reporter:             flow.GetReporter(),
		Action:               flow.GetAction(),
	}
}
