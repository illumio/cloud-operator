# IPFIX Template Sets ConfigMap

## Overview
This document explains how the binary data for the ConfigMap `ipfix-template-sets.yaml` was obtained and its purpose in the Kubernetes cluster.

## Purpose
The ConfigMap `ipfix-template-sets.yaml` contains binary data required for IPFIX (IP Flow Information Export) processing. This binary data is used by the cloud operator to decode IPFIX packets and process network flows immediatly upon recieving packets. (Do not need to wait 10 minutes)

## Steps to Obtain the Binary Data

1. **Generate the Binary File**:
   - The binary file `openvswitch.bin` was generated using an IPFIX exporter.
   - Within the OVN collector code I manually intercepted and wrote the packet that continaed the template sets to a local volume mount within an OVN-k enabled cluster.

2. **Save the Binary File**:
   - The extracted binary data was saved to a file named `openvswitch.bin`.

3. **Create the ConfigMap**:
   - The binary file was added to the ConfigMap using the `binaryData` field.
   - Example ConfigMap YAML:
     ```yaml
     apiVersion: v1
     kind: ConfigMap
     metadata:
       name: ipfix-template-sets
     binaryData:
       openvswitch.bin: <data>
     ```


## Usage
The ConfigMap is mounted as a volume in the cloud operator pod. The operator reads the binary data from the mounted volume and uses it to decode IPFIX packets.

