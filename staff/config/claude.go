package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"
	"github.com/samber/lo"
)

// ClaudeConfig represents the structure of Claude's configuration file.
type ClaudeConfig struct {
	MCPServers map[string]ClaudeMCPServer `json:"mcpServers,omitempty"` // Global MCP servers at root level
	Projects   map[string]ClaudeProject   `json:"projects"`
}

// ClaudeProject represents a project configuration in Claude's config.
type ClaudeProject struct {
	MCPServers map[string]ClaudeMCPServer `json:"mcpServers"`
}

// ClaudeMCPServer represents an MCP server configuration in Claude's format.
type ClaudeMCPServer struct {
	Command    string          `json:"command"`
	Args       []string        `json:"args,omitempty"`
	Env        json.RawMessage `json:"env,omitempty"` // Can be array of strings or object
	ConfigFile string          `json:"configFile,omitempty"`
}

// GetEnvAsStrings converts the Env field to a slice of strings.
// Env can be either an array of strings or an object (map[string]string).
// If it's an object, converts it to "KEY=VALUE" format strings.
// If it's an array, returns it as-is.
func (c *ClaudeMCPServer) GetEnvAsStrings(logger zerolog.Logger) []string {
	if len(c.Env) == 0 {
		return nil
	}

	// Try to unmarshal as array of strings first
	var envArray []string
	if err := json.Unmarshal(c.Env, &envArray); err == nil {
		return envArray
	}

	// If that fails, try as object/map
	var envMap map[string]string
	if err := json.Unmarshal(c.Env, &envMap); err == nil {
		envStrings := lo.MapToSlice(envMap, func(key string, value string) string {
			return fmt.Sprintf("%s=%s", key, value)
		})
		return envStrings
	}

	// If both fail, return empty
	logger.Warn().
		Str("env", string(c.Env)).
		Msg("Failed to parse env field, expected array of strings or object")
	return nil
}

// LoadClaudeConfig loads Claude's configuration from the specified path.
// Returns a config with empty projects if the file doesn't exist (non-fatal).
// Returns an error only if the file exists but cannot be parsed.
func LoadClaudeConfig(logger zerolog.Logger, path string) (*ClaudeConfig, error) {
	expandedPath := expandPath(path)
	logger.Info().Str("path", path).Str("expanded_path", expandedPath).Msg("Loading Claude config")

	// Check if file exists
	if _, err := os.Stat(expandedPath); os.IsNotExist(err) {
		// File doesn't exist - return empty config (non-fatal)
		logger.Info().Str("path", expandedPath).Msg("Config file does not exist, returning empty config")
		return &ClaudeConfig{
			Projects: make(map[string]ClaudeProject),
		}, nil
	}

	// Read file
	data, err := os.ReadFile(expandedPath) //#nosec 304 -- intentional file read for config
	if err != nil {
		logger.Error().Str("path", expandedPath).Err(err).Msg("Failed to read config file")
		return nil, fmt.Errorf("failed to read Claude config file %q: %w", expandedPath, err)
	}

	logger.Info().Int("bytes", len(data)).Msg("Read config file")

	// Parse JSON
	var cfg ClaudeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		logger.Error().Str("path", expandedPath).Err(err).Msg("Failed to parse JSON")
		return nil, fmt.Errorf("failed to parse Claude config file %q: %w", expandedPath, err)
	}

	// Initialize Projects map if nil
	if cfg.Projects == nil {
		cfg.Projects = make(map[string]ClaudeProject)
	}

	// Initialize MCPServers map if nil
	if cfg.MCPServers == nil {
		cfg.MCPServers = make(map[string]ClaudeMCPServer)
	}

	logger.Info().Int("global_servers", len(cfg.MCPServers)).Int("projects", len(cfg.Projects)).Msg("Successfully loaded config")
	return &cfg, nil
}

// MapClaudeToMCPServerConfig converts Claude MCP server configurations to our MCPServerConfig format.
// Takes a map of Claude MCP servers and returns a map of MCPServerConfig with "claude_" prefix.
func MapClaudeToMCPServerConfig(logger zerolog.Logger, claudeServers map[string]ClaudeMCPServer) map[string]*MCPServerConfig {
	logger.Info().Int("servers", len(claudeServers)).Msg("Converting Claude MCP servers to MCPServerConfig format")
	result := make(map[string]*MCPServerConfig)

	for serverName, claudeServer := range claudeServers {
		// Prefix with "claude_" to avoid conflicts with agents.yaml servers
		safeName := "claude_" + serverName

		// Expand ~ in configFile path if present
		configFile := claudeServer.ConfigFile
		if configFile != "" {
			configFile = expandPath(configFile)
		}

		envStrings := claudeServer.GetEnvAsStrings(logger)
		logger.Info().
			Str("server", serverName).
			Str("safe_name", safeName).
			Str("command", claudeServer.Command).
			Strs("args", claudeServer.Args).
			Int("env_var_count", len(envStrings)).
			Str("config_file", configFile).
			Msg("MapClaudeToMCPServerConfig: mapping server")

		result[safeName] = &MCPServerConfig{
			Name:       serverName,
			Command:    claudeServer.Command,
			Args:       claudeServer.Args,
			Env:        envStrings,
			ConfigFile: configFile,
			// Homepage and URL are left empty as Claude config doesn't provide these
		}
	}

	logger.Info().Int("servers", len(result)).Msg("Successfully mapped servers")
	return result
}

