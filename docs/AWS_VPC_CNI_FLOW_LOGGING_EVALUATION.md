# AWS VPC CNI Flow Logging Evaluation

**Document Version:** 1.1  
**Date:** April 10, 2026  
**Status:** Technical Evaluation  
**Jira Ticket:** CLOUD-16362  
**Authors:** Cloud Containers Team  
**Reviewers:** Nick Sappa, Kunal Gandhi, Romain Lenglet, Ryan Ericson

---

## Table of Contents

1. [Executive Summary](#executive-summary)
2. [Background](#background)
3. [Research Methodology](#research-methodology)
4. [Research Findings](#research-findings)
5. [Technical Architecture](#technical-architecture)
6. [Design Decision](#design-decision)
7. [Functional Comparison](#functional-comparison)
8. [Implementation Details](#implementation-details)
9. [Testing Guide](#testing-guide)
10. [Gaps and Limitations](#gaps-and-limitations)
11. [Recommendation](#recommendation)
12. [Appendices](#appendices)

---

## Executive Summary

This document evaluates AWS VPC CNI flow logging capabilities as a potential replacement for Falco-based flow collection in Illumio CloudSecure's Kubernetes traffic visibility.

### Key Findings

| Criteria | AWS VPC CNI | Falco | Verdict |
|----------|-------------|-------|---------|
| Flow data (5-tuple) | Yes | Yes | Parity |
| K8s metadata in logs | No | No | Parity (backend decorates) |
| Direction (ingress/egress) | Yes | No | VPC CNI better |
| Verdict (allow/deny) | Yes | No | VPC CNI better |
| EKS Auto Mode | Not supported | Supported | Falco better |
| Fargate | Not supported | Supported | Falco better |
| Non-EKS clusters | N/A | Supported | Falco better |
| NetworkPolicy required | No (with debug logs) | No | Parity |
| Extra deployment needed | No (native to EKS) | Yes (Falco DaemonSet) | VPC CNI better |

### Recommendation

**Hybrid approach:** Implement AWS VPC CNI as a flow collector option for standard EKS clusters while maintaining Falco as the fallback for unsupported configurations (Auto Mode, Fargate, non-EKS).

### Decision Tree

```
                    Is cluster EKS?
                          |
            +-------------+-------------+
            |                           |
           YES                          NO
            |                           |
    Is VPC CNI >= v1.14?          Is Cilium available?
            |                           |
      +-----+-----+               +-----+-----+
      |           |               |           |
     YES          NO             YES          NO
      |           |               |           |
Is Auto Mode    Use            Use         Use
or Fargate?    Falco        Cilium       Falco
      |
  +---+---+
  |       |
 YES      NO
  |       |
Use     Use
Falco   VPC CNI
```

---

## Background

### Current State

Today, Illumio CloudSecure uses three flow collectors depending on the cluster's CNI:

| Collector | Use Case | Collection Method |
|-----------|----------|-------------------|
| **Cilium/Hubble** | GKE Dataplane V2, Cilium clusters | gRPC stream (pull) |
| **OVN-Kubernetes** | OpenShift with OVN-K | IPFIX/UDP (push) |
| **Falco** | Default fallback (including EKS) | HTTP POST (push) |

### Historical Context: Cilium CNI Chaining on EKS

Previously, for EKS customers who needed both NetworkPolicy enforcement and flow observability, there were limited options:

**Before VPC CNI v1.14 (Pre-2024):**

```
┌─────────────────────────────────────────────────────────────────┐
│  AWS VPC CNI (legacy) - Limited Capabilities                    │
│                                                                  │
│  - IPAM: Yes (pods get VPC IPs)                                │
│  - NetworkPolicy: NO                                            │
│  - Flow observability: NO                                       │
│                                                                  │
│  To get NetworkPolicy + observability, customers had to:        │
│    Option 1: Cilium CNI Chaining                               │
│    Option 2: Replace AWS CNI entirely with Cilium              │
└─────────────────────────────────────────────────────────────────┘
```

**Cilium CNI Chaining Mode:**

Cilium could be installed in "chaining mode" alongside AWS VPC CNI:
- AWS CNI handles IPAM (pods keep VPC IPs for AWS integrations)
- Cilium handles NetworkPolicy enforcement via eBPF
- Hubble provides flow observability

```
┌─────────────────────────────────────────────────────────────────┐
│  Cilium CNI Chaining (legacy approach)                          │
│                                                                  │
│  Pod Traffic Flow:                                              │
│    AWS VPC CNI (IPAM) ──► Cilium (eBPF) ──► Network            │
│                                                                  │
│  Benefits:                                                      │
│    - Keep VPC IPs (required for AWS services)                  │
│    - NetworkPolicy enforcement                                  │
│    - Hubble flow observability (our existing collector works)  │
│                                                                  │
│  Drawbacks:                                                     │
│    - Must deploy Cilium DaemonSet                              │
│    - Additional complexity                                      │
│    - Two CNI layers to manage                                  │
└─────────────────────────────────────────────────────────────────┘
```

For customers who didn't want Cilium chaining, the only option for flow collection was Falco.

### What Changed: VPC CNI v1.14+ (2024)

AWS introduced native NetworkPolicy support in VPC CNI v1.14:

```
┌─────────────────────────────────────────────────────────────────┐
│  AWS VPC CNI v1.14+ - Native Capabilities                       │
│                                                                  │
│  - IPAM: Yes (pods get VPC IPs)                                │
│  - NetworkPolicy: YES (native eBPF enforcement)                │
│  - Flow observability: YES (aws-eks-nodeagent logs)            │
│                                                                  │
│  No Cilium chaining needed for basic use cases!                │
└─────────────────────────────────────────────────────────────────┘
```

**Key insight from Ryan Ericson:**
> NetworkPolicy enforcement is NOT required to get flow logs. Simply enabling `enableNetworkPolicy: true` in the VPC CNI addon deploys the aws-eks-nodeagent container, which logs all flows regardless of whether any NetworkPolicy CRDs exist.

### Evolution Summary

| Era | NetworkPolicy | Flow Observability | Requires |
|-----|--------------|-------------------|----------|
| VPC CNI < v1.14 | No | No | Cilium chaining or Falco |
| VPC CNI >= v1.14 | Yes (native) | Yes (native) | Nothing extra! |

### Problem Statement

Despite VPC CNI's new capabilities, we still use Falco for EKS because:
- We hadn't evaluated VPC CNI's native flow logging
- Falco was the established fallback

Falco requires:
- Deployment of Falco DaemonSet on every node
- Kernel module or eBPF privileges
- Customer to manage additional component

AWS VPC CNI with Network Policy Agent already provides flow logging natively on EKS, potentially eliminating the need for Falco on standard EKS clusters.

### When Cilium Chaining is Still Valuable

Cilium chaining remains beneficial for advanced use cases:
- L7 (application layer) network policies
- Service mesh integration
- More detailed flow metadata (pod names, labels in flows)
- Cilium-specific features (Cluster Mesh, etc.)

For basic NetworkPolicy + flow observability, VPC CNI native is now sufficient.

---

## Research Methodology

### Phase 1: Documentation Review
- AWS VPC CNI documentation
- EKS Network Policy documentation
- aws-network-policy-agent GitHub repository

### Phase 2: Source Code Analysis
- Analyzed aws-network-policy-agent source code
- Identified log format and fields
- Understood eBPF ring buffer structure

### Phase 3: Architecture Analysis
- Mapped aws-node DaemonSet architecture
- Identified aws-eks-nodeagent container
- Analyzed log collection patterns

### Phase 4: Team Discussion
- Discussed with Romain Lenglet on collection approach
- Confirmed polling vs streaming decision
- Validated single stream to CloudSecure requirement

### Phase 5: Implementation
- Built VPC CNI flow collector
- Integrated with existing flow pipeline
- Added unit tests

---

## Research Findings

### Finding 1: aws-node is a DaemonSet (One Per Node)

The `aws-node` DaemonSet runs on every node in an EKS cluster. Each pod contains:
- `aws-node` container: CNI plugin for pod networking
- `aws-eks-nodeagent` container: Network Policy Agent with flow logging

```
+------------------+     +------------------+     +------------------+
|     Node 1       |     |     Node 2       |     |     Node 3       |
| +-------------+  |     | +-------------+  |     | +-------------+  |
| | aws-node    |  |     | | aws-node    |  |     | | aws-node    |  |
| | pod         |  |     | | pod         |  |     | | pod         |  |
| | +---------+ |  |     | | +---------+ |  |     | | +---------+ |  |
| | |nodeagent| |  |     | | |nodeagent| |  |     | | |nodeagent| |  |
| | |(logs)   | |  |     | | |(logs)   | |  |     | | |(logs)   | |  |
| | +---------+ |  |     | | +---------+ |  |     | | +---------+ |  |
| +-------------+  |     | +-------------+  |     | +-------------+  |
+------------------+     +------------------+     +------------------+
```

**Implication:** No cluster-level aggregation point (unlike Hubble Relay for Cilium).

### Finding 2: NetworkPolicy NOT Required for Flow Logging

Key finding from Ryan Ericson's research:

> "Network Policy is **not** required for collecting flow logs. The aws-eks-nodeagent container is deployed as part of VPC CNI >= v1.14 when `enableNetworkPolicy: true` is set in the addon configuration. Flow logs are generated regardless of whether any NetworkPolicy resources exist."

To get ACCEPT flows (not just DENY), enable debug logging:
```bash
kubectl set env daemonset aws-node -n kube-system -c aws-eks-nodeagent ENABLE_POLICY_EVENT_LOGS=true
```

### Finding 3: VPC CNI Flow Log Format

The aws-eks-nodeagent logs flows in JSON format:

```json
{
  "level": "info",
  "ts": "2024-09-23T12:36:53.562Z",
  "logger": "ebpf-client",
  "caller": "events/events.go:193",
  "msg": "Flow Info: ",
  "Src IP": "10.0.141.167",
  "Src Port": 39197,
  "Dest IP": "172.20.0.10",
  "Dest Port": 53,
  "Proto": "TCP",
  "Verdict": "ACCEPT"
}
```

**Available fields:**
| Field | Type | Description |
|-------|------|-------------|
| `Src IP` | string | Source IP address |
| `Src Port` | uint32 | Source port (0 for ICMP) |
| `Dest IP` | string | Destination IP address |
| `Dest Port` | uint32 | Destination port (0 for ICMP) |
| `Proto` | string | TCP, UDP, ICMP, SCTP, UNKNOWN |
| `Verdict` | string | ACCEPT, DENY |

**Note:** Direction (ingress/egress) is available in the internal eBPF structure but not currently exposed in the JSON logs.

### Finding 4: Platform Limitations

| Platform | VPC CNI Flow Logging | Reason |
|----------|---------------------|--------|
| EKS Standard | Supported | Full access to aws-node DaemonSet |
| EKS Auto Mode | **Not Supported** | aws-node DaemonSet excluded via anti-affinity |
| EKS Fargate | Not Supported | No DaemonSets on Fargate |
| Self-managed K8s | Not Supported | VPC CNI specific to EKS |

#### EKS Auto Mode - Deep Dive

**Discovery:** During testing on an EKS Auto Mode cluster (`pavanekstestcluster`), we found that:

1. VPC CNI addon installs successfully with `enableNetworkPolicy: true`
2. `aws-node` DaemonSet is created but shows `DESIRED: 0`
3. No `aws-node` pods are scheduled on any nodes

**Root Cause:** The `aws-node` DaemonSet has an explicit node anti-affinity:

```yaml
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
      - matchExpressions:
        - key: eks.amazonaws.com/compute-type
          operator: NotIn
          values:
          - fargate
          - hybrid
          - auto    # <-- Excludes Auto Mode nodes
```

**How to Identify Auto Mode:**

```bash
# Check node labels
kubectl get nodes -o jsonpath='{.items[0].metadata.labels}' | grep compute-type
# Auto Mode: eks.amazonaws.com/compute-type=auto

# Check for managed node groups (empty = likely Auto Mode)
aws eks list-nodegroups --cluster-name <cluster>
# Auto Mode: {"nodegroups": []}

# Check for Karpenter labels
kubectl get nodes --show-labels | grep karpenter
# Auto Mode: karpenter.sh/* labels present

# Check DaemonSet status
kubectl get daemonset aws-node -n kube-system
# Auto Mode: DESIRED=0, CURRENT=0
```

**Implication:** VPC CNI flow collector cannot be used on Auto Mode clusters. Use Falco as fallback.

### Finding 5: Backend IP Decoration Already Exists

The backend (k8sflowprocessor) already performs IP-to-resource decoration:

```java
// IPDecorationProcessor.java
Map<String,ResourceDecoration> ipToDecorationMap = 
    inventory.resolveIPsToDecorations(tenantId, ipsToDecorate, clusterId, flowTimestamp);
decoratedKey.setSrcDecoration(srcDecoration);
decoratedKey.setDstDecoration(dstDecoration);
```

**Implication:** VPC CNI flows (IP-only) will work with the existing backend pipeline - no changes needed.

---

## Technical Architecture

### Current Flow Collection Architecture

```
+------------------------------------------------------------------+
|                         EKS Cluster                               |
|                                                                   |
|  +------------------+  +------------------+  +------------------+ |
|  |     Node 1       |  |     Node 2       |  |     Node 3       | |
|  | +-------------+  |  | +-------------+  |  | +-------------+  | |
|  | | aws-node    |  |  | | aws-node    |  |  | | aws-node    |  | |
|  | | +---------+ |  |  | | +---------+ |  |  | | +---------+ |  | |
|  | | |nodeagent| |  |  | | |nodeagent| |  |  | | |nodeagent| |  | |
|  | | |(logs)   | |  |  | | |(logs)   | |  |  | | |(logs)   | |  | |
|  | | +---------+ |  |  | | +---------+ |  |  | | +---------+ |  | |
|  | +------+------+  |  | +------+------+  |  | +------+------+  | |
|  +--------|----------+  +--------|----------+  +--------|--------+ |
|           |                      |                      |         |
|           +----------------------+----------------------+         |
|                                  |                                |
|                                  v                                |
|                    +---------------------------+                  |
|                    |     cloud-operator        |                  |
|                    |     (single pod)          |                  |
|                    |                           |                  |
|                    |  +---------------------+  |                  |
|                    |  | VPC CNI Collector   |  |                  |
|                    |  | (polls K8s logs API)|  |                  |
|                    |  +----------+----------+  |                  |
|                    |             |             |                  |
|                    |             v             |                  |
|                    |  +---------------------+  |                  |
|                    |  |     FlowCache       |  |                  |
|                    |  | (dedup + batch)     |  |                  |
|                    |  +----------+----------+  |                  |
|                    |             |             |                  |
|                    +-------------|-------------+                  |
|                                  |                                |
+----------------------------------|--------------------------------+
                                   |
                                   | gRPC Stream (single)
                                   v
                    +---------------------------+
                    |      CloudSecure          |
                    |  +---------------------+  |
                    |  | k8sflowprocessor    |  |
                    |  | (IP decoration)     |  |
                    |  +---------------------+  |
                    +---------------------------+
```

### Polling Architecture (Decided with Romain)

```
+------------------------------------------------------------------+
|  cloud-operator                                                   |
|                                                                   |
|  +------------------------------------------------------------+  |
|  |  VPC CNI Collector - Polling Loop                          |  |
|  |                                                            |  |
|  |  Every X seconds:                                          |  |
|  |    1. List aws-node pods (label: k8s-app=aws-node)        |  |
|  |    2. For each pod:                                        |  |
|  |       - GetLogs(pod, container=aws-eks-nodeagent,         |  |
|  |                 sinceTime=lastPollTime)                    |  |
|  |       - Parse JSON flow logs                               |  |
|  |       - Add to FlowCache                                   |  |
|  |    3. Update lastPollTime per pod                         |  |
|  |    4. Cleanup stale pod entries                           |  |
|  |                                                            |  |
|  +-----------------------------+------------------------------+  |
|                                |                                  |
|                                v                                  |
|  +-----------------------------+------------------------------+  |
|  |  FlowCache (existing)                                      |  |
|  |  - Deduplication                                           |  |
|  |  - Batching                                                |  |
|  |  - Single stream to CloudSecure                           |  |
|  +------------------------------------------------------------+  |
+------------------------------------------------------------------+
```

---

## Design Decision

### Streaming vs Polling

| Approach | Pros | Cons |
|----------|------|------|
| **Streaming** (Follow: true) | Real-time, instant delivery | N open connections (one per node), complex connection management |
| **Polling** (Follow: false) | Simpler, one loop | 5-10s delay, more API calls per cycle |

**Decision: Polling** (discussed with Romain Lenglet)

Rationale:
1. Simpler implementation - no need to manage N persistent connections
2. Easier to handle node additions/removals
3. Acceptable delay for flow data (5-10 seconds)
4. Single stream to CloudSecure maintained

### Why Not Push Model (Like Falco)?

Push model would require deploying a DaemonSet to forward logs to the operator. If we're deploying a DaemonSet anyway, we might as well use Falco. The value of VPC CNI is that it requires **no additional deployment**.

---

## Functional Comparison

### Flow Data Comparison

| Field | VPC CNI | Falco | Cilium |
|-------|---------|-------|--------|
| Source IP | Yes | Yes | Yes |
| Destination IP | Yes | Yes | Yes |
| Source Port | Yes | Yes | Yes |
| Destination Port | Yes | Yes | Yes |
| Protocol | Yes | Yes | Yes |
| Timestamp | Yes | Yes | Yes |
| Direction | Yes (internal) | No | Yes |
| Verdict | Yes | No | Yes |
| K8s Labels | No | No | Yes |
| Pod Name | No | No | Yes |
| Namespace | No | No | Yes |

### Operational Comparison

| Aspect | VPC CNI | Falco |
|--------|---------|-------|
| Deployment | None (native) | DaemonSet required |
| Privileges | None (log reading) | Kernel/eBPF |
| Maintenance | AWS managed | Customer managed |
| Resource Usage | Minimal | Medium |
| EKS Standard | Yes | Yes |
| EKS Auto Mode | No | Yes |
| EKS Fargate | No | Yes |
| Non-EKS | No | Yes |

---

## Implementation Details

### Files Created

```
internal/controller/stream/flows/vpccni/
├── detect.go        # VPC CNI detection logic
├── detect_test.go   # Detection tests
├── factory.go       # FlowCollectorFactory implementation
└── client.go        # Polling client (K8s logs API)

internal/controller/collector/
├── vpccni.go        # VPC CNI flow log parser
└── vpccni_test.go   # Parser tests
```

### Files Modified

```
api/illumio/cloud/k8sclustersync/v1/
├── k8s_info.proto   # Added FLOW_COLLECTOR_VPC_CNI = 5
└── k8s_info.pb.go   # Generated enum value

cmd/main.go                                # Added env vars
internal/controller/stream/flows/flows.go  # Added VPC CNI selection
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ENABLE_VPC_CNI_FLOWS` | `false` | Enable VPC CNI flow collector |
| `VPC_CNI_POLL_INTERVAL` | `5s` | Polling interval for logs |

### Flow Collector Selection Order

```go
func DetermineFlowCollector(ctx, config) (FlowCollector, Factory) {
    // 1. Cilium/Hubble (if available)
    if isCiliumAvailable(ctx, ...) {
        return FLOW_COLLECTOR_CILIUM, &cilium.Factory{...}
    }
    
    // 2. OVN-Kubernetes (if available)
    if isOVNKDeployed(ctx, ...) {
        return FLOW_COLLECTOR_OVNK, &ovnk.Factory{...}
    }
    
    // 3. VPC CNI (if enabled and detected)
    if config.EnableVPCCNI && vpccni.IsVPCCNIAvailable(ctx, ...) {
        return FLOW_COLLECTOR_VPC_CNI, &vpccni.Factory{...}
    }
    
    // 4. Falco (fallback)
    return FLOW_COLLECTOR_FALCO, &falco.Factory{...}
}
```

### Detection Logic

```go
func IsVPCCNIAvailable(ctx, logger, k8sClient) bool {
    // List aws-node pods
    pods, err := k8sClient.CoreV1().Pods("kube-system").List(ctx, 
        metav1.ListOptions{LabelSelector: "k8s-app=aws-node", Limit: 1})
    
    if err != nil || len(pods.Items) == 0 {
        return false
    }
    
    // Check for aws-eks-nodeagent container
    for _, container := range pods.Items[0].Spec.Containers {
        if container.Name == "aws-eks-nodeagent" {
            return true
        }
    }
    return false
}
```

### Polling Client

```go
func (c *vpccniClient) Run(ctx context.Context) error {
    ticker := time.NewTicker(c.pollInterval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-ticker.C:
            c.pollAllPods(ctx)
        }
    }
}

func (c *vpccniClient) pollAllPods(ctx context.Context) error {
    // 1. List aws-node pods
    pods, _ := c.k8sClient.CoreV1().Pods("kube-system").List(ctx,
        metav1.ListOptions{LabelSelector: "k8s-app=aws-node"})
    
    // 2. Get logs from each pod since last poll
    for _, pod := range pods.Items {
        sinceTime := c.lastPollTime[pod.Name]
        logs, _ := c.getLogsFromPod(ctx, pod.Name, sinceTime)
        c.lastPollTime[pod.Name] = time.Now()
        c.parseLogs(ctx, logs, pod.Name)
    }
    
    // 3. Cleanup stale pod entries
    c.cleanupStalePods(pods)
    return nil
}
```

---

## Testing Guide

### Prerequisites

1. AWS CLI installed and configured
2. kubectl configured for EKS cluster
3. EKS cluster with VPC CNI >= v1.14

### Step 1: Verify VPC CNI Version

```bash
# Check VPC CNI addon version
aws eks describe-addon \
  --cluster-name <cluster-name> \
  --addon-name vpc-cni \
  --query 'addon.addonVersion'

# Should be v1.14.0 or higher
```

### Step 2: Enable Network Policy Agent

```bash
# Update VPC CNI addon with NetworkPolicy enabled
aws eks update-addon \
  --cluster-name <cluster-name> \
  --addon-name vpc-cni \
  --configuration-values '{"enableNetworkPolicy":"true"}' \
  --resolve-conflicts OVERWRITE

# Wait for addon to be active
aws eks wait addon-active \
  --cluster-name <cluster-name> \
  --addon-name vpc-cni
```

### Step 3: Enable Flow Logging (ACCEPT flows)

```bash
# Enable policy event logs to see ACCEPT flows (not just DENY)
kubectl set env daemonset aws-node -n kube-system \
  -c aws-eks-nodeagent ENABLE_POLICY_EVENT_LOGS=true

# Wait for rollout
kubectl rollout status daemonset/aws-node -n kube-system
```

### Step 4: Verify aws-eks-nodeagent Container

```bash
# Check that aws-eks-nodeagent container exists
kubectl get pods -n kube-system -l k8s-app=aws-node -o jsonpath='{.items[0].spec.containers[*].name}'

# Should include: aws-node aws-eks-nodeagent
```

### Step 5: Generate Test Traffic

```bash
# Deploy test pods
kubectl run client --image=busybox --restart=Never -- sleep 3600
kubectl run server --image=nginx --restart=Never --port=80

# Wait for pods
kubectl wait --for=condition=Ready pod/client pod/server

# Generate traffic
kubectl exec client -- wget -qO- server
```

### Step 6: Verify Flow Logs

```bash
# Check aws-eks-nodeagent logs for flow info
kubectl logs -n kube-system -l k8s-app=aws-node \
  -c aws-eks-nodeagent --tail=50 | grep "Flow Info"

# Expected output:
# {"level":"info","ts":"...","logger":"ebpf-client","msg":"Flow Info: ",
#  "Src IP":"10.0.x.x","Src Port":xxxxx,"Dest IP":"10.0.x.x",
#  "Dest Port":80,"Proto":"TCP","Verdict":"ACCEPT"}
```

### Step 7: Deploy Operator with VPC CNI Enabled

```bash
# Set environment variables in operator deployment
kubectl set env deployment/cloud-operator -n illumio-cloud \
  ENABLE_VPC_CNI_FLOWS=true \
  VPC_CNI_POLL_INTERVAL=5s

# Check operator logs
kubectl logs -n illumio-cloud -l app=cloud-operator --tail=100 | grep -i "vpc\|flow"
```

---

## Gaps and Limitations

### Technical Limitations

| Limitation | Impact | Mitigation |
|------------|--------|------------|
| Log buffer overflow | High traffic may overflow K8s log buffer (~10MB) | Adjust poll interval, increase kubelet log size |
| Operator restart gap | Flows lost during restart (no offset tracking) | Accept some data loss, restart quickly |
| API server load | N pods = N API calls per poll cycle | Acceptable for most clusters (<100 nodes) |
| Log format changes | AWS may change format | Graceful parsing, version detection |

### Platform Limitations

| Platform | Support | Alternative |
|----------|---------|-------------|
| EKS Standard | Yes | - |
| EKS Auto Mode | No | Use Falco |
| EKS Fargate | No | Use Falco |
| Non-EKS | No | Use Falco or Cilium |

### Data Limitations

| Data | Available | Notes |
|------|-----------|-------|
| Direction | Internal only | Not exposed in JSON logs currently |
| Policy name | No | Only verdict (ACCEPT/DENY) |
| K8s metadata | No | Backend decorates via IP |

---

## Recommendation

### Primary Recommendation

Implement VPC CNI flow collector as an **optional** collector for standard EKS clusters:

1. **Enable via environment variable** (`ENABLE_VPC_CNI_FLOWS=true`)
2. **Auto-detect** aws-eks-nodeagent container
3. **Fall back to Falco** if not available or not enabled

### EKS Flow Collector Decision Guide

```
┌─────────────────────────────────────────────────────────────────┐
│  EKS Customer Decision Guide                                     │
│                                                                  │
│  Q: Do you need L7 policies, service mesh, or Cilium features?  │
│     │                                                            │
│     ├── YES ──► Use Cilium CNI Chaining                         │
│     │           (Hubble flow collection - existing code works)  │
│     │                                                            │
│     └── NO ──► Use VPC CNI Native                               │
│                (VPC CNI flow collection - new implementation)   │
│                                                                  │
│  Q: Is it EKS Auto Mode or Fargate?                             │
│     │                                                            │
│     └── YES ──► Use Falco (VPC CNI not accessible)              │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### Configuration Matrix

| Cluster Type | CNI Setup | Recommended Collector | Configuration |
|--------------|-----------|----------------------|---------------|
| EKS Standard | VPC CNI native | VPC CNI | `ENABLE_VPC_CNI_FLOWS=true` |
| EKS Standard | Cilium chaining | Cilium/Hubble | Auto-detected |
| EKS Auto Mode | AWS managed | Falco | Default |
| EKS Fargate | AWS managed | Falco | Default |
| GKE | Cilium/Dataplane V2 | Cilium/Hubble | Auto-detected |
| OpenShift | OVN-K | OVN-K | Auto-detected |
| Other K8s | Varies | Falco | Default fallback |

### VPC CNI vs Cilium Chaining Comparison

| Aspect | VPC CNI Native | Cilium Chaining |
|--------|----------------|-----------------|
| Deployment | Nothing extra | Cilium DaemonSet |
| NetworkPolicy | L3/L4 only | L3/L4 + L7 |
| Flow metadata | IP + ports only | IP + ports + labels + pod names |
| Maintenance | AWS managed | Customer managed |
| Complexity | Simple | Additional layer |
| Service mesh | No | Yes (optional) |
| Cluster mesh | No | Yes |

**Recommendation:**
- **New EKS clusters:** Start with VPC CNI native (simpler, no extra deployment)
- **Existing Cilium chaining:** Keep using Hubble (richer flow data)
- **Advanced needs:** Cilium chaining (L7 policies, service mesh)

### Benefits of VPC CNI Native

1. **No additional deployment** - Uses native EKS capability
2. **No kernel privileges** - Just reads logs via K8s API
3. **AWS managed** - Network Policy Agent maintained by AWS
4. **Additional data** - Verdict (ACCEPT/DENY) not available in Falco
5. **Simplified stack** - No CNI chaining complexity

### Trade-offs

1. **Polling delay** - 5-10 second delay vs real-time (Hubble is real-time)
2. **Limited platforms** - Only standard EKS
3. **Log buffer risk** - High traffic may cause data loss
4. **Less metadata** - No pod names/labels in flows (backend decorates via IP)

---

## Appendices

### Appendix A: VPC CNI Log Format Reference

**Flow Log (ACCEPT):**
```json
{
  "level": "info",
  "ts": "2024-09-23T12:36:53.562Z",
  "logger": "ebpf-client",
  "caller": "events/events.go:193",
  "msg": "Flow Info: ",
  "Src IP": "10.0.141.167",
  "Src Port": 39197,
  "Dest IP": "172.20.0.10",
  "Dest Port": 53,
  "Proto": "TCP",
  "Verdict": "ACCEPT"
}
```

**Flow Log (DENY):**
```json
{
  "level": "info",
  "ts": "2024-09-23T12:36:53.604Z",
  "logger": "ebpf-client",
  "caller": "events/events.go:193",
  "msg": "Flow Info: ",
  "Src IP": "10.0.141.167",
  "Src Port": 43088,
  "Dest IP": "172.20.2.72",
  "Dest Port": 14220,
  "Proto": "TCP",
  "Verdict": "DENY"
}
```

**ICMP Flow:**
```json
{
  "level": "info",
  "ts": "2024-02-07T19:07:00.513Z",
  "logger": "ebpf-client",
  "msg": "Flow Info: ",
  "Src IP": "57.20.37.65",
  "Src Port": 0,
  "Dest IP": "100.64.44.16",
  "Dest Port": 0,
  "Proto": "ICMP",
  "Verdict": "DENY"
}
```

### Appendix B: Troubleshooting

#### No aws-eks-nodeagent container

```bash
# Verify VPC CNI version >= v1.14
aws eks describe-addon --cluster-name $CLUSTER --addon-name vpc-cni

# Enable NetworkPolicy
aws eks update-addon \
  --cluster-name $CLUSTER \
  --addon-name vpc-cni \
  --configuration-values '{"enableNetworkPolicy":"true"}' \
  --resolve-conflicts OVERWRITE
```

#### No flow logs appearing

```bash
# Enable debug logging for ACCEPT flows
kubectl set env daemonset aws-node -n kube-system \
  -c aws-eks-nodeagent ENABLE_POLICY_EVENT_LOGS=true

# Check nodeagent is running
kubectl get pods -n kube-system -l k8s-app=aws-node

# Check logs directly
kubectl logs -n kube-system <aws-node-pod> -c aws-eks-nodeagent --tail=100
```

#### Operator not detecting VPC CNI

```bash
# Check environment variable
kubectl get deployment cloud-operator -n illumio-cloud -o jsonpath='{.spec.template.spec.containers[0].env}'

# Should include ENABLE_VPC_CNI_FLOWS=true

# Check operator logs
kubectl logs -n illumio-cloud -l app=cloud-operator | grep -i "vpc\|flow collector"
```

### Appendix C: Glossary

| Term | Definition |
|------|------------|
| **VPC CNI** | AWS Virtual Private Cloud Container Network Interface plugin |
| **aws-node** | DaemonSet running VPC CNI on each EKS node |
| **aws-eks-nodeagent** | Container in aws-node that handles Network Policy and flow logging |
| **Falco** | Cloud-native runtime security tool using syscall tracing |
| **Cilium** | eBPF-based networking, observability, and security |
| **Hubble** | Network observability platform built on Cilium |
| **OVN-K** | Open Virtual Network for Kubernetes (OpenShift) |
| **5-tuple** | src IP, dst IP, src port, dst port, protocol |
| **eBPF** | Extended Berkeley Packet Filter |
| **DaemonSet** | Kubernetes workload ensuring pod runs on every node |
| **FlowCache** | Operator component that deduplicates and batches flows |

---

## References

1. [AWS VPC CNI Documentation](https://docs.aws.amazon.com/eks/latest/userguide/managing-vpc-cni.html)
2. [AWS Network Policy Agent GitHub](https://github.com/aws/aws-network-policy-agent)
3. [EKS Network Policies Troubleshooting](https://docs.aws.amazon.com/eks/latest/userguide/network-policies-troubleshooting.html)
4. [Illumio CloudSecure Documentation](https://docs.illumio.com/cloudsecure)
