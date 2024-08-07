#!/bin/bash


kind create cluster


helm package .
helm install cloud-operator-0.0.1.tgz . --namespace illumio-cloud --create-namespace

# INSERT KEY CREATION KUBECTL COMMAND HERE IN ORDER TO ACCESS PRIVATE DOCKERHUB REPO 

# Wait for the deployment to be ready
kubectl rollout status deployment/cloud-operator-0.0.1.tgz-cloud-operator -n illumio-cloud

# Verify the deployment status
DEPLOYMENT_STATUS=$(kubectl get deployment cloud-operator-0.0.1.tgz-cloud-operator -n illumio-cloud -o jsonpath="{.status.conditions[?(@.type=='Available')].status}")
if [ "$DEPLOYMENT_STATUS" != "True" ]; then
  echo "Deployment is not available"
  exit 1
fi

# Verify the pod is running
POD_STATUS=$(kubectl get pods -l app=cloud-operator -n illumio-cloud -o jsonpath="{.items[0].status.phase}")
if [ "$POD_STATUS" != "Running" ]; then
  echo "Pod is not running"
  exit 1
fi

# Check logs
kubectl logs $POD_NAME

kind delete cluster

echo "All tests passed"
