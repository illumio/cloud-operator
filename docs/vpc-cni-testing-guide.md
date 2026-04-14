# AWS VPC CNI Flow Collector Testing Guide

## Overview

This guide covers testing the VPC CNI flow collector feature on AWS EKS clusters. The feature collects network flow logs from the AWS VPC CNI Network Policy Agent (`aws-eks-nodeagent`).

## Prerequisites

- AWS CLI configured with appropriate credentials
- `kubectl` installed
- `eksctl` installed (recommended for cluster creation)
- Docker (for building operator image)
- Access to a container registry (ECR, Docker Hub, etc.)

---

## Step 1: Create EKS Cluster with Network Policy Support

### Option A: Using eksctl (Recommended)

```bash
# Create cluster config file
cat > eks-vpc-cni-cluster.yaml << 'EOF'
apiVersion: eksctl.io/v1alpha5
kind: ClusterConfig

metadata:
  name: vpc-cni-test-cluster
  region: us-west-2
  version: "1.29"

managedNodeGroups:
  - name: ng-1
    instanceType: t3.medium
    desiredCapacity: 2
    minSize: 1
    maxSize: 3

addons:
  - name: vpc-cni
    version: latest
    configurationValues: |
      enableNetworkPolicy: "true"
  - name: coredns
    version: latest
  - name: kube-proxy
    version: latest
EOF

# Create the cluster
eksctl create cluster -f eks-vpc-cni-cluster.yaml
```

### Option B: Enable Network Policy on Existing Cluster

```bash
# Update VPC CNI addon to enable network policy
aws eks update-addon \
  --cluster-name YOUR_CLUSTER_NAME \
  --addon-name vpc-cni \
  --configuration-values '{"enableNetworkPolicy": "true"}' \
  --resolve-conflicts OVERWRITE

# Or using eksctl
eksctl update addon \
  --name vpc-cni \
  --cluster YOUR_CLUSTER_NAME \
  --configuration-values '{"enableNetworkPolicy": "true"}' \
  --force
```

---

## Step 2: Verify Network Policy Agent is Running

```bash
# Check aws-node pods have the aws-eks-nodeagent container
kubectl get pods -n kube-system -l k8s-app=aws-node -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{range .spec.containers[*]}{.name}{" "}{end}{"\n"}{end}'

# Expected output should include "aws-eks-nodeagent" container:
# aws-node-xxxxx    aws-node aws-eks-nodeagent

# Verify container is running
kubectl get pods -n kube-system -l k8s-app=aws-node

# Check logs from nodeagent (should see flow logs when traffic occurs)
kubectl logs -n kube-system -l k8s-app=aws-node -c aws-eks-nodeagent --tail=20
```

---

## Step 3: Build and Push Operator Image

```bash
# Set your registry
export REGISTRY=YOUR_ECR_REGISTRY  # e.g., 123456789.dkr.ecr.us-west-2.amazonaws.com

# Login to ECR
aws ecr get-login-password --region us-west-2 | docker login --username AWS --password-stdin $REGISTRY

# Build the operator image
cd /path/to/cloud-operator
make docker-build IMG=$REGISTRY/cloud-operator:vpc-cni-test

# Push to registry
make docker-push IMG=$REGISTRY/cloud-operator:vpc-cni-test
```

---

## Step 4: Deploy Cloud Operator

### Create Namespace and Secrets

```bash
# Create namespace
kubectl create namespace illumio-cloud

# Create cluster credentials secret (get from CloudSecure)
kubectl create secret generic clustercreds \
  --namespace illumio-cloud \
  --from-literal=cluster_id=YOUR_CLUSTER_ID \
  --from-literal=cluster_token=YOUR_CLUSTER_TOKEN
```

### Deploy Operator with VPC CNI Enabled

```yaml
# Save as cloud-operator-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cloud-operator
  namespace: illumio-cloud
spec:
  replicas: 1
  selector:
    matchLabels:
      app: cloud-operator
  template:
    metadata:
      labels:
        app: cloud-operator
    spec:
      serviceAccountName: cloud-operator
      containers:
      - name: operator
        image: YOUR_REGISTRY/cloud-operator:vpc-cni-test
        env:
        # Enable VPC CNI flow collection
        - name: ENABLE_VPC_CNI_FLOWS
          value: "true"
        # Poll interval (optional, default 5s)
        - name: VPC_CNI_POLL_INTERVAL
          value: "5s"
        # Standard config
        - name: POD_NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
        - name: CLUSTER_CREDS_SECRET
          value: "clustercreds"
        - name: ONBOARDING_ENDPOINT
          value: "https://cloud.illumio.com/api/v1/k8s_cluster/onboard"
        - name: TOKEN_ENDPOINT
          value: "https://cloud.illumio.com/api/v1/k8s_cluster/authenticate"
        # Debug logging (optional)
        - name: VERBOSE_DEBUGGING
          value: "true"
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
        ports:
        - containerPort: 8080
          name: health
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 30
        readinessProbe:
          httpGet:
            path: /healthz
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: cloud-operator
  namespace: illumio-cloud
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: cloud-operator
rules:
- apiGroups: [""]
  resources: ["pods", "pods/log", "services", "namespaces", "nodes"]
  verbs: ["get", "list", "watch"]
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch"]
  resourceNames: ["clustercreds"]
- apiGroups: ["apps"]
  resources: ["deployments", "daemonsets", "replicasets", "statefulsets"]
  verbs: ["get", "list", "watch"]
- apiGroups: ["networking.k8s.io"]
  resources: ["networkpolicies"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: cloud-operator
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cloud-operator
subjects:
- kind: ServiceAccount
  name: cloud-operator
  namespace: illumio-cloud
```

