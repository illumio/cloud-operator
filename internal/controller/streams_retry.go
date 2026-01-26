package controller

import (
	"cmp"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

type backoffOpts struct {
	InitialBackoff       time.Duration
	MaxBackoff           time.Duration
	MaxJitterPct         float64
	SevereErrorThreshold int
	ExponentialFactor    float64
	Logger               *zap.Logger

	// ActionTimeToConsiderSuccess is used in the case that the happy-path of our
	// function is blocking forever. A function like this may have issues that
	// require exponentialBackoff but should be considered recovered after
	// the action runs for ActionTimeToConsiderSuccess.
	ActionTimeToConsiderSuccess time.Duration
}

var _ zapcore.ObjectMarshaler = &backoffOpts{}

func (a backoffOpts) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	enc.AddDuration("initial_backoff", a.InitialBackoff)
	enc.AddDuration("max_backoff", a.MaxBackoff)
	enc.AddFloat64("exponential_factor", a.ExponentialFactor)
	enc.AddFloat64("max_jitter_pct", a.MaxJitterPct)
	enc.AddInt("severe_error_threshold", a.SevereErrorThreshold)

	return nil
}

type Action func() error

// clamp is a utility function. It ensures that val is between minimum, maximum.
//
// Examples:
//
//	clamp(1, 2, 3) // returns 2
//	clamp(1, 0, 3) // returns 1
//	clamp(1, 4, 3) // returns 3
//
// It is a more semantic way to spell a composition of max/min.
func clamp[T cmp.Ordered](minimum, val, maximum T) T {
	if val < minimum {
		return minimum
	}

	if val > maximum {
		return maximum
	}

	return val
}

type state struct {
	backoff             time.Duration
	timer               *time.Timer
	consecutiveFailures int
	opts                backoffOpts
}

func exponentialBackoff(opts backoffOpts, action Action) error {
	s := state{
		backoff:             opts.InitialBackoff,
		consecutiveFailures: 0,
		// Don't wait before the first attempt to execute the action.
		timer: time.NewTimer(0),
		opts:  opts,
	}
	defer s.timer.Stop()

	for range s.timer.C {
		startTime := time.Now()

		err := action()
		if err == nil {
			s.HappyPathResetBackoff()

			continue
		}

		// Let's say we fail 7 times, then succeed for 45 minutes before
		// failing again. Sometimes this 45 minute period of good behavior should be
		// considered a success, even though the function didn't return "no error".
		//
		// We will reset the consecutiveFailures count back to 0, then we can rely
		// on the rest of this function (the error that broke out of the action will
		// cause an AddBackoff)
		if opts.ActionTimeToConsiderSuccess != 0 {
			if time.Since(startTime) > opts.ActionTimeToConsiderSuccess {
				s.LongSuccessResetBackoff()
			}
		}

		// Give up after failing more than SevereErrorThreshold times
		givingUp := s.consecutiveFailures >= opts.SevereErrorThreshold

		// Check if this is a known TLS error that will be retried with different settings
		isRecoverableTLSError := errors.Is(err, tls.ErrTLSALPNHandshakeFailed) || errors.Is(err, tls.ErrNoTLSHandshakeFailed)

		if isRecoverableTLSError {
			// TLS errors are expected when connecting to Hubble Relay - we'll retry with different TLS settings
			opts.Logger.Info("TLS connection failed, will retry with adjusted settings",
				zap.String("reason", err.Error()),
				zap.Int("attempt", s.consecutiveFailures+1),
			)
		} else {
			lg := opts.Logger.Debug

			if givingUp {
				lg = opts.Logger.Error
			}

			lg("Error in backoff function",
				zap.Bool("severe_failure", givingUp),
				zap.Int("consecutive_failures", s.consecutiveFailures),
				zap.Error(err),
			)
		}

		if !givingUp {
			s.AddBackoff(1)

			continue
		}

		s.UnhappyPathResetBackoff()

		return fmt.Errorf("failed %d times", s.consecutiveFailures)
	}

	// unreachable
	return errors.New("broke out of backoff loop")
}

func (s *state) AddBackoff(count int) {
	for range count {
		s.consecutiveFailures++

		sleep := clamp(s.opts.InitialBackoff, jitterTime(s.backoff, s.opts.MaxJitterPct), s.opts.MaxBackoff)
		s.opts.Logger.Debug("Backing off", zap.Duration("sleep", sleep), zap.Int("consecutive_failures", s.consecutiveFailures))
		s.timer.Reset(sleep)
		nextBackoff := time.Duration(float64(s.backoff) * s.opts.ExponentialFactor)
		s.backoff = min(nextBackoff, s.opts.MaxBackoff)
	}
}

func (s *state) HappyPathResetBackoff() {
	s.opts.Logger.Debug("Resetting backoff timer because of success. No need to wait so long when things are well")
	s.resetBackoff()
}

func (s *state) LongSuccessResetBackoff() {
	s.opts.Logger.Debug("Resetting backoff timer because the system has been in a success state for a long time")
	s.resetBackoff()
}

func (s *state) UnhappyPathResetBackoff() {
	s.opts.Logger.Debug("Resetting backoff timer because of severe error")
	s.resetBackoff()
}

func (s *state) resetBackoff() {
	s.consecutiveFailures = 0
	s.backoff = s.opts.InitialBackoff
	s.timer.Reset(s.opts.InitialBackoff)
}
