ARG VARIANT=1.22
FROM mcr.microsoft.com/vscode/devcontainers/go:${VARIANT}

ARG BUF_VERSION=v1.30.0

USER root

# Install basic tools
RUN export DEBIAN_FRONTEND=noninteractive \
    && apt-get update \
    && apt-get -y install --no-install-recommends \
    bash-completion \
    git \
    gnupg \
    software-properties-common \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

USER vscode

RUN go install github.com/bufbuild/buf/cmd/buf@${BUF_VERSION}

USER root

RUN /go/bin/buf completion bash > "/etc/bash_completion.d/buf"

USER vscode