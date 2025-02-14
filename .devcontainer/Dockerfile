ARG VARIANT=1.23-bookworm
FROM mcr.microsoft.com/vscode/devcontainers/go:${VARIANT}

ARG BUF_VERSION=v1.30.0
ARG GOLANGCI_VERSION=v1.60.2
ARG HELM_VERSION=v3.15.4

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
RUN curl -fsSL -o /tmp/helm.tar.gz https://get.helm.sh/helm-${HELM_VERSION}-linux-amd64.tar.gz && \
    tar -zxvf /tmp/helm.tar.gz -C /tmp && \
    ls -l /tmp && \
    ls -l /tmp/linux-amd64 && \
    mv /tmp/linux-amd64/helm /usr/local/bin/helm && \
    chmod +x /usr/local/bin/helm && \
    rm -rf /tmp/helm.tar.gz /tmp/linux-amd64 && \
    helm completion bash > /etc/bash_completion.d/helm

USER vscode

RUN go install github.com/bufbuild/buf/cmd/buf@${BUF_VERSION}
RUN go install sigs.k8s.io/kind@v0.26.0

USER root

RUN /go/bin/buf completion bash > "/etc/bash_completion.d/buf"

USER vscode