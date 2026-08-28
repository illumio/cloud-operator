// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	// gkeMetadataBaseURL is the root of the GKE metadata server. The GKE metadata
	// server intercepts requests to metadata.google.internal (169.254.169.254)
	// locally on the node and serves a curated subset of instance/project
	// attributes — including cluster-name, cluster-location, cluster-uid, and
	// project-id — to workloads. These attributes are unauthenticated (no
	// IAM/RBAC required); only the sensitive service-account-token endpoints are
	// gated.
	//
	// On GKE Autopilot, Workload Identity is always enabled, so the metadata
	// server DaemonSet runs on every node and these endpoints are always
	// reachable.
	gkeMetadataBaseURL = "http://metadata.google.internal/computeMetadata/v1"

	// Metadata paths (relative to gkeMetadataBaseURL) for the GKE cluster
	// identity — together the (project, location, name) key the backend uses to
	// link this cluster to its GKE ContainerCluster inventory object.
	gkeMetadataClusterNamePath     = "/instance/attributes/cluster-name"
	gkeMetadataClusterLocationPath = "/instance/attributes/cluster-location"
	gkeMetadataProjectIDPath       = "/project/project-id"

	// gkeMetadataFlavorHeader and gkeMetadataFlavorValue are required on every
	// request to the metadata server; the server rejects requests without them.
	gkeMetadataFlavorHeader = "Metadata-Flavor"
	gkeMetadataFlavorValue  = "Google"

	// gkeVersionMarker identifies a GKE control-plane version string
	// (e.g. "v1.34.4-gke.1193000"). It is present on both Standard and Autopilot
	// clusters, so it identifies GKE but not the cluster mode. We only use it to
	// avoid firing the metadata call on non-GKE providers (EKS/AKS/self-managed),
	// where metadata.google.internal would not resolve to a GKE metadata server.
	gkeVersionMarker = "-gke."

	// gkeMetadataTimeout bounds a single best-effort metadata call. The endpoint
	// is node-local, so this only needs to cover the loopback round-trip; a
	// failure here must never block or fail the resource stream.
	gkeMetadataTimeout = 2 * time.Second
)

// gkeClusterInfo is the GKE-reported cluster identity read from the metadata
// server. Any field may be empty if that attribute was unreachable; the caller
// only sends the fields it actually got.
type gkeClusterInfo struct {
	Name      string
	Location  string
	ProjectID string
}

// isGKEVersion reports whether a Kubernetes server version string denotes a GKE
// cluster (Standard or Autopilot).
func isGKEVersion(gitVersion string) bool {
	return strings.Contains(gitVersion, gkeVersionMarker)
}

// resolveGKEClusterInfo fetches the GKE cluster name, location, and project ID
// from the GKE metadata server. It is best-effort: each attribute is fetched
// independently, and any error (non-GKE node, metadata server disabled, network
// hiccup) leaves that field empty. It never returns an error, so it can never
// fail the stream.
//
// The calls target the metadata.google.internal hostname, which only resolves to
// a metadata server on GCP nodes; it is therefore safe with respect to other
// clouds (it will never reach, e.g., the AWS IMDS). The caller still gates on
// isGKEVersion to avoid pointless DNS/HTTP attempts on known-non-GKE clusters.
func resolveGKEClusterInfo(ctx context.Context, logger *zap.Logger, client *http.Client) gkeClusterInfo {
	return resolveGKEClusterInfoFrom(ctx, logger, client, gkeMetadataBaseURL)
}

// resolveGKEClusterInfoFrom is resolveGKEClusterInfo with an injectable base URL,
// for testing against an httptest server.
func resolveGKEClusterInfoFrom(ctx context.Context, logger *zap.Logger, client *http.Client, baseURL string) gkeClusterInfo {
	name, _ := fetchGKEMetadata(ctx, logger, client, baseURL+gkeMetadataClusterNamePath)
	location, _ := fetchGKEMetadata(ctx, logger, client, baseURL+gkeMetadataClusterLocationPath)
	projectID, _ := fetchGKEMetadata(ctx, logger, client, baseURL+gkeMetadataProjectIDPath)

	return gkeClusterInfo{
		Name:      name,
		Location:  location,
		ProjectID: projectID,
	}
}

// fetchGKEMetadata performs a single best-effort GET against a metadata server
// URL and returns the trimmed body. It returns ("", false) on any error or
// non-OK status; it never returns an error so it cannot fail the stream.
func fetchGKEMetadata(ctx context.Context, logger *zap.Logger, client *http.Client, url string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, gkeMetadataTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		logger.Debug("Failed to build GKE metadata request", zap.String("url", url), zap.Error(err))

		return "", false
	}

	req.Header.Set(gkeMetadataFlavorHeader, gkeMetadataFlavorValue)

	resp, err := client.Do(req)
	if err != nil {
		logger.Debug("GKE metadata server not reachable", zap.String("url", url), zap.Error(err))

		return "", false
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		logger.Debug("GKE metadata server returned non-OK status",
			zap.String("url", url),
			zap.Int("status_code", resp.StatusCode))

		return "", false
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		logger.Debug("Failed to read GKE metadata response body", zap.String("url", url), zap.Error(err))

		return "", false
	}

	value := strings.TrimSpace(string(body))
	if value == "" {
		return "", false
	}

	return value, true
}
