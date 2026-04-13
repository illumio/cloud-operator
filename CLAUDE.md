# Cloud Operator

Kubernetes operator that streams cluster resources, logs, and network flows to Illumio CloudSecure.

## Build & Test Commands

```bash
make build              # Build the operator binary
make test               # Run all unit tests
make lint               # Run golangci-lint
go test ./... -v        # Run tests with verbose output
go test -run '^TestName$' ./path/to/pkg  # Run single test
```

## Project Structure

```
internal/controller/
├── auth/           # OAuth2 authentication, cluster onboarding
├── collector/      # Flow collectors (Cilium, Falco, OVN-K)
├── stream/         # gRPC stream management (core package)
│   ├── manager.go  # Entry point: ConnectStreams()
│   ├── interfaces.go # StreamClient, StreamClientFactory interfaces
│   ├── config/     # Configuration stream (factory + client)
│   ├── flows/      # Network flows stream
│   │   ├── cache/  # Flow cache for aggregation/eviction
│   │   ├── cilium/ # Cilium/Hubble flow collector
│   │   ├── falco/  # Falco flow collector
│   │   └── ovnk/   # OVN-Kubernetes flow collector
│   ├── logs/       # Log stream (factory + client)
│   └── resources/  # K8s resources stream (factory + client)
├── k8sclient/      # Kubernetes client wrapper
├── logging/        # Buffered gRPC log syncer, gRPC internal logging
└── hubble/         # Cilium Hubble client
```

## Factory Pattern

Streams use the **StreamClient/StreamClientFactory** pattern for dependency injection and testability.

**Interfaces** (`stream/interfaces.go`):
- `StreamClient`: `Run(ctx)`, `SendKeepalive(ctx)`, `Close()`
- `StreamClientFactory`: `NewStreamClient(ctx, grpcClient)`, `Name()`

**Flow Collector Interfaces** (`flows/interfaces.go`):
- `FlowCollector`: `Run(ctx)` - simple interface for flow collectors
- `FlowCollectorFactory`: `NewFlowCollector(ctx) (FlowCollector, error)` - interface for creating collectors

**Flow**: Detection at startup in `main.go` via `DetectFlowCollector()` → factories created → passed to `ConnectStreams()` → `ManageStream()` creates clients

## Code Style

- **Imports**: stdlib, external, internal (use `gofmt -w`)
- **Errors**: Wrap with context using `fmt.Errorf("...: %w", err)`
- **Interfaces**: Define in consumer package, not provider
- **Testing**: Unit tests alongside source (`*_test.go`), mocks for external deps

## Key Entry Points

| What | Where |
|------|-------|
| Main orchestrator | `stream/manager.go:ConnectStreams()` |
| Stream interfaces | `stream/interfaces.go` |
| Auth flow | `auth/authenticator.go:SetUpOAuthConnection()` |
| Flow caching | `stream/flows/cache/cache.go:FlowCache` |
| Flow collector detection | `stream/flows/detect.go:DetectFlowCollector()` |
| Resource watching | `stream/resources/watcher.go` |
| gRPC internal logging | `logging/grpc_internal_logger.go` |

## Configuration

- Environment variables → `stream/config.go:Config`
- Timeouts/intervals → `stream/constants.go`
- Cluster credentials → K8s Secret `clustercreds`
