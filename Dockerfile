# Build the manager binary
FROM golang:1.23 AS builder
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download

# Copy the Go source
COPY cmd/main.go cmd/main.go
COPY internal/controller/ internal/controller/
COPY internal/version/ internal/version/
COPY internal/config internal/config
COPY api/ api/

# Build
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -ldflags="-X 'version.version=${VERSION}'" -a -o manager cmd/main.go

RUN go install github.com/google/gops@latest

# Use distroless as minimal base image to package the manager binary
# Temporarily use busybox for debugging (you can remove it later)
FROM gcr.io/distroless/static:nonroot
WORKDIR /

# Copy the manager binary and debugging tools
COPY --from=builder /workspace/manager .
COPY --from=builder /go/bin/gops .

# Install busybox (for debugging)
RUN curl -sSL https://github.com/uclouvain-lsinf1252/busybox-static/releases/download/v1.32.0/busybox-x86_64 > /busybox && chmod +x /busybox

USER 65532:65532

# Set the entrypoint
ENTRYPOINT ["/manager"]