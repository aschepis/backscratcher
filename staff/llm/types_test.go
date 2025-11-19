package llm

import (
	"encoding/json"
	"testing"
)

func TestNewTextMessage(t *testing.T) {
	msg := NewTextMessage(RoleUser, "Hello, world!")
	if msg.Role != RoleUser {
		t.Errorf("Expected role %v, got %v", RoleUser, msg.Role)
	}
	if len(msg.Content) != 1 {
		t.Errorf("Expected 1 content block, got %d", len(msg.Content))
	}
	if msg.Content[0].Type != ContentBlockTypeText {
		t.Errorf("Expected text block type, got %v", msg.Content[0].Type)
	}
	if msg.Content[0].Text != "Hello, world!" {
		t.Errorf("Expected text 'Hello, world!', got %q", msg.Content[0].Text)
	}
}

func TestNewToolUseMessage(t *testing.T) {
	toolUses := []ToolUseBlock{
		{ID: "tool-1", Name: "test_tool", Input: map[string]interface{}{"arg": "value"}},
	}
	msg := NewToolUseMessage(toolUses)
	if msg.Role != RoleAssistant {
		t.Errorf("Expected role %v, got %v", RoleAssistant, msg.Role)
	}
	if len(msg.Content) != 1 {
		t.Errorf("Expected 1 content block, got %d", len(msg.Content))
	}
	if msg.Content[0].Type != ContentBlockTypeToolUse {
		t.Errorf("Expected tool use block type, got %v", msg.Content[0].Type)
	}
	if msg.Content[0].ToolUse == nil {
		t.Fatal("Expected ToolUse to be set")
	}
	if msg.Content[0].ToolUse.ID != "tool-1" {
		t.Errorf("Expected tool ID 'tool-1', got %q", msg.Content[0].ToolUse.ID)
	}
}

func TestNewToolResultMessage(t *testing.T) {
	toolResults := []ToolResultBlock{
		{ID: "tool-1", Content: `{"result": "success"}`, IsError: false},
	}
	msg := NewToolResultMessage(toolResults)
	if msg.Role != RoleUser {
		t.Errorf("Expected role %v, got %v", RoleUser, msg.Role)
	}
	if len(msg.Content) != 1 {
		t.Errorf("Expected 1 content block, got %d", len(msg.Content))
	}
	if msg.Content[0].Type != ContentBlockTypeToolResult {
		t.Errorf("Expected tool result block type, got %v", msg.Content[0].Type)
	}
	if msg.Content[0].ToolResult == nil {
		t.Fatal("Expected ToolResult to be set")
	}
	if msg.Content[0].ToolResult.ID != "tool-1" {
		t.Errorf("Expected tool ID 'tool-1', got %q", msg.Content[0].ToolResult.ID)
	}
}

func TestMessageToJSON(t *testing.T) {
	msg := NewTextMessage(RoleUser, "Test message")
	jsonData, err := msg.ToJSON()
	if err != nil {
		t.Fatalf("Failed to marshal message to JSON: %v", err)
	}
	if len(jsonData) == 0 {
		t.Fatal("Expected non-empty JSON data")
	}
	// Verify it's valid JSON
	var decoded Message
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}
	if decoded.Role != msg.Role {
		t.Errorf("Expected role %v, got %v", msg.Role, decoded.Role)
	}
}
