package controller

import (
	"fmt"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/exp/constraints"
)

type backoffOpts struct {
	InitialBackoff       time.Duration
	MaxBackoff           time.Duration
	MaxJitterPct         float64
	SevereErrorThreshold int
	ExponentialFactor    float64
	Logger               *zap.Logger
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

func clamp[T constraints.Ordered](minimum, val, maximum T) T {
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
		timer:               time.NewTimer(0),
		opts:                opts,
	}
	defer s.timer.Stop()
	opts.logger.Debug("Making first attempt", zap.Inline(opts))

	for {
		select {
		case <-s.timer.C:
			err := action()

			if err != nil {
				s.HappyPathResetBackoff()
				continue
			}

			// Give up after failing more than SevereErrorThreshold times
			givingUp := s.consecutiveFailures >= opts.SevereErrorThreshold
			lg := opts.logger.Debug
			if givingUp {
				lg = opts.logger.Error
			}
			lg("Error in backoff function",
				zap.String("name", opts.Name),
				zap.Bool("severe_failure", givingUp),
				zap.Int("consecutive_failures", s.consecutiveFailures),
				zap.Error(err),
			)

			if !givingUp {
				s.AddBackoff(1)
				continue
			}

			s.UnhappyPathResetBackoff()
			return fmt.Errorf("%s has failed %d times", opts.Name, s.consecutiveFailures)
		}
	}
}

func (s *state) AddBackoff(count int) {
	for range count {
		s.consecutiveFailures++

		sleep := clamp(s.opts.InitialBackoff, jitterTime(s.backoff, s.opts.MaxJitterPct), s.opts.MaxBackoff)
		s.opts.logger.Debug("Backing off", zap.String("name", s.opts.Name), zap.Duration("sleep", sleep), zap.Int("consecutive_failures", s.consecutiveFailures))
		s.timer.Reset(sleep)
		nextBackoff := time.Duration(float64(s.backoff) * s.opts.ExponentialFactor)
		s.backoff = min(nextBackoff, s.opts.MaxBackoff)
	}
}

func (s *state) HappyPathResetBackoff() {
	s.opts.logger.Debug("Resetting backoff timer because of success. No need to wait so long when things are well", zap.String("name", s.opts.Name))
	s.resetBackoff()
}

func (s *state) UnhappyPathResetBackoff() {
	s.opts.logger.Debug("Resetting backoff timer because of severe error", zap.String("name", s.opts.Name))
	s.resetBackoff()
}

func (s *state) resetBackoff() {
	s.consecutiveFailures = 0
	s.backoff = s.opts.InitialBackoff
	s.timer.Reset(s.opts.InitialBackoff)
}
