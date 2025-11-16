package mcp

import (
	"strings"
)

// NameAdapter handles mapping between MCP tool names (which may contain dots)
// and safe tool names (which must not contain dots for Anthropic API).
type NameAdapter struct {
	safeToOriginal map[string]string
	originalToSafe map[string]string
}

// NewNameAdapter creates a new name adapter.
func NewNameAdapter() *NameAdapter {
	return &NameAdapter{
		safeToOriginal: make(map[string]string),
		originalToSafe: make(map[string]string),
	}
}

// ToSafeName converts an MCP tool name to a safe name by replacing dots with underscores.
// Example: "gmail.messages.list" -> "gmail_messages_list"
func ToSafeName(original string) string {
	return strings.ReplaceAll(original, ".", "_")
}

// ToOriginalName converts a safe name back to the original MCP tool name.
// This requires the adapter to have the mapping stored.
func (a *NameAdapter) ToOriginalName(safe string) (string, bool) {
	original, ok := a.safeToOriginal[safe]
	return original, ok
}

// RegisterMapping registers a bidirectional mapping between original and safe names.
func (a *NameAdapter) RegisterMapping(original, safe string) {
	a.originalToSafe[original] = safe
	a.safeToOriginal[safe] = original
}

// GetSafeName returns the safe name for an original name, creating the mapping if needed.
func (a *NameAdapter) GetSafeName(original string) string {
	if safe, ok := a.originalToSafe[original]; ok {
		return safe
	}
	safe := ToSafeName(original)
	a.RegisterMapping(original, safe)
	return safe
}

