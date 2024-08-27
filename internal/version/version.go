// Copyright 2024 Illumio, Inc. All Rights Reserved.

package version

var (
	version = "dev"
)

// Version returns the version of the operator.
func Version() string {
	return version
}
