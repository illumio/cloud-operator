ARG VARIANT=1.24-bookworm
FROM mcr.microsoft.com/vscode/devcontainers/go:${VARIANT}

ARG BUF_VERSION=v1.30.0
ARG GOLANGCI_VERSION=v1.60.2
ARG HELM_VERSION=v3.15.4
ARG KIND_VERSION=v0.26.0

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

# Install kubectl
RUN curl -fsSL -o /tmp/kubectl "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" && \
    install -o root -g root -m 0755 /tmp/kubectl /usr/local/bin/kubectl && \
    rm -f /tmp/kubectl

# Install Helm
RUN curl -fsSL -o /tmp/helm.tar.gz https://get.helm.sh/helm-${HELM_VERSION}-linux-amd64.tar.gz && \
    tar -zxvf /tmp/helm.tar.gz -C /tmp && \
    ls -l /tmp && \
    ls -l /tmp/linux-amd64 && \
    install -o root -g root -m 0755 /tmp/linux-amd64/helm /usr/local/bin/helm && \
    rm -rf /tmp/helm.tar.gz /tmp/linux-amd64 && \
    helm completion bash > /etc/bash_completion.d/helm

USER vscode

RUN go install github.com/bufbuild/buf/cmd/buf@${BUF_VERSION}
RUN go install sigs.k8s.io/kind@${KIND_VERSION}

USER root

RUN /go/bin/buf completion bash > "/etc/bash_completion.d/buf"

USER vscode