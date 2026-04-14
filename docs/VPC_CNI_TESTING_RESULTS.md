# VPC CNI Flow Collector - Testing Results & Reproduction Steps

**Date:** April 13, 2026  
**Tested By:** Pavan Innamuri  
**Cluster:** vpc-cni-test (us-east-2)  
**VPC CNI Version:** v1.20.4-eksbuild.2  
**Network Policy Agent Version:** v1.2.7-eksbuild.1

---

## Executive Summary

VPC CNI flow logging was successfully validated on a standard EKS cluster. Flow logs contain all required five-tuple data plus verdict and direction information.

**Key Findings:**
1. VPC CNI flow logging works on standard EKS clusters
2. Does NOT work on EKS Auto Mode (aws-node DaemonSet excluded)
3. Log format changed in v1.2.2+ (embedded in msg string vs separate JSON fields)
4. Parser code needs update to handle new format

---

## Test Environment Setup

### Prerequisites Verified

```bash
# AWS CLI
aws --version
# aws-cli/2.27.11

# eksctl
eksctl version
# 0.225.0

# kubectl configured for cluster
kubectl version --client
```

### EKS Auto Mode Limitation Discovery

**Initial Attempt:** Tried using existing cluster `pavanekstestcluster`

**Issue Found:** EKS Auto Mode clusters do not run aws-node DaemonSet pods.

```bash
# Check for Auto Mode
kubectl get nodes --show-labels | grep compute-type
# eks.amazonaws.com/compute-type=auto

# DaemonSet shows DESIRED: 0
kubectl get daemonset aws-node -n kube-system
# NAME       DESIRED   CURRENT   READY
# aws-node   0         0         0

# Root cause: Anti-affinity rule in aws-node DaemonSet
kubectl get daemonset aws-node -n kube-system -o yaml | grep -A20 "affinity:"
# - key: eks.amazonaws.com/compute-type
#   operator: NotIn
#   values:
#   - fargate
#   - hybrid
#   - auto    # <-- Excludes Auto Mode nodes
```

**Conclusion:** VPC CNI flow collector cannot work on EKS Auto Mode.

---

## Standard EKS Cluster Creation

### Step 1: Check Resource Limits

```bash
# Check existing VPCs (limit is 5 per region)
aws ec2 describe-vpcs --region us-east-2 --query 'Vpcs[*].[VpcId,Tags[?Key==`Name`].Value|[0]]' --output table

# Check Elastic IPs
aws ec2 describe-addresses --region us-east-2 --query 'Addresses[*].[PublicIp,AllocationId,AssociationId]' --output table

# Release unused EIPs if needed
aws ec2 release-address --allocation-id eipalloc-xxx --region us-east-2
```

### Step 2: Create Cluster with Existing VPC

Due to VPC limits, we used an existing VPC with subnets:

```bash
# List subnets in existing VPC
aws ec2 describe-subnets --region us-east-2 \
  --filters "Name=vpc-id,Values=vpc-0b0b98ae1a695265e" \
  --query 'Subnets[*].[SubnetId,AvailabilityZone,CidrBlock,Tags[?Key==`Name`].Value|[0]]' \
  --output table

# Create cluster using existing subnets
eksctl create cluster --name vpc-cni-test --region us-east-2 \
  --vpc-public-subnets=subnet-09604f561b9a61269,subnet-056c6ea54a0e1d979 \
  --nodegroup-name ng-1 --node-type t3.medium --nodes 2 --managed
```

**Cluster creation time:** ~15-20 minutes

### Step 3: Verify Cluster

```bash
# Update kubeconfig
aws eks update-kubeconfig --name vpc-cni-test --region us-east-2

# Check cluster status
aws eks describe-cluster --name vpc-cni-test --region us-east-2 --query 'cluster.status'
# "ACTIVE"

# Check nodes (should have standard node names, not instance IDs)
kubectl get nodes
# NAME                                       STATUS   ROLES    AGE   VERSION
# ip-10-0-1-242.us-east-2.compute.internal   Ready    <none>   14m   v1.34.4-eks-f69f56f
# ip-10-0-2-6.us-east-2.compute.internal     Ready    <none>   14m   v1.34.4-eks-f69f56f

# Verify aws-node pods exist with 2/2 containers
kubectl get pods -n kube-system -l k8s-app=aws-node
# NAME             READY   STATUS    RESTARTS   AGE
# aws-node-5qqtj   2/2     Running   0          14m
# aws-node-xgg2s   2/2     Running   0          14m
```

