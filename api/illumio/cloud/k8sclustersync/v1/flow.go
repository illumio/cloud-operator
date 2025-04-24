package k8sclustersyncv1

import "time"

// Flow is a network flow that is collected or exported.
type Flow interface {
	// StartTimestamp is the start timestamp of this flow.
	StartTimestamp() time.Time
	// Key is this flow's flow key. The returned value is Comparable.
	Key() any
}
