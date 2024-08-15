# Contributing Guide

## GitHub Workflow

Non-Illumio contributors to the project should follow this workflow:

1. Fork the repo
2. Create a new branch on the fork
3. Push the branch to your fork
4. Submit a pull request following [GitHub's standard process](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/proposing-changes-to-your-work-with-pull-requests/about-pull-requests)

## Bug Reporting

> [!CAUTION]
> If you find a bug or issue that you believe to be a security vulnerability, please see the [SECURITY](SECURITY.md) document for instructions on reporting. **Do not file a public GitHub Issue.**

Please report any bugs you find as GitHub issues.

Before reporting any bugs, please do a quick search to see if it has already been reported. If so, please add a comment on the existing issue rather than creating a new one.

While reporting a bug, please provide a minimal example to reproduce the issue.


## Development

### Testing Helm Chart

##### Create a test cluster
```
kind create cluster

helm package .
helm install iilumio cloud-operator-0.0.1.tgz --namespace illumio-cloud --create-namespace
```

> [!NOTE]
> TODO: Insert key creation kubectl command here in order to access private DockerHub repo.

##### Wait for the deployment to be ready
```
kubectl rollout status deployment/iilumio-cloud-operator -n illumio-cloud
```
##### Verify the deployment status
```
DEPLOYMENT_STATUS=$(kubectl get deployment iilumio-cloud-operator -n illumio-cloud -o jsonpath="{.status.conditions[?(@.type=='Available')].status}")
if [ "$DEPLOYMENT_STATUS" != "True" ]; then
  echo "Deployment is not available"
  exit 1
fi
```

##### Verify the pod is running
```
POD_STATUS=$(kubectl get pods -l app=cloud-operator -n illumio-cloud -o jsonpath="{.items[0].status.phase}")
if [ "$POD_STATUS" != "Running" ]; then
  echo "Pod is not running"
  exit 1
fi
```

##### Check logs
```
kubectl logs $POD_NAME
```

##### Delete test cluster
```
kind delete cluster
```

## Release Checklist

TBD

1. Create a [new GitHub release](https://github.com/illumio/cloud-operator/releases) from that commit. Summarize the changes in this release.

TBD