---

## Enable Network Policy & Flow Logging

### Step 1: Enable Network Policy

```bash
aws eks update-addon --cluster-name vpc-cni-test --addon-name vpc-cni \
  --configuration-values '{"enableNetworkPolicy":"true"}' \
  --resolve-conflicts OVERWRITE --region us-east-2
```

### Step 2: Enable Policy Event Logs (ACCEPT flows)

**Important:** By default, only DENY flows are logged. To see ACCEPT flows, enable policy event logs:

```bash
aws eks update-addon --cluster-name vpc-cni-test --addon-name vpc-cni \
  --configuration-values '{"enableNetworkPolicy":"true","nodeAgent":{"enablePolicyEventLogs":"true"}}' \
  --resolve-conflicts OVERWRITE --region us-east-2
```

### Step 3: Restart aws-node Pods

```bash
kubectl rollout restart daemonset/aws-node -n kube-system
kubectl rollout status daemonset/aws-node -n kube-system
```

### Step 4: Verify Configuration

```bash
# Check addon configuration
aws eks describe-addon --cluster-name vpc-cni-test --addon-name vpc-cni --region us-east-2

# Verify containers
kubectl get pods -n kube-system -l k8s-app=aws-node -o jsonpath='{range .items[*]}{.metadata.name}{": "}{range .spec.containers[*]}{.name}{" "}{end}{"\n"}{end}'
# aws-node-xxx: aws-node aws-eks-nodeagent

# Verify args include enable-policy-event-logs=true
kubectl describe pod -n kube-system -l k8s-app=aws-node | grep "enable-policy-event-logs"
# --enable-policy-event-logs=true
```

---

## Create Test Workloads & Network Policy

### Step 1: Create Test Namespace and Pods

```bash
kubectl create namespace test-flows

# Create server with app=server label
kubectl run server -n test-flows --image=nginx --port=80 --labels="app=server"

# Create client with run=client label
kubectl run client -n test-flows --image=busybox --restart=Never -- sleep 3600

# Wait for pods
kubectl wait --for=condition=Ready pod/server pod/client -n test-flows --timeout=60s
```

### Step 2: Create Network Policy

**Important:** NetworkPolicy MUST be applied for flow logs to be generated.

```bash
kubectl apply -f - <<< '{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"allow-client","namespace":"test-flows"},"spec":{"podSelector":{"matchLabels":{"app":"server"}},"ingress":[{"from":[{"podSelector":{"matchLabels":{"run":"client"}}}],"ports":[{"port":80}]}]}}'
```

### Step 3: Verify Network Policy is Targeting Pods

```bash
kubectl get networkpolicy -n test-flows
# NAME           POD-SELECTOR   AGE
# allow-client   app=server     21m

kubectl get pods -n test-flows --show-labels
# NAME     READY   STATUS    RESTARTS   AGE   LABELS
# client   1/1     Running   0          33m   run=client
# server   1/1     Running   0          33m   app=server
```

---

## Generate Traffic & Verify Flow Logs

### Step 1: Get Server Pod IP

```bash
kubectl get pod -n test-flows server -o jsonpath='{.status.podIP}'
# 10.0.1.132
```

### Step 2: Generate Traffic

```bash
kubectl exec -n test-flows client -- wget -qO- --timeout=5 10.0.1.132
# Returns nginx HTML - traffic successful
```

### Step 3: Check Flow Logs

**Important:** Logs are written to a file, not stdout. Use debug pod to read:

```bash
# Get node where pods are running
kubectl get pods -n test-flows -o wide
# Both on ip-10-0-1-242.us-east-2.compute.internal

# Read logs from correct node
kubectl debug node/ip-10-0-1-242.us-east-2.compute.internal -it --image=busybox \
  -- tail -100 /host/var/log/aws-routed-eni/network-policy-agent.log | grep "Flow Info"
```

### Step 4: Flow Log Output

