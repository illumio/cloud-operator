# Cloud Operator

Kubernetes operator that streams cluster resources, logs, and network flows to Illumio CloudSecure.

## Build and Test Commands

```bash
make build                              # Build the operator binary
make test                               # Run all unit tests
make lint                               # Run golangci-lint
go test ./... -v                        # Run tests with verbose output
go test -run '^TestName$' ./path/to/pkg # Run a single test
```

Run the narrowest relevant tests while developing, then run the broader applicable suite before handing off a change.

## Project Structure

```text
internal/controller/
├── auth/              # OAuth2 authentication and cluster onboarding
├── collector/         # Shared flow collector detection and parsing
├── stream/            # gRPC stream management (core package)
│   ├── manager.go     # Entry point: ConnectStreams()
│   ├── interfaces.go  # StreamClient and StreamClientFactory
│   ├── config/        # Configuration stream (factory and client)
│   │   └── cache/     # Configuration/policy cache
│   ├── flows/         # Network flows stream
│   │   ├── cache/       # Flow aggregation and eviction
│   │   ├── cilium/      # Cilium/Hubble collector
│   │   ├── falco/       # Falco collector
│   │   ├── ovnk/        # OVN-Kubernetes collector
│   │   ├── awsvpccni/   # Standard AWS VPC CNI collector
│   │   └── awsautomode/ # EKS Auto Mode collector
│   ├── logs/          # Log stream (factory and client)
│   └── resources/     # Kubernetes resources stream
├── reconciler/        # Policy reconciliation and field ownership
├── ovn_template_sets/ # OVN-K policy template set binaries
├── k8sclient/         # Kubernetes client wrapper
├── logging/           # Buffered gRPC logging and internal logging
├── hubble/            # Cilium Hubble client
└── testhelper/        # Shared test utilities
```

## Factory Pattern

Streams use the `StreamClient`/`StreamClientFactory` pattern for dependency injection and testability.

Interfaces in `stream/interfaces.go`:

- `StreamClient`: `Run(ctx)`, `SendKeepalive(ctx)`, and `Close()`
- `StreamClientFactory`: `NewStreamClient(ctx, grpcClient)` and `Name()`

Flow collector interfaces in `stream/flows/interfaces.go`:

- `Collector`: `Run(ctx)`
- `CollectorFactory`: `NewCollector(ctx) (Collector, error)`

Collector detection occurs at startup through `DetectFlowCollector()`. The selected factory is passed to `ConnectStreams()`, and `ManageStream()` creates and runs its client.

## Code Style

- Group imports as standard library, external packages, then internal packages; use `gofmt`.
- Wrap errors with useful context using `fmt.Errorf("...: %w", err)`.
- Define interfaces in the consuming package rather than the provider package.
- Keep unit tests beside their source in `*_test.go` files.
- Mock external dependencies and Kubernetes clients where practical.
- Preserve context cancellation through blocking calls and goroutines.
- Do not silently advance polling checkpoints after downstream processing failures.

## Key Entry Points

| Purpose | Location |
| --- | --- |
| Main orchestrator | `internal/controller/stream/manager.go:ConnectStreams()` |
| Stream interfaces | `internal/controller/stream/interfaces.go` |
| Authentication | `internal/controller/auth/authenticator.go:SetUpOAuthConnection()` |
| Flow caching | `internal/controller/stream/flows/cache/cache.go:FlowCache` |
| Collector detection | `internal/controller/stream/flows/detect.go:DetectFlowCollector()` |
| Policy reconciliation | `internal/controller/reconciler/reconciler.go:NewReconciler()` |
| Resource watching | `internal/controller/stream/resources/watcher.go` |
| gRPC internal logging | `internal/controller/logging/grpc_internal_logger.go` |

## Configuration

- Runtime environment configuration is bound in `cmd/main.go` and represented by `internal/controller/stream/config.go:Config`.
- Shared timeouts and intervals live in `internal/controller/stream/constants.go`.
- Cluster credentials are stored in the Kubernetes Secret named `clustercreds`.
- Helm defaults and validation live in `cloud-operator/values.yaml` and `cloud-operator/values.schema.json`; keep them synchronized with deployment environment variables.
