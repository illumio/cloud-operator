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

	"gotest.tools/v3/assert"
)

type Cluster struct {
	ID            string `json:"id"`
	IllumioRegion string `json:"illumio_region"`
	Enabled       bool   `json:"enabled"`
	Onboarded     bool   `json:"onboarded"`
}

type ClustersResponse struct {
	Clusters      []Cluster `json:"clusters"`
	TotalSize     int       `json:"total_size"`
	NextPageToken string    `json:"next_page_token"`
	PrevPageToken string    `json:"prev_page_token"`
	Page          int       `json:"page"`
}

func getAuthorizationHeader(key, secret string) string {
	credentials := fmt.Sprintf("%s:%s", key, secret)
	encodedCredentials := base64.StdEncoding.EncodeToString([]byte(credentials))
	return fmt.Sprintf("Basic %s", encodedCredentials)
}

func fetchClusters() ([]Cluster, error) {
	baseURL := "http://example.com/api/v1/k8s_cluster" // Replace with actual URL

	params := url.Values{}
	params.Add("max_results", "10")
	params.Add("sortBy.field", "1")
	params.Add("sortBy.sort_order", "1")
	params.Add("filterBy.field", "ILLUMIO_REGION")
	params.Add("filterBy.value", "aws-us-east-1")

	fullURL := fmt.Sprintf("%s?%s", baseURL, params.Encode())

	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Authorization", getAuthorizationHeader("cloud_", "___"))
	req.Header.Add("Accept", "*/*")
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("X-Tenant-Id", "_____")
	req.Header.Add("X-User-Id", "_____")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status: %v", resp.Status)
	}

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

func TestClusterIsOnboarded(t *testing.T) {
	// Fetch clusters
	data, err := fetchClusters()
	if err != nil {
		t.Fatalf("Failed to fetch clusters: %v", err)
	}

	// Check if the output is not empty
	if len(data) == 0 {
		t.Errorf("Expected non-empty response, got empty response")
	}

	// Parse the response to ensure it matches expected structure
	if len(data) == 0 {
		t.Errorf("Expected 'clusters' in response, got %v", data)
	}

	assert.Equal(t, 1, len(data))
	clusterID := data[0].ID
	offBoardURL := "http://example.com/api/v1/k8s_cluster/offboard"

	// Create the body
	body := map[string][]string{
		"cluster_ids": {clusterID},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("failed to marshal JSON body: %v", err)
	}

	req, err := http.NewRequest("POST", offBoardURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Add("Authorization", getAuthorizationHeader("cloud_", "___"))
	req.Header.Add("Accept", "*/*")
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("X-Tenant-Id", "_____")
	req.Header.Add("X-User-Id", "_____")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("failed to do request: %v", err)
	}
	defer resp.Body.Close()

}