```json
{"level":"debug","ts":"2026-04-13T21:18:46.888Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction egress"}
{"level":"debug","ts":"2026-04-13T21:18:46.888Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction ingress"}
```

---

## Flow Log Format Analysis

### Expected Format (from AWS docs, v1.0.x - v1.2.1)

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

### Actual Format (v1.2.2+, current)

```json
{
  "level": "debug",
  "ts": "2026-04-13T21:18:46.888Z",
  "caller": "runtime/asm_amd64.s:1700",
  "msg": "Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction egress"
}
```

### Key Differences

| Aspect | Old Format (v1.0.x-v1.2.1) | New Format (v1.2.2+) |
|--------|---------------------------|---------------------|
| Field structure | Separate JSON fields | Embedded in `msg` string |
| Logger field | `"logger": "ebpf-client"` | `"caller": "runtime/..."` |
| Direction | Not included | Included (egress/ingress) |
| Log level for ACCEPT | info | debug |

### Five Tuple Verification

| Field | Present | Example Value |
|-------|---------|---------------|
| Source IP | ✅ | 10.0.1.28 |
| Source Port | ✅ | 55484 |
| Destination IP | ✅ | 10.0.1.132 |
| Destination Port | ✅ | 80 |
| Protocol | ✅ | TCP |
| **Bonus: Verdict** | ✅ | ACCEPT |
| **Bonus: Direction** | ✅ | egress |

---

## Troubleshooting Guide

### Issue: No aws-node pods

**Cause:** EKS Auto Mode or Fargate

**Check:**
```bash
kubectl get nodes --show-labels | grep "eks.amazonaws.com/compute-type"
```

**Solution:** Use standard EKS cluster with managed node groups

### Issue: aws-node pods show 1/1 instead of 2/2

**Cause:** Network Policy not enabled

**Solution:**
```bash
aws eks update-addon --cluster-name CLUSTER --addon-name vpc-cni \
  --configuration-values '{"enableNetworkPolicy":"true"}' \
  --resolve-conflicts OVERWRITE
```

### Issue: No flow logs appearing

**Cause 1:** Policy event logs not enabled (only DENY flows logged by default)

**Solution:**
```bash
aws eks update-addon --cluster-name CLUSTER --addon-name vpc-cni \
  --configuration-values '{"enableNetworkPolicy":"true","nodeAgent":{"enablePolicyEventLogs":"true"}}' \
  --resolve-conflicts OVERWRITE
```

**Cause 2:** No NetworkPolicy applied

**Solution:** Create a NetworkPolicy targeting your pods

**Cause 3:** NetworkPolicy not matching pods (check labels)

**Solution:**
```bash
kubectl get pods -n NAMESPACE --show-labels
kubectl get networkpolicy -n NAMESPACE -o yaml
```

### Issue: Logs not in stdout, only in file

**Cause:** aws-eks-nodeagent writes to file by default

**Solution:** Read from log file using debug pod:
```bash
kubectl debug node/NODE_NAME -it --image=busybox \
  -- cat /host/var/log/aws-routed-eni/network-policy-agent.log
```

---

## Cleanup

```bash
# Delete test resources
kubectl delete namespace test-flows

# Delete cluster (if created for testing)
eksctl delete cluster --name vpc-cni-test --region us-east-2
```

---

## Code Changes Required

### Parser Update Needed

The current parser in `collector/vpccni.go` expects the old JSON format with separate fields. It needs to be updated to parse the new embedded msg format.

**Current struct (won't work with new format):**
```go
type VPCCNIFlowLog struct {
    SrcIP    string `json:"Src IP"`  // Empty in new format
    DestIP   string `json:"Dest IP"` // Empty in new format
    ...
}
```

**Required:** Parse the `msg` field string to extract flow data:
```
"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction egress"
```

---

## References

- [AWS Network Policy Agent GitHub](https://github.com/aws/aws-network-policy-agent)
- [EKS VPC CNI Documentation](https://docs.aws.amazon.com/eks/latest/userguide/managing-vpc-cni.html)
- [VPC CNI Flow Collector Design Doc](VPC_CNI_FLOW_COLLECTOR_DESIGN.md)
- [AWS VPC CNI Evaluation Doc](AWS_VPC_CNI_FLOW_LOGGING_EVALUATION.md)