// ExtractMCPServersFromProjects extracts MCP servers from Claude config projects and global servers.
// projectPaths can contain "Global" to include global servers, or project paths.
// If projectPaths is empty, extracts from all projects and global servers.
// Returns a map of server name to ClaudeMCPServer and a map of project path to server names.
func ExtractMCPServersFromProjects(logger zerolog.Logger, claudeConfig *ClaudeConfig, projectPaths []string) (map[string]ClaudeMCPServer, map[string][]string) {
	logger.Info().Int("global_servers", len(claudeConfig.MCPServers)).Int("projects", len(claudeConfig.Projects)).Strs("project_paths", projectPaths).Msg("Extracting MCP servers from projects")
	servers := make(map[string]ClaudeMCPServer)
	projectToServers := make(map[string][]string)

	// Check if "Global" is in the project paths
	includeGlobal := false
	filteredProjectPaths := lo.FilterMap(projectPaths, func(path string, _ int) (string, bool) {
		if path == "Global" {
			includeGlobal = true
			return "", false
		}
		return path, true
	})

	// Normalize project paths for comparison
	normalizedPaths := make(map[string]string)
	for _, path := range filteredProjectPaths {
		normalized := filepath.Clean(expandPath(path))
		normalizedPaths[normalized] = path
		logger.Debug().Str("path", path).Str("normalized", normalized).Msg("Normalized project path")
	}

	// If no project paths specified, load from all projects and global
	loadAll := len(filteredProjectPaths) == 0
	if loadAll {
		logger.Debug().Msg("No project filter specified, loading from all projects and global servers")
		includeGlobal = true
	} else {
		logger.Debug().Int("project_count", len(filteredProjectPaths)).Bool("include_global", includeGlobal).Msg("Filtering to specified project(s)")
	}

	// Extract global MCP servers if requested
	if includeGlobal && claudeConfig.MCPServers != nil && len(claudeConfig.MCPServers) > 0 {
		lo.ForEach(lo.Keys(claudeConfig.MCPServers), func(serverName string, _ int) {
			server := claudeConfig.MCPServers[serverName]
			servers[serverName] = server
			logger.Debug().Str("server", serverName).Str("command", server.Command).Msg("Extracted global server")
		})
		serverNames := lo.Keys(claudeConfig.MCPServers)
		projectToServers["Global"] = serverNames
		logger.Debug().Int("server_count", len(serverNames)).Msg("Global contributed MCP servers")
	} else if includeGlobal {
		logger.Debug().Msg("Global has no MCP servers")
	}

	// Extract from projects
	for projectPath, project := range claudeConfig.Projects {
		// Normalize project path for comparison
		normalizedProjectPath := filepath.Clean(expandPath(projectPath))

		// Check if we should load from this project
		shouldLoad := loadAll
		if !loadAll {
			// Check if this project path matches any of the requested paths
			for normalized := range normalizedPaths {
				if normalizedProjectPath == normalized {
					shouldLoad = true
					break
				}
				// Check if project path is within the requested path
				rel, err := filepath.Rel(normalized, normalizedProjectPath)
				if err == nil && rel != ".." && !strings.HasPrefix(rel, "..") {
					shouldLoad = true
					break
				}
			}
		}

		if shouldLoad && project.MCPServers != nil {
			lo.ForEach(lo.Keys(project.MCPServers), func(serverName string, _ int) {
				server := project.MCPServers[serverName]
				servers[serverName] = server
				logger.Debug().Str("server", serverName).Str("project", projectPath).Str("command", server.Command).Msg("Extracted server from project")
			})
			serverNames := lo.Keys(project.MCPServers)
			projectToServers[projectPath] = serverNames
			logger.Debug().Str("project", projectPath).Int("server_count", len(serverNames)).Msg("Project contributed MCP servers")
			continue
		}

		if shouldLoad {
			logger.Debug().Str("project", projectPath).Msg("Project has no MCP servers")
			continue
		}

		logger.Debug().Str("project", projectPath).Msg("Skipping project (not in filter list)")
	}

	logger.Info().Int("server_count", len(servers)).Msg("Extracted MCP servers from Claude config")
	return servers, projectToServers
}
