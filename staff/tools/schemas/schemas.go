// Package schemas contains tool schema definitions for the Staff application.
// These schemas define the input parameters and descriptions for tools that
// agents can use. The schemas are registered with the ToolProvider at startup.
package schemas

// ToolSchema represents a tool's description and JSON schema.
type ToolSchema struct {
	Description string
	Schema      map[string]any
}

// All returns all tool schemas from all categories.
func All() map[string]ToolSchema {
	schemas := make(map[string]ToolSchema)

	// Merge all category schemas
	for name, schema := range MemorySchemas() {
		schemas[name] = schema
	}
	for name, schema := range FilesystemSchemas() {
		schemas[name] = schema
	}
	for name, schema := range SystemSchemas() {
		schemas[name] = schema
	}
	for name, schema := range NotificationSchemas() {
		schemas[name] = schema
	}
	for name, schema := range StaffSchemas() {
		schemas[name] = schema
	}

	return schemas
}

