package logging

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  zapcore.Level
		err   bool
	}{
		{"debug", zapcore.DebugLevel, false},
		{"DEBUG", zapcore.DebugLevel, false},
		{"info", zapcore.InfoLevel, false},
		{"Info", zapcore.InfoLevel, false},
		{"", zapcore.InfoLevel, false},
		{"warn", zapcore.WarnLevel, false},
		{"error", zapcore.ErrorLevel, false},
		{"fatal", zapcore.InfoLevel, true},
		{"garbage", zapcore.InfoLevel, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseLevel(tt.input)
			if tt.err {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestNewLogger_JSON(t *testing.T) {
	logger, err := NewLogger(Config{Level: "info", Format: "json"}, "test-node")
	require.NoError(t, err)
	assert.NotNil(t, logger)
}

func TestNewLogger_Console(t *testing.T) {
	logger, err := NewLogger(Config{Level: "debug", Format: "console"}, "my-node")
	require.NoError(t, err)
	assert.NotNil(t, logger)
}

func TestNewLogger_DefaultFormat(t *testing.T) {
	logger, err := NewLogger(Config{Level: "warn", Format: ""}, "node-1")
	require.NoError(t, err)
	assert.NotNil(t, logger)
}

func TestNewLogger_InvalidFormat(t *testing.T) {
	_, err := NewLogger(Config{Level: "info", Format: "xml"}, "node")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported log format")
}

func TestNewLogger_InvalidLevel(t *testing.T) {
	_, err := NewLogger(Config{Level: "garbage"}, "node")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported log level")
}
