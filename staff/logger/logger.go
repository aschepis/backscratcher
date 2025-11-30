package logger

import (
	"fmt"
	"io"
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
	return InitWithOptions("staff.log", false)
}

// InitWithOptions initializes the logger with the specified options.
// If logFile is empty, logs to stdout/stderr.
// If pretty is true, uses ConsoleWriter for human-readable output (only valid when logFile is empty).
// Log level can be configured via LOG_LEVEL environment variable (debug, info, warn, error).
func InitWithOptions(logFile string, pretty bool) (zerolog.Logger, error) {
	// Get log level from environment variable
	level := parseLogLevel(os.Getenv("LOG_LEVEL"))

	var output io.Writer
	var logPath string

	switch {
	case logFile != "":
		// Log to file
		logPath = logFile
		//nolint:gosec // G304: User-specified log file path is intentional
		file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return zerolog.Logger{}, fmt.Errorf("failed to open log file %s: %w", logPath, err)
		}
		output = file
		// Create file logger - JSON structured logs
		log = zerolog.New(output).
			Level(level).
			With().
			Timestamp().
			Logger()
	case pretty:
		// Log to stdout with pretty console output
		output = zerolog.ConsoleWriter{Out: os.Stdout}
		log = zerolog.New(output).
			Level(level).
			With().
			Timestamp().
			Logger()
	default:
		// Log to stdout/stderr (default)
		output = os.Stdout
		log = zerolog.New(output).
			Level(level).
			With().
			Timestamp().
			Logger()
	}

	// Log initialization
	switch {
	case logFile != "":
		log.Info().Str("path", logPath).Str("level", level.String()).Msg("Logger initialized")
	case pretty:
		log.Info().Str("output", "stdout").Str("format", "pretty").Str("level", level.String()).Msg("Logger initialized")
	default:
		log.Info().Str("output", "stdout/stderr").Str("level", level.String()).Msg("Logger initialized")
	}

	return log, nil
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
