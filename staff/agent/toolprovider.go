package agent

import (
	"fmt"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	"github.com/aschepis/backscratcher/staff/tools"
)

type ToolSchema struct {
	Description string
	Schema      map[string]any
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

func (p *ToolProviderFromRegistry) SpecsFor(agent *AgentConfig) []anthropic.ToolUnionParam {
	if agent == nil {
		return nil
	}

	var out []anthropic.ToolUnionParam

	for _, name := range agent.Tools {
		schema, ok := p.schemas[name]
		if !ok {
			panic(fmt.Errorf("schema for tool %q was never registered", name))
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
