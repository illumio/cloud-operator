# Cloud-operator
The Illumio Cloud-operator is deployed as a Deployment on a desired cluster to stream information about the cluster's resources and network traffic to CloudSecure, and to enforce Illumio network policies by managing corresponding k8s NetworkPolicies in the cluster.

## Getting Started

### Deployment Instructions
#### Prerequisites
Ensure you have Helm installed and configured on your local machine.
Ensure you have access to a Kubernetes cluster and the necessary permissions to deploy resources.

#### Packaging the Helm Chart
First, package the Helm chart. This will create a .tgz file that can be used for installation.
```
helm package .
```
This command will generate a file named `cloud-operator-0.0.1.tgz` (or similar, depending on your chart version) in the current directory.

#### Installing the Helm Chart

TODO - How to set values.yaml, set through UI or through terraform.

To install the Helm chart, use the following command:
```
helm install illumio cloud-operator-0.0.1.tgz --namespace illumio-cloud --create-namespace
```
This command will:

1. Install the Helm chart with the release name `illumio`.
1. Use the packaged chart file `cloud-operator-0.0.1.tgz`.
1. Deploy the resources into the `illumio-cloud` namespace.
1. Create the `illumio-cloud` namespace if it does not already exist.

#### Verifying the Installation.
To verify that the Helm chart has been successfully installed, you can use the following command:

```
helm list --namespace illumio-cloud
```
This will list all the Helm releases in the illumio-cloud namespace, including the illumio release if the installation was successful.

Uninstalling the Helm Chart
If you need to uninstall the Helm chart, use the following command:

```
helm uninstall illumio --namespace illumio-cloud
```
This will delete all the resources associated with the `illumio` release from the `illumio-cloud` namespace.

### Prerequisites
- go version v1.22.2+
- Kubernetes v1.30+ cluster.
- helm version v3.15.4+

## Making a private build to Docker hub

The following `make` command will build and push a private build to `docker
hub`.

```
docker login
make docker-build docker-push DOCKER_USERNAME=arisweedler386
```

## Making a private build to a local registry (if testing with a local cluster)

### Creating a minikube clsuter

The following command starts a minikube cluster that will allow you to pull from your local registry
`
minikube start --insecure-registry="host.docker.internal:5000"
`

### Creating a local registry for your Cloud-Operator images

To create a local registry please use the following `make` command
`
make local-registry
`

### Building a local image to registry

Once you have made your local changes, the following `make` command will build and push to your local registry
`
make deploy-local
`

### Deploying local image to Kubernetes

To deploy using helm and to test the operator using fakeserver here is an example of the command with `--set` args
`
helm install illumio --namespace illumio-cloud oci://ghcr.io/illumio/charts/cloud-operator --version v1.0.5 --create-namespace \
 --values ./fakeserver/cloud-operator.fakeserver.yaml,./cloud-operator.image.yaml
`

## Proxy Configuration

### Configuring the Proxy

1. The `HTTPS_PROXY` environment variable is set in the Helm file, which is read when the client is launched into a Kubernetes container. This ensures that the proxy server is correctly configured for the environment.

2. Ensure that the proxy server is accessible from the environment where the Cloud Operator is running.

### Behavior

- **HTTP Requests**:
  - All HTTP requests made by the Cloud Operator will respect the `HTTPS_PROXY` environment variable.
  - The `http.ProxyFromEnvironment` function is used to determine the proxy settings.

- **gRPC Requests**:
  - All gRPC requests will also respect the `HTTPS_PROXY` environment variable.


# IPFIX Template Sets ConfigMap

## Overview
This document explains how the binary data for the ConfigMap `ipfix-template-sets.yaml` was obtained and its purpose in the Kubernetes cluster.

## Purpose
The ConfigMap `ipfix-template-sets.yaml` contains binary data required for IPFIX (IP Flow Information Export) processing. This binary data is used by the cloud operator to decode IPFIX packets and process network flows immediatly upon recieving packets. (Do not need to wait 10 minutes)

## Steps to Obtain the Binary Data

1. **Generate the Binary File**:
   - The binary file `openvswitch.bin` was generated using an IPFIX exporter.
   - Within the OVN collector code I manually intercepted and wrote the packet that continaed the template sets to a local volume mount within an OVN-k enabled cluster.

2. **Save the Binary File**:
   - The extracted binary data was saved to a file named `openvswitch.bin`.

3. **Create the ConfigMap**:
   - The binary file was added to the ConfigMap using the `binaryData` field.
   - Example ConfigMap YAML:
     ```yaml
     apiVersion: v1
     kind: ConfigMap
     metadata:
       name: ipfix-template-sets
     binaryData:
       openvswitch.bin: <data>
     ```


## Usage
The ConfigMap is mounted as a volume in the cloud operator pod. The operator reads the binary data from the mounted volume and uses it to decode IPFIX packets.


## License

Copyright 2024 Illumio, Inc. All Rights Reserved.

The Illumio Cloud-Operator package is released and distributed as open source software under the Apache License, Version 2.0. You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0.

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific language governing permissions and limitations under the License.

Illumio has no obligation or responsibility related to the package with respect to support, maintenance, availability, security, or otherwise. Please read the entire LICENSE for additional information regarding the permissions and limitations.

For bugs and feature requests, please open a GitHub Issue and label it appropriately.
