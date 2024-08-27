// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

// CacheManager contains the cache that is used to store seen events.
type CacheManager struct {
	// Cache holds {resources UID: hash(metadata)}.
	cache map[string][32]byte
}
