package logger

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	require.Equal(t, LevelDebug, cfg.Level)
	// Output should be non-nil (os.Stdout)
	require.NotNil(t, cfg.Output)
}

func TestNewLogger(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := loggerConfig{Level: LevelInfo, JSONOutput: false, Output: buf}
	l := New(cfg)
	require.NotNil(t, l)
	l.Info("test message", "key", "value")
	assert.Contains(t, buf.String(), "test message")
}

func TestGlobalLogger(t *testing.T) {
	l := Global()
	require.NotNil(t, l)
}

func TestLogLevelFunctions(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := loggerConfig{Level: LevelDebug, JSONOutput: false, Output: buf}
	logger := New(cfg)
	global = logger // override global for test

	Debug("debug message")
	Info("info message")
	Warn("warn message")
	Error("error message")
	InfoWithContext(context.Background(), "info ctx")
	ErrorWithContext(context.Background(), "error ctx")

	out := buf.String()
	for _, msg := range []string{"debug message", "info message", "warn message", "error message", "info ctx", "error ctx"} {
		assert.Contains(t, out, msg)
	}
}
