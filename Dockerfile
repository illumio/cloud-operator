# Build the manager binary
FROM golang:1.23 AS builder
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# Cache dependencies before copying the source to avoid re-downloading
RUN go mod download

# Copy the Go source
COPY cmd/main.go cmd/main.go
COPY internal/controller/ internal/controller/
COPY internal/version/ internal/version/
COPY api/ api/
# Build

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -ldflags="-X 'github.com/illumio/cloud-operator/internal/version.version=${VERSION}'" -a -o manager cmd/main.go

# Install debugging tools (including bash) and gops for troubleshooting
RUN go install github.com/google/gops@latest

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot

# Copy the manager binary and gops (from the builder)
WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=builder /go/bin/gops .
USER 65532:65532

# Set the entrypoint for your app
ENTRYPOINT ["/manager"]
