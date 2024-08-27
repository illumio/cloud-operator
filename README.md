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
- go version v1.22.0+
- Kubernetes v1.30+ cluster.
- helm version v3.15.3+

## License

Copyright 2024 Illumio, Inc. All Rights Reserved.

The Illumio Cloud-Operator package is released and distributed as open source software under the Apache License, Version 2.0. You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0.

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific language governing permissions and limitations under the License.

Illumio has no obligation or responsibility related to the package with respect to support, maintenance, availability, security, or otherwise. Please read the entire LICENSE for additional information regarding the permissions and limitations.

For bugs and feature requests, please open a GitHub Issue and label it appropriately.
