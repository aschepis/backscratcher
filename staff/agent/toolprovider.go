package agent

import (
	"regexp"
	"strings"

	"github.com/aschepis/backscratcher/staff/config"
	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/aschepis/backscratcher/staff/tools"
	"github.com/rs/zerolog"
)

type ToolSchema struct {
	Description string
	Schema      map[string]any
	ServerName  string // MCP server name if this tool comes from an MCP server, empty for native tools
}

// ToolProvider provides tool specifications for agents.
// This interface uses llm.ToolSpec to avoid leaking provider-specific types.
type ToolProvider interface {
	SpecsFor(agent *config.AgentConfig) []llm.ToolSpec
}

// TODO: remove factory pattern
type ToolProviderFromRegistry struct {
	registry *tools.Registry
	schemas  map[string]ToolSchema
	logger   zerolog.Logger
}

func NewToolProvider(reg *tools.Registry, logger zerolog.Logger) *ToolProviderFromRegistry {
	return &ToolProviderFromRegistry{
		registry: reg,
		schemas:  make(map[string]ToolSchema),
		logger:   logger.With().Str("component", "toolProvider").Logger(),
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

func (p *ToolProviderFromRegistry) SpecsFor(agent *config.AgentConfig) []llm.ToolSpec {
	if agent == nil {
		return nil
	}

	// Expand regexp patterns and collect all matched tool names
	seen := make(map[string]bool)
	var expandedTools []string

	for _, pattern := range agent.Tools {
		if pattern == "" {
			p.logger.Warn().Msg("Empty tool pattern found in agent config, skipping")
			continue
		}

		// Check if pattern is an MCP server pattern or needs regexp matching
		hasServerPrefix := strings.Contains(pattern, ":")

		// Try to match as regexp pattern (or exact match if no special characters)
		matched := p.expandToolPattern(pattern)
		if len(matched) == 0 {
			// Only warn if this is not a server prefix pattern (those warn inside expandToolPattern)
			if !hasServerPrefix {
				p.logger.Warn().
					Str("pattern", pattern).
					Msg("Tool pattern matched no tools")
			}
		} else {
			p.logger.Debug().
				Str("pattern", pattern).
				Int("matchCount", len(matched)).
				Strs("matchedTools", matched).
				Msg("Tool pattern matched tools")
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
				p.logger.Warn().
					Str("tool", name).
					Any("type", requiredRaw).
					Msg("Tool has 'required' field with unexpected type. Attempting to convert...")
				// Last resort: try to convert via type assertion to []interface{}
				if reqSlice, ok := requiredRaw.([]interface{}); ok {
					required = make([]string, 0, len(reqSlice))
					for _, v := range reqSlice {
						if str, ok := v.(string); ok {
							required = append(required, str)
						}
					}
				} else {
					p.logger.Error().
						Str("tool", name).
						Any("type", requiredRaw).
						Msg("Failed to extract required fields for tool: type cannot be converted")
				}
			}
		} else {
			p.logger.Debug().
				Str("tool", name).
				Msg("Tool has no 'required' field in schema")
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

		p.logger.Debug().
			Str("tool", name).
			Strs("required", required).
			Strs("properties", getPropertyNames(props)).
			Msg("Tool schema")

		out = append(out, toolSpec)
	}

	// Log warnings for missing tools but don't fail (allow partial matches)
	if len(missingTools) > 0 {
		p.logger.Warn().
			Strs("missingTools", missingTools).
			Msg("Some tools were not found in registry")
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
	if server, tools, found := strings.Cut(pattern, ":"); found {
		serverFilter = server
		toolPattern = tools
	}

	// Compile the regexp pattern
	re, err := regexp.Compile(toolPattern)
	if err != nil {
		p.logger.Warn().
			Str("pattern", pattern).
			Err(err).
			Msg("Invalid regexp pattern")
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
		p.logger.Warn().
			Str("pattern", pattern).
			Str("server", serverFilter).
			Msg("Tool pattern matched no tools (server may not exist)")
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
