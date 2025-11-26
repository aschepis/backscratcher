package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"
)

var (
	logFile *os.File
	log     zerolog.Logger
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
	level := parseLogLevel(os.Getenv("LOG_LEVEL"))

	// Create file logger - JSON structured logs
	log = zerolog.New(logFile).
		Level(level).
		With().
		Timestamp().
		Logger()

	// Log initialization
	log.Info().Str("path", logPath).Str("level", level.String()).Msg("Logger initialized")

	return nil
}

// Close closes the log file. Should be called at application shutdown.
func Close() error {
	if logFile != nil {
		return logFile.Close()
	}
	return nil
}

// SetSuppressConsole is a no-op kept for API compatibility.
// Console output has been removed in favor of file-only logging.
func SetSuppressConsole(_ bool) {}

// Info logs an informational message.
func Info(format string, v ...interface{}) {
	log.Info().Msg(fmt.Sprintf(format, v...))
}

// Error logs an error message.
func Error(format string, v ...interface{}) {
	log.Error().Msg(fmt.Sprintf(format, v...))
}

// Debug logs a debug message.
func Debug(format string, v ...interface{}) {
	log.Debug().Msg(fmt.Sprintf(format, v...))
}

// Warn logs a warning message.
func Warn(format string, v ...interface{}) {
	log.Warn().Msg(fmt.Sprintf(format, v...))
}

// GetLogger returns the zerolog.Logger for structured logging.
func GetLogger() *zerolog.Logger {
	return &log
}

// LogFile returns the path to the log file.
func LogFile() string {
	if logFile != nil {
		return logFile.Name()
	}
	return filepath.Join(".", "staff.log")
}

// GetLevel returns the current log level as a string.
func GetLevel() string {
	return log.GetLevel().String()
}

// SetLevel sets the log level.
func SetLevel(level string) {
	log = log.Level(parseLogLevel(level))
}

// Writer returns an io.Writer that writes to the log file.
// Useful for redirecting output from other loggers.
func Writer() io.Writer {
	if logFile != nil {
		return logFile
	}
	return io.Discard
}

// Helper functions

func parseLogLevel(level string) zerolog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return zerolog.DebugLevel
	case "info", "":
		return zerolog.InfoLevel
	case "warn", "warning":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	case "trace":
		return zerolog.TraceLevel
	default:
		return zerolog.InfoLevel
	}
}
