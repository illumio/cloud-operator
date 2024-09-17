#!/bin/bash

# Create a directory to store the proto files
mkdir -p proto/cilium

# Download the proto files
curl -o cilium/flow.proto https://raw.githubusercontent.com/cilium/cilium/master/api/v1/flow/flow.proto
curl -o cilium/observer.proto https://raw.githubusercontent.com/cilium/cilium/master/api/v1/observer/observer.proto
curl -o cilium/relay.proto https://raw.githubusercontent.com/cilium/cilium/master/api/v1/relay/relay.proto