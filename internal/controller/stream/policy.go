// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// PolicyState holds the desired network policy state received from the server.
// In the future, this will also hold runtime state and run the reconciliation loop.
type PolicyState struct {
	desired map[string]*pb.NetworkPolicyData
}

// NewPolicyState creates a new PolicyState.
func NewPolicyState() *PolicyState {
	return &PolicyState{
		desired: make(map[string]*pb.NetworkPolicyData),
	}
}

// ResetDesired clears and reinitializes the desired state.
// Called when a new stream connection is established and the server sends a fresh snapshot.
func (ps *PolicyState) ResetDesired() {
	ps.desired = make(map[string]*pb.NetworkPolicyData)
}

// SetDesired adds or updates a policy in the desired state.
func (ps *PolicyState) SetDesired(policy *pb.NetworkPolicyData) {
	ps.desired[policy.GetId()] = policy
}

// DeleteDesired removes a policy from the desired state.
func (ps *PolicyState) DeleteDesired(id string) {
	delete(ps.desired, id)
}

// DesiredLen returns the number of policies in the desired state.
func (ps *PolicyState) DesiredLen() int {
	return len(ps.desired)
}
