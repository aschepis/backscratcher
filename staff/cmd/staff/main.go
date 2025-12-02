package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/aschepis/backscratcher/staff/client"
	"github.com/aschepis/backscratcher/staff/config"
	stafflogger "github.com/aschepis/backscratcher/staff/logger"
	"github.com/aschepis/backscratcher/staff/ui/tui"
)

const (
	defaultSocketPath = "/tmp/staffd.sock"
)

func main() {
	// Parse command-line flags
	var (
		socketPath = flag.String("socket", defaultSocketPath, "Unix socket path for daemon connection")
		tcpAddress = flag.String("tcp", "", "TCP address to connect to (e.g., localhost:50051). If set, disables Unix socket")
		logFile    = flag.String("logfile", "", "Path to log file. If not set, logs to stdout/stderr")
		pretty     = flag.Bool("pretty", false, "Use pretty console output (only valid when logfile is not set)")
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

	logger.Info().Msg("Starting Staff CLI client")

	// Load client configuration
	configPath := config.GetClientConfigPath()
	clientConfig, err := config.LoadClientConfig(configPath)
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to load client configuration, using defaults")
		// Continue with defaults
		clientConfig = &config.ClientConfig{
			Daemon: config.DaemonConfig{
				Socket: defaultSocketPath,
				TCP:    "",
			},
			Theme:       "solarized",
			ChatTimeout: 60,
		}
	}

	// ---------------------------
	// Connect to Daemon
	// ---------------------------

	// Determine connection address (command line flags override config)
	var address string
	if *tcpAddress != "" {
		address = *tcpAddress
		logger.Info().Str("address", address).Msg("Connecting to daemon via TCP")
	} else if clientConfig.Daemon.TCP != "" {
		address = clientConfig.Daemon.TCP
		logger.Info().Str("address", address).Msg("Connecting to daemon via TCP (from config)")
	} else if *socketPath != defaultSocketPath {
		address = *socketPath
		logger.Info().Str("socket", address).Msg("Connecting to daemon via Unix socket")
	} else if clientConfig.Daemon.Socket != "" {
		address = clientConfig.Daemon.Socket
		logger.Info().Str("socket", address).Msg("Connecting to daemon via Unix socket (from config)")
	} else {
		address = defaultSocketPath
		logger.Info().Str("socket", address).Msg("Connecting to daemon via Unix socket (default)")
	}

	// Connect to daemon
	grpcClient, err := client.Connect(address)
	if err != nil {
		logger.Error().Err(err).Str("address", address).Msg("Failed to connect to daemon")
		fmt.Fprintf(os.Stderr, "Cannot connect to staffd at %s\n", address)
		fmt.Fprintf(os.Stderr, "Make sure the daemon is running: staffd\n")
		os.Exit(1)
	}
	defer grpcClient.Close() //nolint:errcheck // No remedy for grpcClient close errors

	logger.Info().Msg("Connected to daemon successfully")

	// ---------------------------
	// Create Service Adapter
	// ---------------------------

	// Get chat timeout: env var takes precedence, then config file, then default (60)
	chatTimeout := 60 * time.Second // default
	if envTimeout := os.Getenv("STAFF_CHAT_TIMEOUT"); envTimeout != "" {
		if parsed, err := strconv.Atoi(envTimeout); err == nil && parsed > 0 {
			chatTimeout = time.Duration(parsed) * time.Second
		}
	} else if clientConfig.ChatTimeout > 0 {
		chatTimeout = time.Duration(clientConfig.ChatTimeout) * time.Second
	}

	chatService := client.NewServiceAdapter(grpcClient, chatTimeout)
	logger.Info().Dur("timeout", chatTimeout).Msg("Service adapter created")

	// ---------------------------
	// Start TUI
	// ---------------------------

	logger.Info().Msg("Initializing UI")

	// Get theme: env var takes precedence, then config file, then default
	theme := os.Getenv("STAFF_THEME")
	if theme == "" {
		theme = clientConfig.Theme
	}
	if theme == "" {
		theme = "solarized"
	}

	app := tui.NewAppWithTheme(logger, configPath, chatService, theme)

	logger.Info().Msg("Starting terminal UI")
	if err := app.Run(); err != nil {
		logger.Error().Err(err).Msg("Error running application")
		fmt.Fprintf(os.Stderr, "Error running application: %v\n", err)
		os.Exit(1)
	}

	logger.Info().Msg("Application shutdown")
}