```bash
# Deploy
kubectl apply -f cloud-operator-deployment.yaml

# Check operator logs
kubectl logs -n illumio-cloud -l app=cloud-operator -f
```

---

## Step 5: Create Test Workloads and Network Policies

### Deploy Test Applications

```yaml
# Save as test-workloads.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: test-flows
---
# Client pod
apiVersion: v1
kind: Pod
metadata:
  name: client
  namespace: test-flows
  labels:
    app: client
spec:
  containers:
  - name: client
    image: busybox
    command: ["sleep", "3600"]
---
# Server pod
apiVersion: v1
kind: Pod
metadata:
  name: server
  namespace: test-flows
  labels:
    app: server
spec:
  containers:
  - name: server
    image: nginx:alpine
    ports:
    - containerPort: 80
---
apiVersion: v1
kind: Service
metadata:
  name: server
  namespace: test-flows
spec:
  selector:
    app: server
  ports:
  - port: 80
    targetPort: 80
```

### Create Network Policy (Required for Flow Logs)

```yaml
# Save as test-network-policy.yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-client-to-server
  namespace: test-flows
spec:
  podSelector:
    matchLabels:
      app: server
  policyTypes:
  - Ingress
  ingress:
  - from:
    - podSelector:
        matchLabels:
          app: client
    ports:
    - protocol: TCP
      port: 80
---
# Deny all other ingress to server
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: deny-other-ingress
  namespace: test-flows
spec:
  podSelector:
    matchLabels:
      app: server
  policyTypes:
  - Ingress
```

```bash
# Apply workloads and policies
kubectl apply -f test-workloads.yaml
kubectl apply -f test-network-policy.yaml
```

---

## Step 6: Generate Traffic and Verify Flow Collection

### Generate Allowed Traffic

```bash
# From client to server (should be ACCEPT)
kubectl exec -n test-flows client -- wget -q -O- http://server
```

### Generate Denied Traffic

```bash
# Create another pod without the client label
kubectl run unauthorized -n test-flows --image=busybox --restart=Never -- sleep 3600

# Try to access server (should be DENY)
kubectl exec -n test-flows unauthorized -- wget -q -O- --timeout=2 http://server || echo "Connection denied as expected"
```

### Check Flow Logs in aws-eks-nodeagent

```bash
# View raw flow logs from nodeagent
kubectl logs -n kube-system -l k8s-app=aws-node -c aws-eks-nodeagent --tail=50 | grep "Flow Info"

# Expected output format:
# {"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"10.0.141.167","Src Port":39197,"Dest IP":"172.20.0.10","Dest Port":80,"Proto":"TCP","Verdict":"ACCEPT"}
```

### Check Operator Logs

```bash
# Check operator is collecting flows
kubectl logs -n illumio-cloud -l app=cloud-operator -f | grep -i "vpc\|flow"

# Look for:
# - "Using AWS VPC CNI flow collector"
# - "Starting VPC CNI flow collector"
# - "Parsed flows from pod"
```

---

## Step 7: Verify in CloudSecure

1. Log into CloudSecure dashboard
2. Navigate to your cluster
3. Check the Traffic tab for collected flows
4. Verify flows show correct source/destination IPs and verdicts

---

## Troubleshooting

### No aws-eks-nodeagent container

```bash
# Check if Network Policy is enabled
aws eks describe-addon --cluster-name YOUR_CLUSTER --addon-name vpc-cni \
  --query 'addon.configurationValues' --output text

# Should show: {"enableNetworkPolicy":"true"}
```

### No flow logs appearing

1. **Network policies required**: VPC CNI only logs flows when network policies are applied
2. **Check nodeagent logs**: `kubectl logs -n kube-system -l k8s-app=aws-node -c aws-eks-nodeagent`
3. **Check operator detection**: Look for "Using AWS VPC CNI flow collector" in operator logs

### Operator not detecting VPC CNI

```bash
# Verify ENABLE_VPC_CNI_FLOWS is set
kubectl get deployment -n illumio-cloud cloud-operator -o jsonpath='{.spec.template.spec.containers[0].env}' | jq

# Check operator has permission to read pod logs
kubectl auth can-i get pods/log -n kube-system --as=system:serviceaccount:illumio-cloud:cloud-operator
```

### Permission errors reading logs

Ensure the ClusterRole includes:
```yaml
- apiGroups: [""]
  resources: ["pods/log"]
  verbs: ["get"]
```

---

## Cleanup

```bash
# Delete test resources
kubectl delete namespace test-flows
kubectl delete pod unauthorized -n test-flows --ignore-not-found

# Delete operator
kubectl delete -f cloud-operator-deployment.yaml

# Delete cluster (if created for testing)
eksctl delete cluster -f eks-vpc-cni-cluster.yaml
```

---

## Configuration Reference

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `ENABLE_VPC_CNI_FLOWS` | `false` | Enable VPC CNI flow collection |
| `VPC_CNI_POLL_INTERVAL` | `5s` | Interval to poll aws-node pod logs |

---

## Flow Log Format

The VPC CNI Network Policy Agent produces flow logs in JSON format:

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

**Verdict values:**
- `ACCEPT` - Traffic allowed by network policy
- `DENY` - Traffic blocked by network policy
