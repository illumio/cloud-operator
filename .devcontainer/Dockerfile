ARG VARIANT=1.24-bookworm
FROM mcr.microsoft.com/vscode/devcontainers/go:${VARIANT}

ARG BUF_VERSION=v1.57.2
ARG GCI_VERSION=v0.13.7
ARG GOLANGCI_VERSION=v2.5.0
ARG HELM_VERSION=v3.18.5
ARG KIND_VERSION=v0.29.0
ARG SBOM_UTILITY=v0.18.1
ARG SYFT_VERSION=v1.31.0

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

RUN go install github.com/bufbuild/buf/cmd/buf@${BUF_VERSION} \
    && go install github.com/daixiang0/gci@${GCI_VERSION} \
    && go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_VERSION} \
    && go install sigs.k8s.io/kind@${KIND_VERSION} \
    && go install github.com/CycloneDX/sbom-utility@${SBOM_UTILITY} \
    && go install github.com/anchore/syft/cmd/syft@${SYFT_VERSION}

USER root

RUN /go/bin/buf completion bash > "/etc/bash_completion.d/buf" \
    && /go/bin/sbom-utility completion bash > "/etc/bash_completion.d/sbom-utility" \
    && /go/bin/syft completion bash > "/etc/bash_completion.d/syft"

USER vscode