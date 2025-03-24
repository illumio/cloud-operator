# Variables
APP_NAME := operator
DOCKER_USERNAME := aaronnguyenillumio
DOCKER_IMAGE := $(DOCKER_USERNAME)/$(APP_NAME)
LOCAL_REGISTRY := localhost:5000
LOCAL_IMAGE := $(LOCAL_REGISTRY)/$(APP_NAME)
COMMIT := $(shell git rev-parse --short HEAD)
DATE := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
LDFLAGS := -ldflags "-X main.Version=latest -X main.Commit=$(COMMIT) -X main.Date=$(DATE)"

# Default target
.PHONY: all
all: build

# Build the Go project
.PHONY: build
build:
	@echo "Building the project..."
	go build $(LDFLAGS) -o $(APP_NAME) ./cmd

# Run tests
.PHONY: test
test:
	@echo "Running tests..."
	go test ./...

# Clean the build
.PHONY: clean
clean:
	@echo "Cleaning up..."
	rm -f $(APP_NAME)

# Build Docker image
.PHONY: docker-build
docker-build:
	@echo "Building Docker image..."
	docker buildx build --platform linux/amd64,linux/arm64 --load -t $(DOCKER_IMAGE):latest .

# Push Docker image to Docker Hub
.PHONY: docker-push
docker-push:
	@echo "Pushing Docker image to Docker Hub..."
	docker push $(DOCKER_IMAGE):latest

# Deploy target (build and push Docker image to Docker Hub)
.PHONY: deploy
deploy: docker-build docker-push

# Create and run a local Docker registry
.PHONY: local-registry
local-registry:
	@echo "Creating Local Docker Registry..."
	docker run -d -p 5000:5000 --name registry registry:2 || echo "Local registry already running"

# Build Docker image for local registry
.PHONY: docker-build-local
docker-build-local: build
	@echo "Building Docker image for local registry..."
	docker build -t $(LOCAL_IMAGE):latest .

# Build Docker image for local registry and push it
.PHONY: docker-push-local
docker-push-local: docker-build-local
	@echo "Pushing Docker image to local registry..."
	docker push $(LOCAL_IMAGE):latest

# Deploy target (build and push Docker image to local registry)
.PHONY: deploy-local
deploy-local: docker-build-local docker-push-local

# Run the Go project
.PHONY: run
run:
	@echo "Running the project..."
	go run $(LDFLAGS) ./cmd/main.go

# Help target to display available commands
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build              Build the Go project"
	@echo "  test               Run tests"
	@echo "  clean              Clean the build"
	@echo "  docker-build       Build Docker image"
	@echo "  docker-push        Push Docker image to Docker Hub"
	@echo "  deploy             Build and push Docker image to Docker Hub"
	@echo "  local-registry     Create and run a local Docker registry"
	@echo "  docker-build-local Build Docker image for local registry"
	@echo "  docker-push-local  Push Docker image to local registry"
	@echo "  deploy-local       Build and push Docker image to local registry"
	@echo "  run                Run the Go project"
	@echo "  help               Display this help message"