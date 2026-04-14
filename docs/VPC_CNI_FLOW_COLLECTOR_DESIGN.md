# VPC CNI Flow Collector - Design Document

**Document Version:** 1.0  
**Date:** April 10, 2026  
**Status:** Approved  
**Jira Ticket:** CLOUD-16362  
**Author:** Cloud Containers Team

---

## Table of Contents

1. [Overview](#overview)
2. [What We Did Before](#what-we-did-before)
3. [What We Are Doing Now](#what-we-are-doing-now)
4. [How It Works](#how-it-works)
5. [Why This Approach](#why-this-approach)
6. [Implementation](#implementation)
7. [Configuration](#configuration)
8. [Migration Guide](#migration-guide)

---

## Overview

This document describes the design of the AWS VPC CNI flow collector for Illumio CloudSecure's cloud-operator. The new collector enables native flow collection on EKS clusters without requiring additional components like Falco or Cilium chaining.

---

## What We Did Before

### Legacy EKS Flow Collection

For EKS clusters, we had two options for flow collection:

#### Option 1: Cilium CNI Chaining

```
┌─────────────────────────────────────────────────────────────────┐
│  EKS Cluster with Cilium Chaining                               │
│                                                                  │
│  +------------------+     +------------------+                  │
│  |   AWS VPC CNI    |     |     Cilium       |                  │
│  |   (IPAM only)    | ──► |   (eBPF + Hubble)|                  │
│  +------------------+     +--------+---------+                  │
│                                    │                            │
│                                    │ gRPC                       │
│                                    ▼                            │
│                           +------------------+                  │
│                           |  Hubble Relay    |                  │
│                           +--------+---------+                  │
│                                    │                            │
│                                    │ gRPC stream                │
│                                    ▼                            │
│                           +------------------+                  │
│                           | cloud-operator   |                  │
│                           +------------------+                  │
│                                                                  │
│  Requires: Cilium DaemonSet deployment                         │
└─────────────────────────────────────────────────────────────────┘
```

**Pros:**
- Rich flow metadata (pod names, labels)
- Real-time flow streaming
- L7 network policy support
- Existing Hubble collector code works

**Cons:**
- Customer must deploy and manage Cilium
- Two CNI layers (complexity)
- Additional resource overhead

#### Option 2: Falco

```
┌─────────────────────────────────────────────────────────────────┐
│  EKS Cluster with Falco                                         │
│                                                                  │
│  +------------------+     +------------------+                  │
│  |   AWS VPC CNI    |     |  Falco DaemonSet |                  │
│  |   (networking)   |     |  (syscall trace) |                  │
│  +------------------+     +--------+---------+                  │
│                                    │                            │
│                                    │ HTTP POST                  │
│                                    ▼                            │
│                           +------------------+                  │
│                           | cloud-operator   |                  │
│                           | (port 5000)      |                  │
│                           +------------------+                  │
│                                                                  │
│  Requires: Falco DaemonSet deployment + kernel/eBPF privileges │
└─────────────────────────────────────────────────────────────────┘
```

**Pros:**
- Works on any cluster (not CNI specific)
- Push model (simple operator code)

**Cons:**
- Customer must deploy and manage Falco
- Requires kernel module or eBPF privileges
- No direction or verdict info
- Additional resource overhead

### The Problem

Both approaches required customers to deploy and manage additional components:

| Approach | Extra Deployment | Privileges Required |
|----------|-----------------|---------------------|
| Cilium Chaining | Cilium DaemonSet | eBPF |
| Falco | Falco DaemonSet | Kernel module or eBPF |

**Customer pain points:**
1. Additional deployment complexity
2. Security concerns with privileged containers
3. Maintenance burden
4. Resource overhead

---

## What We Are Doing Now

### Native VPC CNI Flow Collection

AWS VPC CNI v1.14+ includes native NetworkPolicy support and flow logging via the `aws-eks-nodeagent` container. We now collect flows directly from this native component.

```
┌─────────────────────────────────────────────────────────────────┐
│  EKS Cluster with VPC CNI Native (NEW)                          │
│                                                                  │
│  +----------------------------------------------------------+  │
│  |  aws-node DaemonSet (AWS managed)                         |  │
│  |  +-------------------+  +-------------------+             |  │
│  |  |    aws-node       |  | aws-eks-nodeagent |             |  │
│  |  |    (CNI plugin)   |  | (flow logs)       |             |  │
│  |  +-------------------+  +---------+---------+             |  │
│  +------------------------------------------|----------------+  │
│                                             │                   │
│                                             │ K8s Logs API      │
│                                             │ (polling)         │
│                                             ▼                   │
│                                  +------------------+           │
│                                  | cloud-operator   |           │
│                                  +------------------+           │
│                                                                  │
│  Requires: NOTHING EXTRA! VPC CNI is already there.            │
└─────────────────────────────────────────────────────────────────┘
```

### Key Change

| Before | Now |
|--------|-----|
| Deploy Cilium or Falco | Use existing aws-node DaemonSet |
| Push model (Falco HTTP) or Pull (Hubble gRPC) | Pull via K8s Logs API |
| Privileged containers | No privileges (just log reading) |

---

## How It Works

### Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        EKS Cluster                               │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Node 1                Node 2                Node 3              │
│  ┌──────────────┐     ┌──────────────┐     ┌──────────────┐    │
│  │ aws-node pod │     │ aws-node pod │     │ aws-node pod │    │
│  │ ┌──────────┐ │     │ ┌──────────┐ │     │ ┌──────────┐ │    │
│  │ │nodeagent │ │     │ │nodeagent │ │     │ │nodeagent │ │    │
│  │ │(logs)    │ │     │ │(logs)    │ │     │ │(logs)    │ │    │
│  │ └──────────┘ │     │ └──────────┘ │     │ └──────────┘ │    │
│  └──────────────┘     └──────────────┘     └──────────────┘    │
│         │                    │                    │             │
│         └────────────────────┼────────────────────┘             │
│                              │                                   │
│                    K8s Logs API (polling)                       │
│                              │                                   │
│                              ▼                                   │
│              ┌───────────────────────────────┐                  │
│              │       cloud-operator          │                  │
│              │  ┌─────────────────────────┐  │                  │
│              │  │  VPC CNI Collector      │  │                  │
│              │  │  (polls every 5s)       │  │                  │
│              │  └───────────┬─────────────┘  │                  │
│              │              │                │                  │
│              │              ▼                │                  │
│              │  ┌─────────────────────────┐  │                  │
│              │  │      FlowCache          │  │                  │
│              │  │  (dedup + batch)        │  │                  │
│              │  └───────────┬─────────────┘  │                  │
│              │              │                │                  │
│              └──────────────┼────────────────┘                  │
│                             │                                    │
└─────────────────────────────┼────────────────────────────────────┘
                              │ gRPC stream (single)
                              ▼
                    ┌───────────────────┐
                    │   CloudSecure     │
                    └───────────────────┘
```

### Polling Flow

```
┌─────────────────────────────────────────────────────────────────┐
│  VPC CNI Collector - Polling Loop                               │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │                                                           │  │
│  │  1. List aws-node pods                                   │  │
│  │     kubectl get pods -n kube-system -l k8s-app=aws-node  │  │
│  │                                                           │  │
│  │  2. For each pod:                                        │  │
│  │     - Get logs since last poll                           │  │
│  │       kubectl logs <pod> -c aws-eks-nodeagent            │  │
│  │              --since-time=<lastPollTime>                 │  │
│  │     - Parse JSON flow logs                               │  │
│  │     - Add flows to FlowCache                             │  │
│  │                                                           │  │
│  │  3. Update lastPollTime per pod                          │  │
│  │                                                           │  │
│  │  4. Cleanup stale pod entries (nodes removed)            │  │
│  │                                                           │  │
│  │  5. Sleep for poll interval (default 5s)                 │  │
│  │                                                           │  │
│  │  6. Repeat                                               │  │
│  │                                                           │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### Log Format

The aws-eks-nodeagent outputs flow logs in JSON format:

```json
{
  "level": "info",
  "ts": "2024-09-23T12:36:53.562Z",
  "logger": "ebpf-client",
  "msg": "Flow Info: ",
  "Src IP": "10.0.141.167",
  "Src Port": 39197,
  "Dest IP": "172.20.0.10",
  "Dest Port": 53,
  "Proto": "TCP",
  "Verdict": "ACCEPT"
}
```

### Data Flow

```
aws-eks-nodeagent     K8s API        cloud-operator      CloudSecure
       │                 │                  │                  │
       │ write log       │                  │                  │
       ├────────►        │                  │                  │
       │                 │   GetLogs()      │                  │
       │                 │◄─────────────────┤                  │
       │                 │                  │                  │
       │                 │   log lines      │                  │
       │                 ├─────────────────►│                  │
       │                 │                  │                  │
       │                 │                  │  parse JSON      │
       │                 │                  │  extract 5-tuple │
       │                 │                  │                  │
       │                 │                  │  FlowCache       │
       │                 │                  │  (dedup+batch)   │
       │                 │                  │                  │
       │                 │                  │  gRPC stream     │
       │                 │                  ├─────────────────►│
       │                 │                  │                  │
```

---

## Why This Approach

### Decision: Polling vs Streaming

We evaluated two approaches:

| Approach | Description | Complexity |
|----------|-------------|------------|
| **Streaming** | Keep N connections open (one per node) | High - manage connections, handle disconnects |
| **Polling** | Periodically fetch logs from all pods | Low - simple loop, no connection management |

**Decision: Polling** (discussed with Romain Lenglet)

**Rationale:**
1. **Simpler implementation** - No need to manage N persistent connections
2. **Easier node handling** - Nodes added/removed handled naturally in next poll
3. **Acceptable latency** - 5-10 second delay is fine for flow data
4. **Resource efficient** - No persistent connections consuming resources

### Decision: Pull vs Push

| Approach | Description | Requires |
|----------|-------------|----------|
| **Push** (like Falco) | Deploy forwarder DaemonSet that POSTs to operator | Extra DaemonSet |
| **Pull** (K8s API) | Operator fetches logs via K8s API | Nothing extra |

**Decision: Pull via K8s Logs API**

**Rationale:**
1. **No extra deployment** - The whole point is to avoid deploying additional components
2. **If we deploy a DaemonSet anyway, why not just use Falco?**
3. **K8s API is sufficient** - Log volume is manageable

### Why Not Continue Using Falco?

| Aspect | Falco | VPC CNI Native |
|--------|-------|----------------|
| Deployment | Customer deploys DaemonSet | Nothing (already there) |
| Privileges | Kernel module or eBPF | None (log reading only) |
| Maintenance | Customer responsibility | AWS managed |
| Verdict info | No | Yes (ACCEPT/DENY) |
| Direction info | No | Yes (internal) |

**Bottom line:** VPC CNI native is simpler for customers and provides more data.

### Why Not Always Use Cilium Chaining?

| Aspect | Cilium Chaining | VPC CNI Native |
|--------|-----------------|----------------|
| Flow metadata | Rich (labels, pod names) | Basic (IPs only) |
| Real-time | Yes | 5-10s delay |
| L7 policies | Yes | No |
| Deployment | Cilium DaemonSet | Nothing |
| Complexity | Two CNI layers | Single CNI |

**When to use Cilium chaining:**
- Need L7 network policies
- Need rich flow metadata
- Already using Cilium features

**When to use VPC CNI native:**
- Basic flow visibility is sufficient
- Want simplest possible setup
- Don't need Cilium features

---

## Implementation

### Files Structure

```
internal/controller/
├── collector/
│   ├── vpccni.go           # Log parser
│   └── vpccni_test.go      # Parser tests
└── stream/flows/
    └── vpccni/
        ├── detect.go       # Detection logic
        ├── detect_test.go  # Detection tests
        ├── factory.go      # Factory pattern
        └── client.go       # Polling client
```

### Flow Collector Selection

```go
func DetermineFlowCollector(ctx, config) (FlowCollector, Factory) {
    // Priority order:
    
    // 1. Cilium (if Hubble Relay available)
    if isCiliumAvailable(ctx, ...) {
        return CILIUM, &cilium.Factory{...}
    }
    
    // 2. OVN-K (if deployed)
    if isOVNKDeployed(ctx, ...) {
        return OVNK, &ovnk.Factory{...}
    }
    
    // 3. VPC CNI (if enabled and aws-eks-nodeagent detected)
    if config.EnableVPCCNI && vpccni.IsVPCCNIAvailable(ctx, ...) {
        return VPC_CNI, &vpccni.Factory{...}
    }
    
    // 4. Falco (fallback)
    return FALCO, &falco.Factory{...}
}
```

### Detection Logic

```go
func IsVPCCNIAvailable(ctx, logger, k8sClient) bool {
    // Check for aws-node pods with aws-eks-nodeagent container
    pods, _ := k8sClient.Pods("kube-system").List(ctx,
        LabelSelector: "k8s-app=aws-node")
    
    if len(pods.Items) == 0 {
        return false
    }
    
    for _, container := range pods.Items[0].Spec.Containers {
        if container.Name == "aws-eks-nodeagent" {
            return true
        }
    }
    return false
}
```

---

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ENABLE_VPC_CNI_FLOWS` | `false` | Enable VPC CNI flow collector |
| `VPC_CNI_POLL_INTERVAL` | `5s` | Polling interval |

### Enabling VPC CNI Flow Collection

```bash
# Update operator deployment
kubectl set env deployment/cloud-operator -n illumio-cloud \
  ENABLE_VPC_CNI_FLOWS=true \
  VPC_CNI_POLL_INTERVAL=5s
```

### Prerequisites on EKS

```bash
# 1. Ensure VPC CNI >= v1.14
aws eks describe-addon --cluster-name <cluster> --addon-name vpc-cni

# 2. Enable NetworkPolicy (deploys aws-eks-nodeagent)
aws eks update-addon \
  --cluster-name <cluster> \
  --addon-name vpc-cni \
  --configuration-values '{"enableNetworkPolicy":"true"}'

# 3. Enable flow logs for ACCEPT (optional, for non-DENY flows)
kubectl set env daemonset aws-node -n kube-system \
  -c aws-eks-nodeagent ENABLE_POLICY_EVENT_LOGS=true
```

---

## Migration Guide

### From Falco to VPC CNI

1. **Enable VPC CNI flow logging on EKS:**
   ```bash
   aws eks update-addon --cluster-name <cluster> --addon-name vpc-cni \
     --configuration-values '{"enableNetworkPolicy":"true"}'
   ```

2. **Enable operator VPC CNI collector:**
   ```bash
   kubectl set env deployment/cloud-operator -n illumio-cloud \
     ENABLE_VPC_CNI_FLOWS=true
   ```

3. **Verify flows are being collected:**
   ```bash
   kubectl logs -n illumio-cloud -l app=cloud-operator | grep "VPC CNI"
   ```

4. **Remove Falco (optional):**
   ```bash
   kubectl delete daemonset falco -n falco
   ```

### From Cilium Chaining to VPC CNI

**Note:** Only migrate if you don't need Cilium's advanced features.

1. **Remove Cilium:**
   ```bash
   helm uninstall cilium -n kube-system
   ```

2. **Enable VPC CNI NetworkPolicy:**
   ```bash
   aws eks update-addon --cluster-name <cluster> --addon-name vpc-cni \
     --configuration-values '{"enableNetworkPolicy":"true"}'
   ```

3. **Enable operator VPC CNI collector:**
   ```bash
   kubectl set env deployment/cloud-operator -n illumio-cloud \
     ENABLE_VPC_CNI_FLOWS=true
   ```

---

## Platform Limitations

### EKS Auto Mode - NOT SUPPORTED

**Critical:** VPC CNI flow collector does NOT work on EKS Auto Mode clusters.

**Why:** EKS Auto Mode manages nodes differently - networking is built into the infrastructure rather than running as DaemonSet pods. The `aws-node` DaemonSet has an explicit anti-affinity rule that excludes Auto Mode nodes:

```yaml
# aws-node DaemonSet affinity (from live cluster inspection)
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
      - matchExpressions:
        - key: kubernetes.io/os
          operator: In
          values:
          - linux
        - key: kubernetes.io/arch
          operator: In
          values:
          - amd64
          - arm64
        - key: eks.amazonaws.com/compute-type
          operator: NotIn
          values:
          - fargate
          - hybrid
          - auto    # <-- Auto Mode nodes explicitly excluded
```

**How to identify EKS Auto Mode clusters:**

| Check | Command | Auto Mode Result |
|-------|---------|------------------|
| Node label | `kubectl get nodes --show-labels \| grep compute-type` | `eks.amazonaws.com/compute-type=auto` |
| Node groups | `aws eks list-nodegroups --cluster-name <cluster>` | `{"nodegroups": []}` (empty) |
| Karpenter | `kubectl get nodes -o jsonpath='{.items[0].metadata.labels}' \| grep karpenter` | `karpenter.sh/*` labels present |
| Node names | `kubectl get nodes` | Instance IDs like `i-0c87194e2b64f9ca9` |
| DaemonSet | `kubectl get daemonset aws-node -n kube-system` | `DESIRED: 0` |

**Impact:**
- `aws-node` DaemonSet shows `DESIRED: 0` (no pods scheduled)
- No `aws-eks-nodeagent` containers exist to collect logs from
- VPC CNI flow collector detection returns `false`

**Alternative:** Use Falco as the flow collector (default fallback)

### Supported vs Unsupported Configurations

| Platform | Support | Reason |
|----------|---------|--------|
| EKS Standard (managed node groups) | **Supported** | Full access to aws-node DaemonSet |
| EKS Standard (self-managed nodes) | **Supported** | Full access to aws-node DaemonSet |
| EKS with Karpenter (non-Auto) | **Supported** | Karpenter nodes still run aws-node |
| EKS Auto Mode | **Not Supported** | aws-node excluded via anti-affinity |
| EKS Fargate | **Not Supported** | No DaemonSets on Fargate |
| Non-EKS (GKE, AKS, etc.) | **Not Applicable** | VPC CNI is EKS-specific |

---

## Summary

| Aspect | Before | Now |
|--------|--------|-----|
| **EKS flow collection** | Cilium chaining or Falco | VPC CNI native |
| **Extra deployment** | Yes (Cilium or Falco DaemonSet) | No |
| **Privileges** | eBPF or kernel module | None (log reading) |
| **Collection method** | gRPC stream (Hubble) or HTTP push (Falco) | K8s Logs API polling |
| **Flow metadata** | Rich (Cilium) or basic (Falco) | Basic + verdict |
| **Maintenance** | Customer managed | AWS managed |

**Key benefit:** Customers no longer need to deploy additional components for flow visibility on standard EKS clusters.
