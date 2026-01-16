#!/bin/bash
# Setup Minikube with Calico v3.30.0 (Goldmane + Whisker enabled)
#
# This script sets up a local Kubernetes cluster with:
# - Minikube
# - Calico CNI v3.30.0 (via Tigera operator)
# - Goldmane (flow logs gRPC API on port 7443)
# - Whisker (web UI on port 8081)

set -e

CALICO_VERSION="${CALICO_VERSION:-v3.30.0}"
MINIKUBE_MEMORY="${MINIKUBE_MEMORY:-4096}"
MINIKUBE_CPUS="${MINIKUBE_CPUS:-2}"
POD_CIDR="${POD_CIDR:-192.168.0.0/16}"

echo "=== Setting up Minikube with Calico ${CALICO_VERSION} ==="

# Step 1: Delete existing minikube cluster (if any)
echo "[1/5] Cleaning up existing minikube cluster..."
minikube delete 2>/dev/null || true

# Step 2: Start minikube without default CNI
echo "[2/5] Starting minikube (memory=${MINIKUBE_MEMORY}MB, cpus=${MINIKUBE_CPUS})..."
minikube start \
  --cni=false \
  --memory=${MINIKUBE_MEMORY} \
  --cpus=${MINIKUBE_CPUS} \
  --driver=docker \
  --extra-config=kubeadm.pod-network-cidr=${POD_CIDR}

# Step 3: Install Tigera operator
echo "[3/5] Installing Tigera operator..."
kubectl create -f https://raw.githubusercontent.com/projectcalico/calico/${CALICO_VERSION}/manifests/tigera-operator.yaml

# Wait for operator to be ready
echo "Waiting for Tigera operator to be ready..."
kubectl wait --for=condition=available deployment/tigera-operator -n tigera-operator --timeout=120s

# Step 4: Install Calico with Goldmane and Whisker
echo "[4/5] Installing Calico custom resources (includes Goldmane + Whisker)..."
kubectl create -f https://raw.githubusercontent.com/projectcalico/calico/${CALICO_VERSION}/manifests/custom-resources.yaml

# Step 5: Wait for all Calico components to be ready
echo "[5/5] Waiting for Calico components to be ready..."
echo "This may take a few minutes..."

# Wait for calico-system namespace to exist
while ! kubectl get namespace calico-system &>/dev/null; do
  echo "Waiting for calico-system namespace..."
  sleep 5
done

# Wait for pods to be created
sleep 30

# Wait for all pods to be ready
kubectl wait --for=condition=Ready pods --all -n calico-system --timeout=300s

echo ""
echo "=== Setup Complete ==="
echo ""
echo "Calico Components:"
kubectl get pods -n calico-system
echo ""
echo "Services:"
kubectl get svc -n calico-system
echo ""
echo "Goldmane mTLS Secret:"
kubectl get secret goldmane-key-pair -n calico-system
echo ""
echo "=== Connection Details ==="
echo "Goldmane gRPC: goldmane.calico-system.svc:7443"
echo "Whisker UI:    whisker.calico-system.svc:8081"
echo ""
echo "To access Whisker UI locally:"
echo "  kubectl port-forward svc/whisker 8081:8081 -n calico-system"
echo "  Open: http://localhost:8081"
echo ""
echo "To test Goldmane gRPC locally:"
echo "  kubectl port-forward svc/goldmane 7443:7443 -n calico-system"
echo ""
