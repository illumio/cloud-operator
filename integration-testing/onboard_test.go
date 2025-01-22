// Copyright 2024 Illumio, Inc. All Rights Reserved.

package integrationtesting

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/spf13/viper"
)

// Cluster represents a Kubernetes cluster with relevant metadata.
type Cluster struct {
	ID            string `json:"id"`
	IllumioRegion string `json:"illumio_region"`
	Enabled       bool   `json:"enabled"`
	Onboarded     bool   `json:"onboarded"`
}

// ClustersResponse represents the response containing cluster data.
type ClustersResponse struct {
	Clusters      []Cluster `json:"clusters"`
	TotalSize     int       `json:"total_size"`
	NextPageToken string    `json:"next_page_token"`
	PrevPageToken string    `json:"prev_page_token"`
	Page          int       `json:"page"`
}

// Config holds configuration values needed for the API requests.
type Config struct {
	TenantId     string
	CloudIdKey   string
	CloudIdValue string
	UserId       string
}

// NewHTTPClient returns a reusable HTTP client.
func NewHTTPClient() *http.Client {
	return &http.Client{}
}

// getAuthorizationHeader returns the Basic Authorization header using the provided credentials.
func getAuthorizationHeader(key, secret string) string {
	credentials := fmt.Sprintf("%s:%s", key, secret)
	encodedCredentials := base64.StdEncoding.EncodeToString([]byte(credentials))
	return fmt.Sprintf("Basic %s", encodedCredentials)
}

// buildRequest creates a new HTTP request with common headers.
func buildRequest(method, url string, body io.Reader, config Config) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Authorization", getAuthorizationHeader(config.CloudIdKey, config.CloudIdValue))
	req.Header.Add("Accept", "*/*")
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("X-Tenant-Id", config.TenantId)
	req.Header.Add("X-User-Id", config.UserId)

	return req, nil
}

// fetchClusters makes an HTTP request to fetch a list of clusters.
func fetchClusters(config Config) ([]Cluster, error) {
	baseURL := "https://cloud.illum.io/api/v1/k8s_cluster" // Replace with actual URL

	params := url.Values{}
	params.Add("max_results", "10")
	params.Add("sortBy.field", "1")
	params.Add("sortBy.sort_order", "1")
	params.Add("filterBy.field", "ILLUMIO_REGION")
	params.Add("filterBy.value", "aws-us-west-2")

	fullURL := fmt.Sprintf("%s?%s", baseURL, params.Encode())

	req, err := buildRequest("GET", fullURL, nil, config)
	if err != nil {
		return nil, err
	}

	client := NewHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status: %v", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var clustersResponse ClustersResponse
	if err := json.Unmarshal(body, &clustersResponse); err != nil {
		return nil, err
	}

	return clustersResponse.Clusters, nil
}

// offboardCluster sends a request to offboard a cluster by its ID.
func offboardCluster(config Config, clusterIds []string) error {
	offBoardURL := "https://cloud.illum.io/api/v1/k8s_cluster/offboard"

	body := map[string][]string{
		"cluster_ids": clusterIds,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON body: %v", err)
	}

	req, err := buildRequest("PUT", offBoardURL, bytes.NewBuffer(jsonBody), config)
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	client := NewHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("request failed with status: %v", resp.Status)
	}

	return nil
}

// Function to extract UUIDs from the list of clusters
func extractUUIDs(clusters []Cluster) []string {
	var uuids []string
	for _, cluster := range clusters {
		uuids = append(uuids, cluster.ID)
	}
	return uuids
}

// TestClusterIsOnboarded tests if a cluster can be offboarded after fetching it.
func TestClusterIsOnboarded(t *testing.T) {
	// Load configuration from environment variables using Viper
	viper.AutomaticEnv()

	config := Config{
		TenantId:     viper.GetString("TENANT_ID"),
		CloudIdKey:   viper.GetString("CLOUD_API_KEY"),
		CloudIdValue: viper.GetString("CLOUD_SECRET"),
		UserId:       viper.GetString("USER_ID"),
	}

	// Fetch clusters
	clusters, err := fetchClusters(config)
	if err != nil {
		t.Fatalf("Failed to fetch clusters: %v", err)
	}

	// Validate the response
	if len(clusters) == 0 {
		t.Error("Expected non-empty response, got empty response")
	}

	// Check the first cluster and attempt to offboard it
	uuids := extractUUIDs(clusters)
	t.Logf("Attempting to offboard clusters with ID: %s", uuids)

	if err := offboardCluster(config, uuids); err != nil {
		t.Fatalf("Failed to offboard cluster: %v", err)
	}

}
