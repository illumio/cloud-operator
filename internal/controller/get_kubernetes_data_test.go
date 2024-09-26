// Copyright 2024 Illumio, Inc. All Rights Reserved

package controller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

func TestConvertMetaObjectToMetadata(t *testing.T) {
	sampleData := make(map[string]string)
	resource := "test-resource"
	creationTimestamp := metav1.Time{Time: time.Now()}
	objMeta := metav1.ObjectMeta{
		Annotations:       sampleData,
		CreationTimestamp: creationTimestamp,
		Labels:            sampleData,
		Name:              "test-name",
		Namespace:         "test-namespace",
		ResourceVersion:   "test-version",
		UID:               "test-uid",
	}

	expected := &pb.KubernetesObjectMetadata{
		Annotations:       sampleData,
		CreationTimestamp: convertToProtoTimestamp(creationTimestamp),
		Kind:              resource,
		Labels:            sampleData,
		Name:              "test-name",
		Namespace:         "test-namespace",
		ResourceVersion:   "test-version",
		Uid:               "test-uid",
	}

	result := convertMetaObjectToMetadata(objMeta, resource)
	assert.Equal(t, expected, result)
}

func TestConvertToProtoTimestamp(t *testing.T) {
	k8sTime := metav1.Time{Time: time.Now()}
	expected := timestamppb.New(k8sTime.Time)

	result := convertToProtoTimestamp(k8sTime)
	assert.Equal(t, expected, result)
}
