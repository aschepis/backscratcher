package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aschepis/backscratcher/staff/agent"
	"github.com/aschepis/backscratcher/staff/logger"
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
func (c *ClaudeMCPServer) GetEnvAsStrings() []string {
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
		envStrings := make([]string, 0, len(envMap))
		for key, value := range envMap {
			envStrings = append(envStrings, fmt.Sprintf("%s=%s", key, value))
		}
		return envStrings
	}

	// If both fail, return empty
	logger.Warn("Failed to parse env field, expected array of strings or object: %v", string(c.Env))
	return nil
}

// LoadClaudeConfig loads Claude's configuration from the specified path.
// Returns a config with empty projects if the file doesn't exist (non-fatal).
// Returns an error only if the file exists but cannot be parsed.
func LoadClaudeConfig(path string) (*ClaudeConfig, error) {
	expandedPath := expandPath(path)
	logger.Info("LoadClaudeConfig: loading Claude config from %q (expanded: %q)", path, expandedPath)

	// Check if file exists
	if _, err := os.Stat(expandedPath); os.IsNotExist(err) {
		// File doesn't exist - return empty config (non-fatal)
		logger.Info("LoadClaudeConfig: config file does not exist at %q, returning empty config", expandedPath)
		return &ClaudeConfig{
			Projects: make(map[string]ClaudeProject),
		}, nil
	}

	// Read file
	data, err := os.ReadFile(expandedPath) //#nosec 304 -- intentional file read for config
	if err != nil {
		logger.Error("LoadClaudeConfig: failed to read config file %q: %v", expandedPath, err)
		return nil, fmt.Errorf("failed to read Claude config file %q: %w", expandedPath, err)
	}

	logger.Info("LoadClaudeConfig: read %d bytes from config file", len(data))

	// Parse JSON
	var cfg ClaudeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		logger.Error("LoadClaudeConfig: failed to parse JSON from %q: %v", expandedPath, err)
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

	logger.Info("LoadClaudeConfig: successfully loaded config with %d global MCP server(s) and %d project(s)", len(cfg.MCPServers), len(cfg.Projects))
	return &cfg, nil
}

// MapClaudeToMCPServerConfig converts Claude MCP server configurations to our MCPServerConfig format.
// Takes a map of Claude MCP servers and returns a map of MCPServerConfig with "claude_" prefix.
func MapClaudeToMCPServerConfig(claudeServers map[string]ClaudeMCPServer) map[string]*agent.MCPServerConfig {
	logger.Info("MapClaudeToMCPServerConfig: converting %d Claude MCP server(s) to MCPServerConfig format", len(claudeServers))
	result := make(map[string]*agent.MCPServerConfig)

	for serverName, claudeServer := range claudeServers {
		// Prefix with "claude_" to avoid conflicts with agents.yaml servers
		safeName := "claude_" + serverName

		// Expand ~ in configFile path if present
		configFile := claudeServer.ConfigFile
		if configFile != "" {
			configFile = expandPath(configFile)
		}

		envStrings := claudeServer.GetEnvAsStrings()
		logger.Info("MapClaudeToMCPServerConfig: mapping server %q -> %q (command=%s, args=%v, env=%d vars, configFile=%s)",
			serverName, safeName, claudeServer.Command, claudeServer.Args, len(envStrings), configFile)

		result[safeName] = &agent.MCPServerConfig{
			Name:       serverName,
			Command:    claudeServer.Command,
			Args:       claudeServer.Args,
			Env:        envStrings,
			ConfigFile: configFile,
			// Homepage and URL are left empty as Claude config doesn't provide these
		}
	}

	logger.Info("MapClaudeToMCPServerConfig: successfully mapped %d server(s)", len(result))
	return result
}

// ExtractMCPServersFromProjects extracts MCP servers from Claude config projects and global servers.
// projectPaths can contain "Global" to include global servers, or project paths.
// If projectPaths is empty, extracts from all projects and global servers.
// Returns a map of server name to ClaudeMCPServer and a map of project path to server names.
func ExtractMCPServersFromProjects(claudeConfig *ClaudeConfig, projectPaths []string) (map[string]ClaudeMCPServer, map[string][]string) {
	logger.Info("ExtractMCPServersFromProjects: extracting from %d global server(s) and %d project(s) in Claude config, filter: %v", len(claudeConfig.MCPServers), len(claudeConfig.Projects), projectPaths)
	servers := make(map[string]ClaudeMCPServer)
	projectToServers := make(map[string][]string)

	// Check if "Global" is in the project paths
	includeGlobal := false
	filteredProjectPaths := make([]string, 0, len(projectPaths))
	for _, path := range projectPaths {
		if path == "Global" {
			includeGlobal = true
		} else {
			filteredProjectPaths = append(filteredProjectPaths, path)
		}
	}

	// Normalize project paths for comparison
	normalizedPaths := make(map[string]string)
	for _, path := range filteredProjectPaths {
		normalized := filepath.Clean(expandPath(path))
		normalizedPaths[normalized] = path
		logger.Info("ExtractMCPServersFromProjects: normalized project path %q -> %q", path, normalized)
	}

	// If no project paths specified, load from all projects and global
	loadAll := len(filteredProjectPaths) == 0
	if loadAll {
		logger.Info("ExtractMCPServersFromProjects: no project filter specified, loading from all projects and global servers")
		includeGlobal = true
	} else {
		logger.Info("ExtractMCPServersFromProjects: filtering to %d specified project(s), includeGlobal=%v", len(filteredProjectPaths), includeGlobal)
	}

	// Extract global MCP servers if requested
	if includeGlobal && claudeConfig.MCPServers != nil && len(claudeConfig.MCPServers) > 0 {
		serverNames := make([]string, 0, len(claudeConfig.MCPServers))
		for serverName, server := range claudeConfig.MCPServers {
			servers[serverName] = server
			serverNames = append(serverNames, serverName)
			logger.Info("ExtractMCPServersFromProjects: extracted global server %q (command=%s)", serverName, server.Command)
		}
		projectToServers["Global"] = serverNames
		logger.Info("ExtractMCPServersFromProjects: Global contributed %d MCP server(s)", len(serverNames))
	} else if includeGlobal {
		logger.Info("ExtractMCPServersFromProjects: Global has no MCP servers")
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
			serverNames := make([]string, 0, len(project.MCPServers))
			for serverName, server := range project.MCPServers {
				servers[serverName] = server
				serverNames = append(serverNames, serverName)
				logger.Info("ExtractMCPServersFromProjects: extracted server %q from project %q (command=%s)", serverName, projectPath, server.Command)
			}
			projectToServers[projectPath] = serverNames
			logger.Info("ExtractMCPServersFromProjects: project %q contributed %d MCP server(s)", projectPath, len(serverNames))
			continue
		}

		if shouldLoad {
			logger.Info("ExtractMCPServersFromProjects: project %q has no MCP servers", projectPath)
			continue
		}

		logger.Info("ExtractMCPServersFromProjects: skipping project %q (not in filter list)", projectPath)
	}

	// Count projects vs global
	projectCount := 0
	hasGlobal := false
	for path := range projectToServers {
		if path == "Global" {
			hasGlobal = true
		} else {
			projectCount++
		}
	}
	if hasGlobal {
		logger.Info("ExtractMCPServersFromProjects: extracted %d total MCP server(s) from Global and %d project(s)", len(servers), projectCount)
	} else {
		logger.Info("ExtractMCPServersFromProjects: extracted %d total MCP server(s) from %d project(s)", len(servers), projectCount)
	}
	return servers, projectToServers
}
