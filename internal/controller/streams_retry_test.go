package controller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestClamp(t *testing.T) {
	tests := []struct {
		name     string
		minimum  int
		value    int
		maximum  int
		expected int
	}{
		{"value within range", 5, 10, 20, 10},
		{"value below minimum", 5, 2, 20, 5},
		{"value above maximum", 5, 25, 20, 20},
		{"minimum equals maximum", 10, 15, 10, 10},
		{"value equals minimum", 5, 5, 20, 5},
		{"value equals maximum", 5, 20, 20, 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := clamp(tt.minimum, tt.value, tt.maximum)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMarshalLogObject(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	opts := backoffOpts{
		InitialBackoff:       1 * time.Second,
		MaxBackoff:           10 * time.Second,
		MaxJitterPct:         0.1,
		SevereErrorThreshold: 5,
		ExponentialFactor:    2.0,
		Logger:               logger,
	}

	enc := zapcore.NewMapObjectEncoder()
	err = opts.MarshalLogObject(enc)
	require.NoError(t, err)

	// Verify all fields are correctly marshaled
	assert.Equal(t, opts.InitialBackoff, enc.Fields["initial_backoff"])
	assert.Equal(t, opts.MaxBackoff, enc.Fields["max_backoff"])
	assert.InEpsilon(t, opts.ExponentialFactor, enc.Fields["exponential_factor"], 0, 01)
	assert.InEpsilon(t, opts.MaxJitterPct, enc.Fields["max_jitter_pct"], 0.01)
	assert.Equal(t, opts.SevereErrorThreshold, enc.Fields["severe_error_threshold"])
	// Logger field is not marshaled as it's not a basic type
}
