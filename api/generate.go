// Copyright 2024 Illumio, Inc. All Rights Reserved.

package internal

//go:generate buf format -w illumio/cloud/k8sclustersync/v1/k8s_info.proto
//go:generate buf lint illumio/cloud/k8sclustersync/v1/k8s_info.proto
//go:generate buf generate illumio/cloud/k8sclustersync/v1/k8s_info.proto

//go:generate buf format -w illumio/cloud/goldmane/v1/goldmane.proto
//go:generate buf lint illumio/cloud/goldmane/v1/goldmane.proto
//go:generate buf generate illumio/cloud/goldmane/v1/goldmane.proto
