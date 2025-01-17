# Step 1: Build the manager binary
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
COPY internal/config internal/config
COPY api/ api/

# Build the manager binary
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -ldflags="-X 'version.version=${VERSION}'" -a -o manager cmd/main.go

# Install debugging tools (including bash) and gops for troubleshooting
RUN go install github.com/google/gops@latest

# Step 2: Use Alpine as base image for debugging
FROM alpine:latest AS debug

# Install bash and any other debugging tools you need
RUN apk update && \
    apk add --no-cache bash curl ca-certificates

# Copy the manager binary and gops (from the builder)
WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=builder /go/bin/gops .

# Add a non-root user for security
RUN adduser -D myuser
USER myuser

# Set the entrypoint for your app
ENTRYPOINT ["/manager"]

# Step 3: Finalize the image for debugging
# This will allow you to exec into the container and interact with the shell
CMD ["/bin/bash"]
