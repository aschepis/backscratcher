package agent

import (
	"regexp"
	"strings"

	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/aschepis/backscratcher/staff/logger"
	"github.com/aschepis/backscratcher/staff/tools"
)

type ToolSchema struct {
	Description string
	Schema      map[string]any
	ServerName  string // MCP server name if this tool comes from an MCP server, empty for native tools
}

// ToolProvider provides tool specifications for agents.
// This interface uses llm.ToolSpec to avoid leaking provider-specific types.
type ToolProvider interface {
	SpecsFor(agent *AgentConfig) []llm.ToolSpec
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

func (p *ToolProviderFromRegistry) SpecsFor(agent *AgentConfig) []llm.ToolSpec {
	if agent == nil {
		return nil
	}

	// Expand regexp patterns and collect all matched tool names
	seen := make(map[string]bool)
	var expandedTools []string

	for _, pattern := range agent.Tools {
		if pattern == "" {
			logger.Warn("Empty tool pattern found in agent config, skipping")
			continue
		}

		// Check if pattern is an MCP server pattern or needs regexp matching
		hasServerPrefix := strings.Contains(pattern, ":")

		// Try to match as regexp pattern (or exact match if no special characters)
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
	}

	// Build tool specs for all matched tools
	var out []llm.ToolSpec
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
		requiredRaw := schema.Schema["required"]
		if requiredRaw != nil {
			switch req := requiredRaw.(type) {
			case []string:
				required = req
			case []any:
				// Handle case where JSON unmarshaling produces []interface{} instead of []string
				// Note: []any is an alias for []interface{}, so this handles both
				required = make([]string, 0, len(req))
				for _, v := range req {
					if str, ok := v.(string); ok {
						required = append(required, str)
					}
				}
			default:
				logger.Warn("Tool %s has 'required' field with unexpected type: %T (value: %v). Attempting to convert...", name, requiredRaw, requiredRaw)
				// Last resort: try to convert via type assertion to []interface{}
				if reqSlice, ok := requiredRaw.([]interface{}); ok {
					required = make([]string, 0, len(reqSlice))
					for _, v := range reqSlice {
						if str, ok := v.(string); ok {
							required = append(required, str)
						}
					}
				} else {
					logger.Error("Failed to extract required fields for tool %s: type %T cannot be converted", name, requiredRaw)
				}
			}
		} else {
			logger.Debug("Tool %s has no 'required' field in schema", name)
		}

		// Extra fields (e.g. descriptions) go into ExtraFields
		extra := map[string]any{}
		for k, v := range schema.Schema {
			if k != "properties" && k != "required" {
				extra[k] = v
			}
		}

		toolSpec := llm.ToolSpec{
			Name:        name,
			Description: schema.Description,
			Schema: llm.ToolSchema{
				Type:        "object",
				Properties:  props,
				Required:    required,
				ExtraFields: extra,
			},
		}

		logger.Debug("Tool schema for %s: required=%v, properties=%v", name, required, getPropertyNames(props))

		out = append(out, toolSpec)
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

// expandToolPattern expands a tool pattern (with optional MCP server prefix) into matching tool names using regexp
func (p *ToolProviderFromRegistry) expandToolPattern(pattern string) []string {
	if pattern == "" {
		return nil
	}

	allTools := p.getAllToolNames()

	var serverFilter string
	toolPattern := pattern

	// Check for MCP server prefix (format: "server:pattern")
	if idx := strings.Index(pattern, ":"); idx != -1 {
		serverFilter = pattern[:idx]
		toolPattern = pattern[idx+1:]
	}

	// Compile the regexp pattern
	re, err := regexp.Compile(toolPattern)
	if err != nil {
		logger.Warn("Invalid regexp pattern %q: %v", pattern, err)
		return nil
	}

	var matched []string
	for _, toolName := range allTools {
		// If server filter is specified, only match tools from that server
		if serverFilter != "" {
			schema, ok := p.schemas[toolName]
			if !ok || schema.ServerName != serverFilter {
				continue
			}
		}

		if re.MatchString(toolName) {
			matched = append(matched, toolName)
		}
	}

	if len(matched) == 0 && serverFilter != "" {
		logger.Warn("Tool pattern %q matched no tools (server %q may not exist)", pattern, serverFilter)
	}

	return matched
}

// getPropertyNames returns the names of properties in a schema properties map
func getPropertyNames(props map[string]any) []string {
	if props == nil {
		return nil
	}
	names := make([]string, 0, len(props))
	for k := range props {
		names = append(names, k)
	}
	return names
}
