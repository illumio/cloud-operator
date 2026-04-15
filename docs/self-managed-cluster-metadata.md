# Self-Managed Cluster Name and Region Support

## Overview

Currently, cluster name and region are auto-detected only for managed Kubernetes clusters (EKS, AKS, GKE, OCI) by reading cloud-provider-specific node labels. Self-managed clusters (on-premises, bare-metal, or unrecognized providers) do not have these labels, resulting in empty name/region fields in CloudSecure.

This document proposes adding optional Helm configuration for customers to specify cluster name and region during operator installation.

## Current Behavior

### Managed Clusters (Auto-detected)

The `k8sparser` package extracts cluster metadata from node labels:

| Provider | Cluster Name Label | Region Label |
|----------|-------------------|--------------|
| EKS | `alpha.eksctl.io/cluster-name` | `topology.kubernetes.io/region` |
| AKS | `kubernetes.azure.com/cluster` | `topology.kubernetes.io/region` |
| GKE | `goog-k8s-cluster-name` or parsed from node name | `topology.kubernetes.io/region` |
| OCI | `displayName` | `topology.kubernetes.io/region` (converted from short code) |

### Self-Managed Clusters (Current Gap)

- **Cluster Name**: Empty
- **Region**: Empty (unless `topology.kubernetes.io/region` label exists)

## Proposed Solution

### Helm Configuration

Add optional cluster metadata configuration in `values.yaml`:

```yaml
cluster:
  # Optional: Cluster name for self-managed clusters
  # If not set, auto-detection is attempted from node labels
  name: ""
  
  # Optional: Cluster region for self-managed clusters  
  # If not set, auto-detection is attempted from node labels
  region: ""
```

### Environment Variables

The Helm chart passes these as environment variables to the operator:

| Helm Value | Environment Variable |
|------------|---------------------|
| `cluster.name` | `CLUSTER_NAME` |
| `cluster.region` | `CLUSTER_REGION` |

### Operator Changes

1. Read `CLUSTER_NAME` and `CLUSTER_REGION` environment variables
2. Include in `ClusterMetadata` sent to the server
3. These values are used as fallbacks when cloud-provider labels are not detected

### Server Changes (k8sclustersync)

In `k8sparser/cluster_metadata.go`, add fallback logic:

```go
func (clusterDetails *ClusterDetails) extractNodeLabels(metadata *op.KubernetesObjectData) {
    labels := metadata.Labels

    // Existing cloud provider detection...
    
    // Fallback: Use operator-provided values for self-managed clusters
    if clusterDetails.ClusterName == "" && clusterDetails.Metadata.ClusterName != "" {
        clusterDetails.ClusterName = clusterDetails.Metadata.ClusterName
    }
    if clusterDetails.CloudRegion == "" && clusterDetails.Metadata.ClusterRegion != "" {
        clusterDetails.CloudRegion = clusterDetails.Metadata.ClusterRegion
    }
}
```

## Usage

### Installation with Cluster Metadata

```bash
helm install illumio-cloud-operator ./cloud-operator \
  --namespace illumio-cloud \
  --set onboardingSecret.clientId="<client-id>" \
  --set onboardingSecret.clientSecret="<client-secret>" \
  --set cluster.name="production-k8s" \
  --set cluster.region="us-east-datacenter"
```

### Upgrade Existing Installation

```bash
helm upgrade illumio-cloud-operator ./cloud-operator \
  --namespace illumio-cloud \
  --set cluster.name="production-k8s" \
  --set cluster.region="us-east-datacenter"
```

## Implementation Checklist

### Cloud Operator (this repo)

- [ ] Add `cluster.name` and `cluster.region` to `values.yaml`
- [ ] Add `CLUSTER_NAME` and `CLUSTER_REGION` env vars to deployment template
- [ ] Read env vars in `cmd/main.go`
- [ ] Include in stream metadata sent to server

### k8sclustersync (storm repo)

- [ ] Update proto to include cluster name/region in metadata (if not already present)
- [ ] Add fallback logic in `k8sparser/cluster_metadata.go`
- [ ] Add unit tests for fallback behavior

## Backward Compatibility

- Fully backward compatible
- Existing installations continue to work (empty values = current behavior)
- No breaking changes to Helm chart or operator

## Alternatives Considered

### Option: Custom Node Labels

Require customers to add labels like `illumio.com/cluster-name` to all nodes.

**Rejected because:**
- Requires modifying customer infrastructure
- Must label all nodes
- More operational burden for customers

### Option: Onboarding API

Pass name/region during the onboarding HTTP call.

**Rejected because:**
- Onboarding is one-time; can't update later
- Would require re-onboarding to change values
