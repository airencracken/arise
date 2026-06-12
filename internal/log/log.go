package log

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

var (
	level  = new(slog.LevelVar)
	logger *slog.Logger
)

func init() {
	level.Set(slog.LevelInfo)
	logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	}))
}

// SetLevel sets the minimum log level.
func SetLevel(l slog.Level) {
	level.Set(l)
}

// SetLevelString parses a log level string.
func SetLevelString(s string) {
	switch s {
	case "debug":
		level.Set(slog.LevelDebug)
	case "info":
		level.Set(slog.LevelInfo)
	case "warn":
		level.Set(slog.LevelWarn)
	case "error":
		level.Set(slog.LevelError)
	}
}

// SetOutput sets the log output destination.
func SetOutput(w io.Writer) {
	logger = slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
	}))
}

// Debug logs at debug level.
func Debug(msg string, args ...any) {
	logger.Debug(msg, args...)
}

// Info logs at info level.
func Info(msg string, args ...any) {
	logger.Info(msg, args...)
}

// Warn logs at warn level.
func Warn(msg string, args ...any) {
	logger.Warn(msg, args...)
}

// Error logs at error level and returns the message as an error.
func Error(msg string, args ...any) error {
	logger.Error(msg, args...)
	return fmt.Errorf("%s", msg)
}

// Errorf logs at error level with optional key-value args and returns a formatted error.
func Errorf(format string, args ...any) error {
	err := fmt.Errorf(format, args...)
	logger.Error(err.Error())
	return err
}
