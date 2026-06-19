# GitHub Copilot Instructions — cloud-operator

These instructions guide Copilot when reviewing pull requests or assisting with code in this repository.

## What This Project Does

`cloud-operator` is a Kubernetes operator that streams cluster resources, logs, and network flows to Illumio CloudSecure over gRPC. It is written in Go and runs inside a Kubernetes cluster.

---

## Review Checklist for Every PR

### 1. Correctness & Logic
- Verify that new or changed logic matches the intent described in the PR description.
- Check that context cancellation (`ctx`) is propagated correctly through all goroutines and gRPC calls.
- Ensure errors are always wrapped with context: `fmt.Errorf("what failed: %w", err)`. Bare `errors.New` or unwrapped returns are a red flag.
- Confirm that goroutines started in `Run(ctx)` methods respect context cancellation and do not leak.

### 2. Factory Pattern Compliance
All stream types must follow the **StreamClient / StreamClientFactory** pattern:
- `StreamClient` interface: `Run(ctx context.Context) error`, `SendKeepalive(ctx context.Context) error`, `Close()`
- `StreamClientFactory` interface: `NewStreamClient(ctx context.Context, grpcClient ...) (StreamClient, error)`, `Name() string`
- Flow collectors use `FlowCollector` / `FlowCollectorFactory` defined in `stream/flows/interfaces.go`.
- New stream or collector types **must** implement these interfaces; direct instantiation that bypasses the factory pattern should be flagged.

### 3. Interfaces Defined in Consumer Package
Go interfaces must be defined in the **consumer** package, not in the package that implements them. Flag any interface that lives next to its concrete implementation.

### 4. Import Ordering
Imports must follow the three-group convention enforced by `gofmt`:
1. Standard library
2. External (third-party) packages
3. Internal (`github.com/illumio/cloud-operator/...`) packages

Flag imports that mix these groups or are unformatted.

### 5. Testing
- Every new exported function or method should have a corresponding `_test.go` file in the same package.
- Tests must use mocks or fakes for external dependencies (gRPC connections, Kubernetes API server, Hubble client).
- Shared test utilities belong in `internal/controller/testhelper/`.
- Do not remove or weaken existing tests.

### 6. Security
- No credentials, tokens, API keys, or secrets may be hard-coded or logged.
- Cluster credentials are sourced exclusively from the `clustercreds` Kubernetes Secret; any new credential path must go through the same mechanism.
- gRPC connections must use TLS; flag any plaintext dial options (`grpc.WithInsecure()` or `insecure.NewCredentials()` without justification).
- Validate all inputs that come from external sources (Kubernetes events, gRPC messages).

### 7. Configuration
- All tunable values (timeouts, intervals, retry counts) must come from `stream/constants.go` or be driven by `stream/config.go:Config` (populated from environment variables).
- Magic numbers inline in logic are a red flag.

### 8. Error Handling & Retries
- Functions that call gRPC endpoints or the Kubernetes API must handle transient errors with appropriate retry/backoff logic (see existing patterns in `stream/manager.go`).
- Fatal errors should be logged before the process exits; do not silently swallow errors.

### 9. Resource / Memory Management
- Verify that all `Close()` methods are called (typically via `defer`) for streams, connections, and watchers.
- Check that caches (`stream/flows/cache/`, `stream/config/cache/`) are bounded and have eviction logic to prevent unbounded growth.

### 10. Kubernetes Best Practices
- New RBAC rules added to the Helm chart must follow least-privilege: only the exact verbs and resources needed.
- Label selectors and namespace filters should be consistent with existing patterns in `stream/resources/watcher.go`.

---

## Key Entry Points (Quick Reference)

| Concern | File |
|---|---|
| Main stream orchestrator | `internal/controller/stream/manager.go` — `ConnectStreams()` |
| Stream interfaces | `internal/controller/stream/interfaces.go` |
| Auth / onboarding | `internal/controller/auth/authenticator.go` — `SetUpOAuthConnection()` |
| Flow cache | `internal/controller/stream/flows/cache/cache.go` |
| Flow collector detection | `internal/controller/stream/flows/detect.go` — `DetectFlowCollector()` |
| Policy reconciliation | `internal/controller/reconciler/reconciler.go` — `NewReconciler()` |
| Resource watcher | `internal/controller/stream/resources/watcher.go` |
| gRPC internal logging | `internal/controller/logging/grpc_internal_logger.go` |

---

## Build & Test Commands

```bash
make build      # compile the operator binary
make test       # run all unit tests
make lint       # run golangci-lint
```

Run these in CI before merging. A PR should not be approved if `make lint` or `make test` fails.

---

## Things to Flag (Do Not Merge Without Resolution)

- [ ] Secrets or credentials committed in source code
- [ ] Goroutines that ignore context cancellation
- [ ] New stream/collector type that bypasses the factory pattern
- [ ] Hard-coded magic numbers for timeouts or intervals
- [ ] Plaintext (non-TLS) gRPC connections
- [ ] Missing or deleted unit tests for changed logic
- [ ] Interfaces defined in the provider package instead of the consumer package
- [ ] Unbounded caches without eviction
