package logger

import (
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog"
)

var (
	log zerolog.Logger
)

// Init initializes the file logger, writing to staff.log in the current directory.
// It should be called once at application startup.
// Log level can be configured via LOG_LEVEL environment variable (debug, info, warn, error).
func Init() (zerolog.Logger, error) {
	logPath := "staff.log"

	var err error
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return zerolog.Logger{}, fmt.Errorf("failed to open log file %s: %w", logPath, err)
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

	return log, nil
}

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
func GetLogger() zerolog.Logger {
	return log
}

// GetLevel returns the current log level as a string.
func GetLevel() string {
	return log.GetLevel().String()
}

// SetLevel sets the log level.
func SetLevel(level string) {
	log = log.Level(parseLogLevel(level))
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
