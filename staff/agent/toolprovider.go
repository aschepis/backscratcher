package agent

import (
	"path/filepath"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	"github.com/aschepis/backscratcher/staff/logger"
	"github.com/aschepis/backscratcher/staff/tools"
)

type ToolSchema struct {
	Description string
	Schema      map[string]any
	ServerName  string // MCP server name if this tool comes from an MCP server, empty for native tools
}

type ToolProvider interface {
	SpecsFor(agent *AgentConfig) []anthropic.ToolUnionParam
}

type ToolProviderFromRegistry struct {
	registry *tools.Registry
	schemas  map[string]ToolSchema
}

func NewToolProvider(reg *tools.Registry) *ToolProviderFromRegistry {
	return &ToolProviderFromRegistry{
		registry: reg,
		schemas:  make(map[string]ToolSchema),
	}
}

func (p *ToolProviderFromRegistry) RegisterSchema(name string, ts ToolSchema) {
	p.schemas[name] = ts
}

// RegisterSchemaWithServer registers a tool schema with an optional MCP server name
func (p *ToolProviderFromRegistry) RegisterSchemaWithServer(name string, ts ToolSchema, serverName string) {
	ts.ServerName = serverName
	p.schemas[name] = ts
}

func (p *ToolProviderFromRegistry) SpecsFor(agent *AgentConfig) []anthropic.ToolUnionParam {
	if agent == nil {
		return nil
	}

	// Expand wildcard patterns and collect all matched tool names
	seen := make(map[string]bool)
	var expandedTools []string

	for _, pattern := range agent.Tools {
		if pattern == "" {
			logger.Warn("Empty tool pattern found in agent config, skipping")
			continue
		}

		// Check if pattern contains wildcards or is an MCP server pattern
		hasWildcard := strings.Contains(pattern, "*") || strings.Contains(pattern, "?") || strings.Contains(pattern, "[")
		hasServerPrefix := strings.Contains(pattern, ":")

		if hasWildcard || hasServerPrefix {
			// Expand the pattern
			matched := p.expandToolPattern(pattern)
			if len(matched) == 0 {
				// Only warn if this is not a server prefix pattern (those warn inside expandToolPattern)
				if !hasServerPrefix {
					logger.Warn("Tool pattern %q matched no tools", pattern)
				}
			} else {
				logger.Debug("Tool pattern %q matched %d tools: %v", pattern, len(matched), matched)
			}
			for _, toolName := range matched {
				if !seen[toolName] {
					seen[toolName] = true
					expandedTools = append(expandedTools, toolName)
				}
			}
			continue
		}

		// Exact tool name (no wildcards) - use as-is
		if !seen[pattern] {
			seen[pattern] = true
			expandedTools = append(expandedTools, pattern)
		}
	}

	// Build tool specs for all matched tools
	var out []anthropic.ToolUnionParam
	var missingTools []string

	for _, name := range expandedTools {
		schema, ok := p.schemas[name]
		if !ok {
			missingTools = append(missingTools, name)
			continue
		}

		// Extract JSON-schema-style fields
		props, _ := schema.Schema["properties"].(map[string]any)

		var required []string
		if req, ok := schema.Schema["required"].([]string); ok {
			required = req
		}

		// Extra fields (e.g. descriptions) go into ExtraFields
		extra := map[string]any{}
		for k, v := range schema.Schema {
			if k != "properties" && k != "required" {
				extra[k] = v
			}
		}

		tp := anthropic.ToolParam{
			Name:        name,
			Description: anthropic.String(schema.Description),
			InputSchema: anthropic.ToolInputSchemaParam{
				Type:        "object",
				Properties:  props,
				Required:    required,
				ExtraFields: extra,
			},
		}

		out = append(out,
			anthropic.ToolUnionParam{OfTool: &tp},
		)
	}

	// Log warnings for missing tools but don't fail (allow partial matches)
	if len(missingTools) > 0 {
		logger.Warn("Some tools were not found in registry: %v", missingTools)
	}

	return out
}

// GetAllSchemas returns all registered tool schemas
func (p *ToolProviderFromRegistry) GetAllSchemas() map[string]ToolSchema {
	result := make(map[string]ToolSchema)
	for name, schema := range p.schemas {
		result[name] = schema
	}
	return result
}

// getAllToolNames returns all registered tool names
func (p *ToolProviderFromRegistry) getAllToolNames() []string {
	names := make([]string, 0, len(p.schemas))
	for name := range p.schemas {
		names = append(names, name)
	}
	return names
}

// expandToolPattern expands a tool pattern (with optional MCP server prefix) into matching tool names
func (p *ToolProviderFromRegistry) expandToolPattern(pattern string) []string {
	if pattern == "" {
		return nil
	}

	// Special case: `*` matches all tools
	if pattern == "*" {
		return p.getAllToolNames()
	}

	var matched []string
	allTools := p.getAllToolNames()

	// Check if pattern has MCP server prefix (format: "server:pattern")
	if strings.Contains(pattern, ":") {
		parts := strings.SplitN(pattern, ":", 2)
		if len(parts) == 2 {
			serverName := parts[0]
			toolPattern := parts[1]

			// Special case: server:* matches all tools from that server
			if toolPattern == "*" {
				// Debug: log available servers and tools for diagnosis
				serverTools := make(map[string][]string)
				for _, toolName := range allTools {
					if schema, ok := p.schemas[toolName]; ok {
						if schema.ServerName != "" {
							serverTools[schema.ServerName] = append(serverTools[schema.ServerName], toolName)
						}
					}
				}
				logger.Debug("Matching pattern %q: looking for server %q. Available servers: %v", pattern, serverName, getMapKeys(serverTools))

				for _, toolName := range allTools {
					if schema, ok := p.schemas[toolName]; ok && schema.ServerName == serverName {
						matched = append(matched, toolName)
					}
				}
				// Warn if server prefix was used but no tools matched (server might not exist)
				if len(matched) == 0 {
					logger.Warn("Tool pattern %q with server prefix %q matched no tools (server may not exist or have no tools). Available servers: %v", pattern, serverName, getMapKeys(serverTools))
				}
				return matched
			}

			// Match tools from the specific server
			for _, toolName := range allTools {
				schema, ok := p.schemas[toolName]
				if !ok {
					continue
				}
				// Only match tools from the specified server
				if schema.ServerName != serverName {
					continue
				}
				// Use filepath.Match to match the tool name against the pattern
				matchedPattern, err := filepath.Match(toolPattern, toolName)
				if err != nil {
					logger.Warn("Invalid wildcard pattern %q: %v", toolPattern, err)
					continue
				}
				if matchedPattern {
					matched = append(matched, toolName)
				}
			}
			// Warn if server prefix was used but no tools matched (server might not exist or have no matching tools)
			if len(matched) == 0 {
				logger.Warn("Tool pattern %q with server prefix %q matched no tools (server may not exist or have no matching tools)", pattern, serverName)
			}
			return matched
		}
	}

	// Plain pattern (no server prefix) - match across all tools
	for _, toolName := range allTools {
		matchedPattern, err := filepath.Match(pattern, toolName)
		if err != nil {
			logger.Warn("Invalid wildcard pattern %q: %v", pattern, err)
			continue
		}
		if matchedPattern {
			matched = append(matched, toolName)
		}
	}

	return matched
}

// getMapKeys returns the keys of a map as a slice of strings
func getMapKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
