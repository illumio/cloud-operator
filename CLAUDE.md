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
│   ├── config/     # Configuration stream
│   ├── flows/      # Network flows stream
│   ├── logs/       # Log stream
│   └── resources/  # K8s resources stream
├── k8sclient/      # Kubernetes client wrapper
├── logging/        # Buffered gRPC log syncer
└── hubble/         # Cilium Hubble client
```

## Code Style

- **Imports**: stdlib, external, internal (use `gofmt -w`)
- **Errors**: Wrap with context using `fmt.Errorf("...: %w", err)`
- **Interfaces**: Define in consumer package, not provider
- **Testing**: Unit tests alongside source (`*_test.go`), mocks for external deps

## Key Entry Points

| What | Where |
|------|-------|
| Main orchestrator | `stream/manager.go:ConnectStreams()` |
| Auth flow | `auth/authenticator.go:SetUpOAuthConnection()` |
| Flow caching | `stream/cache.go:FlowCache` |
| Resource watching | `stream/resources/watcher.go` |

## Configuration

- Environment variables → `stream/types.go:EnvironmentConfig`
- Timeouts/intervals → `stream/constants.go`
- Cluster credentials → K8s Secret `clustercreds`
