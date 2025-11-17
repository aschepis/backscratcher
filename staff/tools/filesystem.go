package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aschepis/backscratcher/staff/logger"
)

// validateWorkspacePath ensures the given path is within the workspace directory
// and prevents directory traversal attacks
func validateWorkspacePath(workspacePath, targetPath string) (string, error) {
	// Clean the workspace path
	workspacePath = filepath.Clean(workspacePath)
	absWorkspace, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", fmt.Errorf("invalid workspace path: %w", err)
	}

	// If target is absolute, validate it directly
	if filepath.IsAbs(targetPath) {
		absTarget := filepath.Clean(targetPath)
		if !strings.HasPrefix(absTarget+string(filepath.Separator), absWorkspace+string(filepath.Separator)) {
			return "", fmt.Errorf("path outside workspace: %s", targetPath)
		}
		return absTarget, nil
	}

	// For relative paths, join with workspace and validate
	joined := filepath.Join(absWorkspace, targetPath)
	absTarget, err := filepath.Abs(joined)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	// Ensure the resolved path is still within workspace
	if !strings.HasPrefix(absTarget+string(filepath.Separator), absWorkspace+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal detected: %s", targetPath)
	}

	return absTarget, nil
}

// RegisterFilesystemTools registers all filesystem-related tools
func (r *Registry) RegisterFilesystemTools(workspacePath string) {
	logger.Info("Registering filesystem tools in registry")

	r.Register("read_file", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			Path     string `json:"path"`
			Encoding string `json:"encoding"`
			MaxBytes int64  `json:"max_bytes"`
		}
		if err := json.Unmarshal(args, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
		}

		if payload.Encoding == "" {
			payload.Encoding = "utf-8"
		}

		validPath, err := validateWorkspacePath(workspacePath, payload.Path)
		if err != nil {
			return nil, err
		}

		info, err := os.Stat(validPath)
		if err != nil {
			return nil, fmt.Errorf("failed to stat file: %w", err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("path is a directory, not a file: %s", payload.Path)
		}

		file, err := os.Open(validPath) //#nosec 304 -- validated above
		if err != nil {
			return nil, fmt.Errorf("failed to open file: %w", err)
		}
		defer file.Close() //nolint:errcheck // File close error can be ignored

		var content []byte
		if payload.MaxBytes > 0 {
			content = make([]byte, payload.MaxBytes)
			n, err := file.Read(content)
			if err != nil && err != io.EOF {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}
			content = content[:n]
		} else {
			content, err = io.ReadAll(file)
			if err != nil {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}
		}

		contentStr := string(content)
		if payload.Encoding != "utf-8" {
			// For now, we only support UTF-8. In the future, we could add encoding conversion
			logger.Warn("Non-UTF-8 encoding requested but not yet supported: %s", payload.Encoding)
		}

		return map[string]any{
			"content": contentStr,
			"size":    len(content),
			"path":    payload.Path,
		}, nil
	})

	r.Register("write_file", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			Path       string `json:"path"`
			Content    string `json:"content"`
			CreateDirs bool   `json:"create_dirs"`
		}
		if err := json.Unmarshal(args, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
		}

		validPath, err := validateWorkspacePath(workspacePath, payload.Path)
		if err != nil {
			return nil, err
		}

		// Create parent directories if needed
		if payload.CreateDirs {
			parentDir := filepath.Dir(validPath)
			if err := os.MkdirAll(parentDir, 0o750); err != nil {
				return nil, fmt.Errorf("failed to create parent directories: %w", err)
			}
		}

		if err := os.WriteFile(validPath, []byte(payload.Content), 0o600); err != nil {
			return nil, fmt.Errorf("failed to write file: %w", err)
		}

		info, err := os.Stat(validPath)
		if err != nil {
			return nil, fmt.Errorf("failed to stat written file: %w", err)
		}

		return map[string]any{
			"path":    payload.Path,
			"size":    info.Size(),
			"written": true,
		}, nil
	})

	r.Register("list_directory", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			Path          string `json:"path"`
			Recursive     bool   `json:"recursive"`
			IncludeHidden bool   `json:"include_hidden"`
		}
		if err := json.Unmarshal(args, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
		}

		if payload.Path == "" {
			payload.Path = "."
		}

		validPath, err := validateWorkspacePath(workspacePath, payload.Path)
		if err != nil {
			return nil, err
		}

		info, err := os.Stat(validPath)
		if err != nil {
			return nil, fmt.Errorf("failed to stat path: %w", err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("path is not a directory: %s", payload.Path)
		}

		var entries []map[string]any
		if payload.Recursive {
			err = filepath.Walk(validPath, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				relPath, err := filepath.Rel(workspacePath, path)
				if err != nil {
					return err
				}
				name := info.Name()
				if !payload.IncludeHidden && strings.HasPrefix(name, ".") {
					if info.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				entries = append(entries, map[string]any{
					"path":     relPath,
					"name":     name,
					"is_dir":   info.IsDir(),
					"size":     info.Size(),
					"mode":     info.Mode().String(),
					"mod_time": info.ModTime().Unix(),
				})
				return nil
			})
		} else {
			dirEntries, err := os.ReadDir(validPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read directory: %w", err)
			}
			for _, entry := range dirEntries {
				name := entry.Name()
				if !payload.IncludeHidden && strings.HasPrefix(name, ".") {
					continue
				}
				info, err := entry.Info()
				if err != nil {
					continue
				}
				relPath := filepath.Join(payload.Path, name)
				entries = append(entries, map[string]any{
					"path":     relPath,
					"name":     name,
					"is_dir":   entry.IsDir(),
					"size":     info.Size(),
					"mode":     info.Mode().String(),
					"mod_time": info.ModTime().Unix(),
				})
			}
		}

		if err != nil {
			return nil, fmt.Errorf("failed to walk directory: %w", err)
		}

		return map[string]any{
			"path":    payload.Path,
			"entries": entries,
			"count":   len(entries),
		}, nil
	})

	r.Register("file_search", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			Pattern string `json:"pattern"`
			Root    string `json:"root"`
			Limit   int    `json:"limit"`
		}
		if err := json.Unmarshal(args, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
		}

		if payload.Root == "" {
			payload.Root = "."
		}
		if payload.Limit == 0 {
			payload.Limit = 100
		}

		validRoot, err := validateWorkspacePath(workspacePath, payload.Root)
		if err != nil {
			return nil, err
		}

		var matches []string
		_ = filepath.Walk(validRoot, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // Skip errors
			}
			if len(matches) >= payload.Limit {
				return filepath.SkipAll
			}
			relPath, err := filepath.Rel(workspacePath, path)
			if err != nil {
				return nil
			}
			matched, err := filepath.Match(payload.Pattern, info.Name())
			if err != nil {
				return nil // Invalid pattern, skip
			}
			if matched {
				matches = append(matches, relPath)
			}
			return nil
		})

		// Also handle ** patterns for recursive matching
		if strings.Contains(payload.Pattern, "**") {
			// Convert ** pattern to proper glob pattern
			parts := strings.Split(payload.Pattern, "**")
			if len(parts) == 2 {
				prefix := strings.TrimSuffix(parts[0], "/")
				suffix := strings.TrimPrefix(parts[1], "/")
				_ = filepath.Walk(validRoot, func(path string, info os.FileInfo, err error) error {
					if err != nil {
						return nil
					}
					if len(matches) >= payload.Limit {
						return filepath.SkipAll
					}
					relPath, err := filepath.Rel(workspacePath, path)
					if err != nil {
						return nil
					}
					if prefix != "" && !strings.HasPrefix(relPath, prefix) {
						return nil
					}
					matched, err := filepath.Match(suffix, info.Name())
					if err != nil {
						return nil
					}
					if matched {
						// Check if already in matches
						found := false
						for _, m := range matches {
							if m == relPath {
								found = true
								break
							}
						}
						if !found {
							matches = append(matches, relPath)
						}
					}
					return nil
				})
			}
		}

		return map[string]any{
			"pattern": payload.Pattern,
			"root":    payload.Root,
			"matches": matches,
			"count":   len(matches),
		}, nil
	})

	r.Register("file_info", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(args, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
		}

		validPath, err := validateWorkspacePath(workspacePath, payload.Path)
		if err != nil {
			return nil, err
		}

		info, err := os.Stat(validPath)
		if err != nil {
			return nil, fmt.Errorf("failed to stat file: %w", err)
		}

		return map[string]any{
			"path":     payload.Path,
			"exists":   true,
			"is_dir":   info.IsDir(),
			"size":     info.Size(),
			"mode":     info.Mode().String(),
			"mod_time": info.ModTime().Unix(),
			"perm":     info.Mode().Perm().String(),
		}, nil
	})

	r.Register("create_directory", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			Path    string `json:"path"`
			Parents bool   `json:"parents"`
		}
		if err := json.Unmarshal(args, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
		}

		validPath, err := validateWorkspacePath(workspacePath, payload.Path)
		if err != nil {
			return nil, err
		}

		var mode os.FileMode = 0o750
		if payload.Parents {
			err = os.MkdirAll(validPath, mode)
		} else {
			err = os.Mkdir(validPath, mode)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create directory: %w", err)
		}

		info, err := os.Stat(validPath)
		if err != nil {
			return nil, fmt.Errorf("failed to stat created directory: %w", err)
		}

		return map[string]any{
			"path":    payload.Path,
			"created": true,
			"mode":    info.Mode().String(),
		}, nil
	})

	r.Register("grep_search", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			Pattern       string `json:"pattern"`
			Path          string `json:"path"`
			CaseSensitive bool   `json:"case_sensitive"`
			ContextLines  int    `json:"context_lines"`
		}
		if err := json.Unmarshal(args, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
		}

		if payload.ContextLines < 0 {
			payload.ContextLines = 0
		}
		if payload.ContextLines > 5 {
			payload.ContextLines = 5 // Limit context to prevent huge results
		}

		validPath, err := validateWorkspacePath(workspacePath, payload.Path)
		if err != nil {
			return nil, err
		}

		info, err := os.Stat(validPath)
		if err != nil {
			return nil, fmt.Errorf("failed to stat path: %w", err)
		}

		var matches []map[string]any

		searchFile := func(filePath, relFilePath string) error {
			content, err := os.ReadFile(filePath) //#nosec 304 -- intentional file read for grepping
			if err != nil {
				return err
			}

			flags := ""
			if !payload.CaseSensitive {
				flags = "(?i)"
			}

			re, err := regexp.Compile(flags + payload.Pattern)
			if err != nil {
				return fmt.Errorf("invalid regex pattern: %w", err)
			}

			lines := strings.Split(string(content), "\n")
			for lineNum, line := range lines {
				if re.MatchString(line) {
					match := map[string]any{
						"line_number": lineNum + 1,
						"line":        line,
						"file":        relFilePath,
						"context":     []string{},
					}

					// Add context lines
					if payload.ContextLines > 0 {
						start := lineNum - payload.ContextLines
						if start < 0 {
							start = 0
						}
						end := lineNum + payload.ContextLines + 1
						if end > len(lines) {
							end = len(lines)
						}
						context := []string{}
						for i := start; i < end; i++ {
							if i != lineNum {
								context = append(context, lines[i])
							}
						}
						match["context"] = context
					}

					matches = append(matches, match)
				}
			}
			return nil
		}

		absWorkspace, err := filepath.Abs(workspacePath)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve workspace path: %w", err)
		}

		if info.IsDir() {
			_ = filepath.Walk(validPath, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				if !info.IsDir() {
					relPath, err := filepath.Rel(absWorkspace, path)
					if err != nil {
						return nil
					}
					if searchErr := searchFile(path, relPath); searchErr != nil {
						// Continue on errors
						return nil
					}
				}
				return nil
			})
		} else {
			relPath, err := filepath.Rel(absWorkspace, validPath)
			if err != nil {
				return nil, fmt.Errorf("failed to get relative path: %w", err)
			}
			if err := searchFile(validPath, relPath); err != nil {
				return nil, fmt.Errorf("failed to search: %w", err)
			}
		}

		return map[string]any{
			"pattern": payload.Pattern,
			"path":    payload.Path,
			"matches": matches,
			"count":   len(matches),
		}, nil
	})
}
