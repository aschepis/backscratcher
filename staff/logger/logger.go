package logger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

var (
	fileLogger      *log.Logger
	logFile         *os.File
	suppressConsole bool // When true, don't write to stdout/stderr (for TUI mode)
)

// Init initializes the file logger, writing to staff.log in the current directory.
// It should be called once at application startup.
func Init() error {
	logPath := "staff.log"

	var err error
	logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open log file %s: %w", logPath, err)
	}

	fileLogger = log.New(logFile, "", log.LstdFlags)

	// Log initialization
	Info("Logger initialized, writing to %s", logPath)

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
	suppressConsole = suppress
}

// Info logs an informational message with timestamp.
func Info(format string, v ...interface{}) {
	if fileLogger != nil {
		fileLogger.Printf("[INFO] "+format, v...)
	}
	// Also log to stdout for visibility (unless suppressed for TUI)
	if !suppressConsole {
		log.Printf("[INFO] "+format, v...)
	}
}

// Error logs an error message with timestamp.
func Error(format string, v ...interface{}) {
	if fileLogger != nil {
		fileLogger.Printf("[ERROR] "+format, v...)
	}
	// Also log to stderr for visibility (unless suppressed for TUI)
	if !suppressConsole {
		log.Printf("[ERROR] "+format, v...)
	}
}

// Debug logs a debug message with timestamp.
func Debug(format string, v ...interface{}) {
	if fileLogger != nil {
		fileLogger.Printf("[DEBUG] "+format, v...)
	}
}

// Warn logs a warning message with timestamp.
func Warn(format string, v ...interface{}) {
	if fileLogger != nil {
		fileLogger.Printf("[WARN] "+format, v...)
	}
	// Also log to stdout for visibility (unless suppressed for TUI)
	if !suppressConsole {
		log.Printf("[WARN] "+format, v...)
	}
}

// GetLogger returns the underlying log.Logger for advanced usage.
func GetLogger() *log.Logger {
	return fileLogger
}

// LogFile returns the path to the log file.
func LogFile() string {
	if logFile != nil {
		return logFile.Name()
	}
	return filepath.Join(".", "staff.log")
}
