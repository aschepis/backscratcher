package main

import (
	"flag"
	"fmt"
	"os"

	stafflogger "github.com/aschepis/backscratcher/staff/logger"
)

func main() {
	// Parse command-line flags
	var (
		logFile = flag.String("logfile", "", "Path to log file. If not set, logs to stdout/stderr")
		pretty  = flag.Bool("pretty", false, "Use pretty console output (only valid when logfile is not set)")
	)
	flag.Parse()

	// Validate that --logfile and --pretty are mutually exclusive
	if *logFile != "" && *pretty {
		fmt.Fprintf(os.Stderr, "Error: --logfile and --pretty are mutually exclusive\n")
		os.Exit(1)
	}

	// Initialize logger with options
	logger, err := stafflogger.InitWithOptions(*logFile, *pretty)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}

	logger.Info().Msg("staffd starting")
	// TODO: Add server initialization and startup logic here
	logger.Info().Msg("staffd shutdown")
}
