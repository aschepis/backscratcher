package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	logFile         *os.File
	suppressConsole bool // When true, don't write to stdout/stderr (for TUI mode)
	logLevel        slog.Level
	mu              sync.RWMutex

	// Handlers for different outputs
	fileHandler    slog.Handler
	consoleHandler slog.Handler
)

// Init initializes the file logger, writing to staff.log in the current directory.
// It should be called once at application startup.
// Log level can be configured via LOG_LEVEL environment variable (debug, info, warn, error).
func Init() error {
	logPath := "staff.log"

	var err error
	logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open log file %s: %w", logPath, err)
	}

	// Get log level from environment variable
	logLevel = parseLogLevel(os.Getenv("LOG_LEVEL"))

	// Create file handler - always logs at debug level to capture everything
	fileHandler = slog.NewTextHandler(logFile, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Customize time format and level display
			if a.Key == slog.LevelKey {
				level := a.Value.Any().(slog.Level)
				a.Value = slog.StringValue(levelToString(level))
			}
			return a
		},
	})

	// Create console handler - respects configured log level
	consoleHandler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				level := a.Value.Any().(slog.Level)
				a.Value = slog.StringValue(levelToString(level))
			}
			return a
		},
	})

	// Log initialization
	Info("Logger initialized, writing to %s (level: %s)", logPath, logLevel.String())

	return nil
}

// Close closes the log file. Should be called at application shutdown.
func Close() error {
	if logFile != nil {
		return logFile.Close()
	}
	return nil
}

// SetSuppressConsole sets whether console output (stdout/stderr) should be suppressed.
// Set to true when using a TUI to avoid interfering with terminal rendering.
func SetSuppressConsole(suppress bool) {
	mu.Lock()
	defer mu.Unlock()
	suppressConsole = suppress
}

// Info logs an informational message with timestamp.
func Info(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)

	// Log to file
	if fileHandler != nil {
		fileHandler.Handle(context.Background(), slog.NewRecord(
			timeNow(), slog.LevelInfo, msg, 0,
		))
	}

	// Log to console (unless suppressed)
	mu.RLock()
	suppress := suppressConsole
	mu.RUnlock()

	if !suppress && consoleHandler != nil && slog.LevelInfo >= logLevel {
		consoleHandler.Handle(context.Background(), slog.NewRecord(
			timeNow(), slog.LevelInfo, msg, 0,
		))
	}
}

// Error logs an error message with timestamp.
func Error(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)

	// Log to file
	if fileHandler != nil {
		fileHandler.Handle(context.Background(), slog.NewRecord(
			timeNow(), slog.LevelError, msg, 0,
		))
	}

	// Log to console (unless suppressed)
	mu.RLock()
	suppress := suppressConsole
	mu.RUnlock()

	if !suppress && consoleHandler != nil && slog.LevelError >= logLevel {
		consoleHandler.Handle(context.Background(), slog.NewRecord(
			timeNow(), slog.LevelError, msg, 0,
		))
	}
}

// Debug logs a debug message with timestamp.
// Debug messages only go to file, not console.
func Debug(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)

	// Log to file only
	if fileHandler != nil {
		fileHandler.Handle(context.Background(), slog.NewRecord(
			timeNow(), slog.LevelDebug, msg, 0,
		))
	}
}

// Warn logs a warning message with timestamp.
func Warn(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)

	// Log to file
	if fileHandler != nil {
		fileHandler.Handle(context.Background(), slog.NewRecord(
			timeNow(), slog.LevelWarn, msg, 0,
		))
	}

	// Log to console (unless suppressed)
	mu.RLock()
	suppress := suppressConsole
	mu.RUnlock()

	if !suppress && consoleHandler != nil && slog.LevelWarn >= logLevel {
		consoleHandler.Handle(context.Background(), slog.NewRecord(
			timeNow(), slog.LevelWarn, msg, 0,
		))
	}
}

// GetLogger returns a standard library logger that writes to the log file.
// This is provided for backward compatibility with code that needs a *log.Logger.
func GetLogger() *slog.Logger {
	if fileHandler != nil {
		return slog.New(fileHandler)
	}
	return slog.Default()
}

// LogFile returns the path to the log file.
func LogFile() string {
	if logFile != nil {
		return logFile.Name()
	}
	return filepath.Join(".", "staff.log")
}

// GetLevel returns the current log level.
func GetLevel() slog.Level {
	return logLevel
}

// SetLevel sets the log level for console output.
func SetLevel(level slog.Level) {
	mu.Lock()
	defer mu.Unlock()
	logLevel = level

	// Recreate console handler with new level
	consoleHandler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				lvl := a.Value.Any().(slog.Level)
				a.Value = slog.StringValue(levelToString(lvl))
			}
			return a
		},
	})
}

// WithAttrs returns a new slog.Logger with the given attributes for structured logging.
func WithAttrs(attrs ...slog.Attr) *slog.Logger {
	if fileHandler == nil {
		return slog.Default()
	}

	// Create a multi-handler that writes to both file and console
	return slog.New(&multiHandler{
		file:    fileHandler,
		console: consoleHandler,
	}).With(attrsToAny(attrs)...)
}

// Helper functions

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "info", "":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func levelToString(level slog.Level) string {
	switch level {
	case slog.LevelDebug:
		return "DEBUG"
	case slog.LevelInfo:
		return "INFO"
	case slog.LevelWarn:
		return "WARN"
	case slog.LevelError:
		return "ERROR"
	default:
		return level.String()
	}
}

func timeNow() interface{ UnixNano() int64 } {
	return timeProvider{}
}

type timeProvider struct{}

func (timeProvider) UnixNano() int64 {
	return 0 // slog will use current time
}

func attrsToAny(attrs []slog.Attr) []any {
	result := make([]any, len(attrs))
	for i, attr := range attrs {
		result[i] = attr
	}
	return result
}

// multiHandler implements slog.Handler and writes to multiple handlers
type multiHandler struct {
	file    slog.Handler
	console slog.Handler
}

func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.file.Enabled(ctx, level) || h.console.Enabled(ctx, level)
}

func (h *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	// Always write to file
	if h.file != nil {
		if err := h.file.Handle(ctx, r); err != nil {
			return err
		}
	}

	// Write to console unless suppressed or it's a debug message
	mu.RLock()
	suppress := suppressConsole
	mu.RUnlock()

	if !suppress && h.console != nil && r.Level >= slog.LevelInfo {
		if err := h.console.Handle(ctx, r); err != nil {
			return err
		}
	}

	return nil
}

func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &multiHandler{
		file:    h.file.WithAttrs(attrs),
		console: h.console.WithAttrs(attrs),
	}
}

func (h *multiHandler) WithGroup(name string) slog.Handler {
	return &multiHandler{
		file:    h.file.WithGroup(name),
		console: h.console.WithGroup(name),
	}
}

// Writer returns an io.Writer that writes to the log file.
// Useful for redirecting output from other loggers.
func Writer() io.Writer {
	if logFile != nil {
		return logFile
	}
	return io.Discard
}
