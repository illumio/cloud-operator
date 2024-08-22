ARG VARIANT=1.22
FROM mcr.microsoft.com/vscode/devcontainers/go:${VARIANT}

ARG BUF_VERSION=v1.30.0
ARG GOLANGCI_VERSION=v1.60.2

USER root

# Install basic tools
RUN export DEBIAN_FRONTEND=noninteractive \
    && apt-get update \
    && apt-get -y install --no-install-recommends \
    bash-completion \
    curl \
    git \
    gnupg \
    software-properties-common \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin ${GOLANGCI_VERSION}

# Install Helm
RUN curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 && chmod 700 get_helm.sh && ./get_helm.sh

USER vscode

RUN go install github.com/bufbuild/buf/cmd/buf@${BUF_VERSION}

USER root

RUN /go/bin/buf completion bash > "/etc/bash_completion.d/buf"

USER vscode