// Copyright 2026 Illumio, Inc. All Rights Reserved.

package controller

import (
	"time"
)

// Clock abstracts time operations.
type Clock interface {
	// Now returns the current time.
	Now() time.Time

	// Since returns the time elapsed since t.
	Since(t time.Time) time.Duration

	// NewTimer creates a new Timer that will send the current time on its channel after at least duration d.
	NewTimer(d time.Duration) Timer

	// NewTicker creates a new Ticker that sends the current time on its channel with a period specified by the duration.
	NewTicker(d time.Duration) Ticker

	// After returns a channel that receives the current time after at least duration d.
	After(d time.Duration) <-chan time.Time
}

// Timer represents a timer that can be stopped and reset.
type Timer interface {
	// C returns the channel on which the time is delivered.
	C() <-chan time.Time

	// Stop prevents the Timer from firing.
	Stop() bool

	// Reset changes the timer to expire after duration d.
	Reset(d time.Duration) bool
}

// Ticker represents a ticker that delivers ticks at regular intervals.
type Ticker interface {
	// C returns the channel on which ticks are delivered.
	C() <-chan time.Time

	// Stop stops the ticker.
	Stop()
}

// realClock implements Clock using the standard time package.
type realClock struct{}

// NewRealClock returns a Clock implementation using the standard time package.
func NewRealClock() Clock {
	return &realClock{}
}

func (c *realClock) Now() time.Time {
	return time.Now()
}

func (c *realClock) Since(t time.Time) time.Duration {
	return time.Since(t)
}

func (c *realClock) NewTimer(d time.Duration) Timer {
	return &realTimer{timer: time.NewTimer(d)}
}

func (c *realClock) NewTicker(d time.Duration) Ticker {
	return &realTicker{ticker: time.NewTicker(d)}
}

func (c *realClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

type realTimer struct {
	timer *time.Timer
}

func (t *realTimer) C() <-chan time.Time {
	return t.timer.C
}

func (t *realTimer) Stop() bool {
	return t.timer.Stop()
}

func (t *realTimer) Reset(d time.Duration) bool {
	return t.timer.Reset(d)
}

type realTicker struct {
	ticker *time.Ticker
}

func (t *realTicker) C() <-chan time.Time {
	return t.ticker.C
}

func (t *realTicker) Stop() {
	t.ticker.Stop()
}
