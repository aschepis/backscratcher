package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateWorkspacePath(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath, err := filepath.Abs(tmpDir)
	if err != nil {
		t.Fatalf("Failed to get absolute path: %v", err)
	}

	tests := []struct {
		name        string
		workspace   string
		target      string
		wantErr     bool
		description string
	}{
		{
			name:        "valid relative path",
			workspace:   workspacePath,
			target:      "test.txt",
			wantErr:     false,
			description: "Should allow relative paths within workspace",
		},
		{
			name:        "valid absolute path within workspace",
			workspace:   workspacePath,
			target:      filepath.Join(workspacePath, "test.txt"),
			wantErr:     false,
			description: "Should allow absolute paths within workspace",
		},
		{
			name:        "path traversal attempt",
			workspace:   workspacePath,
			target:      "../../../etc/passwd",
			wantErr:     true,
			description: "Should block directory traversal attacks",
		},
		{
			name:        "path outside workspace",
			workspace:   workspacePath,
			target:      "/etc/passwd",
			wantErr:     true,
			description: "Should block paths outside workspace",
		},
		{
			name:        "valid nested path",
			workspace:   workspacePath,
			target:      "dir/subdir/file.txt",
			wantErr:     false,
			description: "Should allow nested paths within workspace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateWorkspacePath(tt.workspace, tt.target)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateWorkspacePath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got == "" {
				t.Errorf("validateWorkspacePath() returned empty path for valid input")
			}
		})
	}
}

func TestReadFile(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath, _ := filepath.Abs(tmpDir)

	// Create test file
	testFile := filepath.Join(workspacePath, "test.txt")
	testContent := "Hello, World!\nThis is a test file."
	if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	reg := NewRegistry()
	reg.RegisterFilesystemTools(workspacePath)

	ctx := context.Background()
	args := json.RawMessage(`{"path": "test.txt"}`)

	result, err := reg.Handle(ctx, "read_file", "test-agent", args)
	if err != nil {
		t.Fatalf("read_file failed: %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Expected map[string]any, got %T", result)
	}

	if content, ok := resultMap["content"].(string); !ok || content != testContent {
		t.Errorf("Expected content %q, got %q", testContent, content)
	}
}

func TestWriteFile(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath, _ := filepath.Abs(tmpDir)

	reg := NewRegistry()
	reg.RegisterFilesystemTools(workspacePath)

	ctx := context.Background()
	testContent := "This is written content\nWith multiple lines."
	args := json.RawMessage(`{"path": "output.txt", "content": "This is written content\nWith multiple lines.", "create_dirs": false}`)

	result, err := reg.Handle(ctx, "write_file", "test-agent", args)
	if err != nil {
		t.Fatalf("write_file failed: %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Expected map[string]any, got %T", result)
	}

	if written, ok := resultMap["written"].(bool); !ok || !written {
		t.Error("Expected written=true")
	}

	// Verify file was created
	expectedPath := filepath.Join(workspacePath, "output.txt")
	content, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("Failed to read created file: %v", err)
	}

	if string(content) != testContent {
		t.Errorf("Expected file content %q, got %q", testContent, string(content))
	}
}

func TestListDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath, _ := filepath.Abs(tmpDir)

	// Create test directory structure
	os.MkdirAll(filepath.Join(workspacePath, "dir1"), 0755)
	os.WriteFile(filepath.Join(workspacePath, "file1.txt"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(workspacePath, "file2.txt"), []byte("content"), 0644)

	reg := NewRegistry()
	reg.RegisterFilesystemTools(workspacePath)

	ctx := context.Background()
	args := json.RawMessage(`{"path": "."}`)

	result, err := reg.Handle(ctx, "list_directory", "test-agent", args)
	if err != nil {
		t.Fatalf("list_directory failed: %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Expected map[string]any, got %T", result)
	}

	entries, ok := resultMap["entries"].([]map[string]any)
	if !ok {
		t.Fatalf("Expected []map[string]any, got %T", resultMap["entries"])
	}

	if len(entries) < 3 {
		t.Errorf("Expected at least 3 entries, got %d", len(entries))
	}
}

func TestFileSearch(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath, _ := filepath.Abs(tmpDir)

	// Create test files
	os.WriteFile(filepath.Join(workspacePath, "test1.go"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(workspacePath, "test2.go"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(workspacePath, "test.txt"), []byte("content"), 0644)

	reg := NewRegistry()
	reg.RegisterFilesystemTools(workspacePath)

	ctx := context.Background()
	args := json.RawMessage(`{"pattern": "*.go"}`)

	result, err := reg.Handle(ctx, "file_search", "test-agent", args)
	if err != nil {
		t.Fatalf("file_search failed: %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Expected map[string]any, got %T", result)
	}

	matches, ok := resultMap["matches"].([]string)
	if !ok {
		t.Fatalf("Expected []string, got %T", resultMap["matches"])
	}

	if len(matches) < 2 {
		t.Errorf("Expected at least 2 .go files, got %d", len(matches))
	}
}

func TestFileInfo(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath, _ := filepath.Abs(tmpDir)

	testFile := filepath.Join(workspacePath, "info.txt")
	os.WriteFile(testFile, []byte("test content"), 0644)

	reg := NewRegistry()
	reg.RegisterFilesystemTools(workspacePath)

	ctx := context.Background()
	args := json.RawMessage(`{"path": "info.txt"}`)

	result, err := reg.Handle(ctx, "file_info", "test-agent", args)
	if err != nil {
		t.Fatalf("file_info failed: %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Expected map[string]any, got %T", result)
	}

	if exists, ok := resultMap["exists"].(bool); !ok || !exists {
		t.Error("Expected exists=true")
	}

	if isDir, ok := resultMap["is_dir"].(bool); !ok || isDir {
		t.Error("Expected is_dir=false for a file")
	}
}

func TestCreateDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath, _ := filepath.Abs(tmpDir)

	reg := NewRegistry()
	reg.RegisterFilesystemTools(workspacePath)

	ctx := context.Background()
	args := json.RawMessage(`{"path": "newdir", "parents": false}`)

	result, err := reg.Handle(ctx, "create_directory", "test-agent", args)
	if err != nil {
		t.Fatalf("create_directory failed: %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Expected map[string]any, got %T", result)
	}

	if created, ok := resultMap["created"].(bool); !ok || !created {
		t.Error("Expected created=true")
	}

	// Verify directory was created
	expectedPath := filepath.Join(workspacePath, "newdir")
	info, err := os.Stat(expectedPath)
	if err != nil {
		t.Fatalf("Directory was not created: %v", err)
	}

	if !info.IsDir() {
		t.Error("Created path is not a directory")
	}
}

func TestGrepSearch(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath, _ := filepath.Abs(tmpDir)

	testFile := filepath.Join(workspacePath, "search.txt")
	content := "line1: hello\nline2: world\nline3: hello world\nline4: test"
	os.WriteFile(testFile, []byte(content), 0644)

	reg := NewRegistry()
	reg.RegisterFilesystemTools(workspacePath)

	ctx := context.Background()
	args := json.RawMessage(`{"pattern": "hello", "path": "search.txt"}`)

	result, err := reg.Handle(ctx, "grep_search", "test-agent", args)
	if err != nil {
		t.Fatalf("grep_search failed: %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Expected map[string]any, got %T", result)
	}

	matches, ok := resultMap["matches"].([]map[string]any)
	if !ok {
		t.Fatalf("Expected []map[string]any, got %T", resultMap["matches"])
	}

	if len(matches) < 2 {
		t.Errorf("Expected at least 2 matches, got %d", len(matches))
	}
}

