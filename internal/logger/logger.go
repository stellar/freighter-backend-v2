package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
)

// Log levels
const (
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)

// Logger wraps slog.Logger to provide a consistent interface
type Logger struct {
	*slog.Logger
}

// Config holds logger configuration
type loggerConfig struct {
	Level      slog.Level
	JSONOutput bool
	Output     io.Writer
}

// DefaultConfig returns a default logger configuration
func DefaultConfig() loggerConfig {
	return loggerConfig{
		Level:      LevelDebug,
		JSONOutput: false,
		Output:     os.Stdout,
	}
}

// New creates a new logger with the given configuration
func New(cfg loggerConfig) *Logger {
	var handler slog.Handler

	switch cfg.JSONOutput {
	case true:
		handler = slog.NewJSONHandler(cfg.Output, &slog.HandlerOptions{
			Level: cfg.Level,
		})
	case false:
		handler = slog.NewTextHandler(cfg.Output, &slog.HandlerOptions{
			Level: cfg.Level,
		})
	}

	return &Logger{
		Logger: slog.New(handler),
	}
}

// Global logger instance
var (
	global *Logger
	once   sync.Once
)

// Global returns the global logger instance, initializing it safely if needed
func Global() *Logger {
	once.Do(func() {
		if global == nil {
			global = New(DefaultConfig())
		}
	})
	return global
}

// Debug logs at debug level
func Debug(msg string, args ...any) {
	Global().Debug(msg, args...)
}

// Info logs at info level
func Info(msg string, args ...any) {
	Global().Info(msg, args...)
}

// Warn logs at warn level
func Warn(msg string, args ...any) {
	Global().Warn(msg, args...)
}

// Error logs at error level
func Error(msg string, args ...any) {
	Global().Error(msg, args...)
}

func InfoWithContext(ctx context.Context, msg string, args ...any) {
	Global().InfoContext(ctx, msg, args...)
}

func ErrorWithContext(ctx context.Context, msg string, args ...any) {
	Global().ErrorContext(ctx, msg, args...)
}
