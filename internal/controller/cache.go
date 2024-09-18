// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

// Cache contains the cache that is used to store seen events.
type Cache struct {
	// Cache holds {resources UID: hash(metadata)}.
	cache map[string][32]byte
}
