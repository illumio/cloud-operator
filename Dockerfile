# Build the manager binary
FROM golang:1.24.4 AS builder
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.sum ./

# Cache dependencies before copying the source to avoid re-downloading
RUN go mod download

# Copy the Go source
COPY cmd/main.go cmd/main.go
COPY internal/controller/ internal/controller/
COPY internal/pkg/ internal/pkg/
COPY internal/version/ internal/version/
COPY api/ api/

# Build the Go binary
RUN CGO_ENABLED=0 go build -ldflags="-X 'github.com/illumio/cloud-operator/internal/version.version=${VERSION}'" -a -o manager cmd/main.go

# Install gops for troubleshooting
RUN CGO_ENABLED=0 go install github.com/google/gops@latest

# Use distroless as minimal base image to package the manager binary
FROM gcr.io/distroless/static:nonroot

# Copy binaries
WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=builder /go/bin/gops /sbin/gops

USER 65532:65532

# Configure gops
ENV GOPS_CONFIG_DIR="/var/run/gops"

# Set the entrypoint for your app
ENTRYPOINT ["/manager"]
