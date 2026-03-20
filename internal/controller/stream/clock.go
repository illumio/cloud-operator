// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import "time"

// Clock provides an interface for time operations, enabling testing with fake clocks.
type Clock interface {
	Now() time.Time
	Since(t time.Time) time.Duration
	NewTicker(d time.Duration) *time.Ticker
	After(d time.Duration) <-chan time.Time
}

// RealClock implements Clock using the standard time package.
type RealClock struct{}

// NewRealClock creates a new RealClock instance.
func NewRealClock() *RealClock {
	return &RealClock{}
}

func (RealClock) Now() time.Time {
	return time.Now()
}

func (RealClock) Since(t time.Time) time.Duration {
	return time.Since(t)
}

func (RealClock) NewTicker(d time.Duration) *time.Ticker {
	return time.NewTicker(d)
}

func (RealClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}
