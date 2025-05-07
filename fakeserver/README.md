# FakeServer

Illumio's cloud-operator streams Kubernetes metadata, logs, and network flows out to a server that you control. The FakeServer provides a convenient way to test the cloud-operator locally without needing a full backend server to collect these streams. It simulates the necessary OAuth endpoints and provides dummy gRPC endpoints to receive the streamed data.

## Running FakeServer

### Standard Mode

From the repository root, run the following command:

```bash
go run ./fakeserver
```

This starts the FakeServer with its gRPC service listening on port `50051` and its HTTP/OAuth service listening on port `50053`.

### Proxy Mode

To test the cloud-operator's ability to connect through an HTTP/HTTPS proxy, you can run FakeServer bundled with a simple CONNECT-only proxy:

```bash
go run ./fakeserver --proxy
```

In this mode:

- The FakeServer itself runs as above (gRPC on `50051`, HTTP/OAuth on `50053`).
- A Proxy Server also starts, listening on port `8888` by default. This proxy handles CONNECT requests and tunnels them to the appropriate FakeServer port (`50051` or `50053`) running on the same host.

## Pointing Cloud-Operator to FakeServer

### 1. Credentials

FakeServer accepts a default set of credentials for the initial onboarding step. You can find these constants in the FakeServer code (or configure FakeServer to expect different ones if needed). The defaults are typically:

- **Client ID**: `client_id_1`
- **Client Secret**: `client_secret_1`

You need to configure the cloud-operator Helm chart (usually via a `values.yaml` file) with these credentials under the `onboardingSecret` section:

```yaml
# Example values snippet for cloud-operator Helm chart
onboardingSecret:
  clientId: "client_id_1"
  clientSecret: "client_secret_1"
```

### 2. Endpoints

The cloud-operator needs to know where to reach the FakeServer's HTTP/OAuth endpoints. Since cloud-operator runs inside a Kubernetes pod (often within a Docker Desktop VM or similar environment), you typically cannot use `localhost`. Instead, use `host.docker.internal`, which Docker Desktop provides as a DNS name resolving to the host machine.

Configure the following environment variables for the cloud-operator deployment:

```yaml
# Example values snippet for cloud-operator Helm chart
# Adjust 'env' section based on your chart's structure
env:
  # Required: Allows connecting to the FakeServer's HTTPS endpoint
  # using its self-signed certificate without validation errors.
  tlsSkipVerify: true

  # Endpoint for the initial onboarding request
  onboardingEndpoint: "https://host.docker.internal:50053/api/v1/k8s_cluster/onboard"

  # Endpoint for exchanging credentials for an access token
  tokenEndpoint: "https://host.docker.internal:50053/api/v1/k8s_cluster/authenticate"

  # --- Configuration for Proxy Mode ---
  # If running FakeServer with the --proxy flag (or using any other proxy),
  # set the HTTPS_PROXY environment variable.
  # The proxy listens on port 8888 by default when run via 'fakeserver --proxy'.
  # httpsProxy: "http://host.docker.internal:8888" # Uncomment this line when using the proxy

  # --- Configuration for gRPC Target (Handled Internally) ---
  # The gRPC target address (e.g., host.docker.internal:50051) is usually
  # configured internally by the operator based on successful authentication,
  # or potentially via other specific environment variables if needed.
  # Ensure the operator is configured to eventually target host.docker.internal:50051
  # when connecting from the container to the host.
```

### Important Note on Proxy Usage

When you set `httpsProxy` for the cloud-operator, its gRPC connections (to port `50051`) and its HTTPS calls (to the OAuth endpoints on port `50053`) will both be directed through the proxy specified. The proxy (either the built-in one started with `fakeserver --proxy` or another like Tinyproxy) must be configured to allow CONNECT requests to both `host.docker.internal:50051` and `host.docker.internal:50053`.

### 3. Putting It All Together

An example `values.yaml` file (`./fakeserver/cloud-operator.fakeserver.yaml`) might look like this:

```yaml
# ./fakeserver/cloud-operator.fakeserver.yaml
# Example values for running cloud-operator against FakeServer

onboardingSecret:
  clientId: "client_id_1"
  clientSecret: "client_secret_1"

deployment:
  env:
    - name: ILLUMIO_TLS_SKIP_VERIFY # Use the actual env var name expected by the operator
      value: "true"
    - name: ILLUMIO_ONBOARDING_ENDPOINT
      value: "https://host.docker.internal:50053/api/v1/k8s_cluster/onboard"
    - name: ILLUMIO_TOKEN_ENDPOINT
      value: "https://host.docker.internal:50053/api/v1/k8s_cluster/authenticate"

    # --- UNCOMMENT FOR PROXY MODE ---
    # - name: HTTPS_PROXY # Or the specific env var the operator uses for proxy
    #   value: "http://host.docker.internal:8888"
    # - name: HTTP_PROXY # Might be needed if operator makes plain HTTP calls
    #   value: "http://host.docker.internal:8888"
    # - name: NO_PROXY # Ensure Kubernetes internal communication isn't proxied
    #   value: "kubernetes.default.svc,.svc,.cluster.local"

# Add other necessary chart values (image repository, tags, resource limits, etc.)
```

You can then install the Helm chart using these values:

```bash
# Ensure FakeServer is running (either standard or --proxy mode)

# Install the chart (adjust chart name/path and release name as needed)
helm install illumio-operator ./charts/cloud-operator \
  --namespace illumio-cloud \
  --create-namespace \
  --values ./fakeserver/cloud-operator.fakeserver.yaml
```

Monitor the logs of both FakeServer and the cloud-operator pod to verify successful onboarding, authentication, and stream connections.