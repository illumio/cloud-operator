# Variables
APP_NAME := operator
DOCKER_IMAGE := illumioevanj80/$(APP_NAME)
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
	docker build -t $(DOCKER_IMAGE):latest .

# Push Docker image to Docker Hub
.PHONY: docker-push
docker-push:
	@echo "Pushing Docker image to Docker Hub..."
	docker push $(DOCKER_IMAGE):latest

# Deploy target (build and push Docker image)
.PHONY: deploy
deploy: docker-build docker-push

# Run the Go project
.PHONY: run
run:
	@echo "Running the project..."
	go run $(LDFLAGS) ./cmd/main.go

# Help target to display available commands
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build        Build the Go project"
	@echo "  test         Run tests"
	@echo "  clean        Clean the build"
	@echo "  docker-build Build Docker image"
	@echo "  docker-push  Push Docker image to Docker Hub"
	@echo "  deploy       Build and push Docker image"
	@echo "  run          Run the Go project"
	@echo "  help         Display this help message"