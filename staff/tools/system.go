package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Dangerous command patterns that should be blocked
var dangerousPatterns = []string{
	"rm ", "rm -", "rmdir", "unlink",
	"format", "mkfs", "dd ",
	"sudo rm", "sudo format", "sudo mkfs",
	"chmod 777", "chmod 000",
	"curl | sh", "curl | bash", "wget | sh", "wget | bash",
	"> /dev/sd", "of=/dev/sd", "of=/dev/hd",
	"rm -rf /", "rm -rf ~", "rm -rf *",
	"mkfs.", "format ", "fdisk ",
	"dd if=", "dd of=",
}

// isDangerousCommand checks if a command contains dangerous patterns
func isDangerousCommand(command string) bool {
	cmdLower := strings.ToLower(command)
	for _, pattern := range dangerousPatterns {
		if strings.Contains(cmdLower, strings.ToLower(pattern)) {
			return true
		}
	}

	// Explicitly block curl/wget pipelines that execute shells, even with args between.
	if (strings.Contains(cmdLower, "curl") || strings.Contains(cmdLower, "wget")) &&
		strings.Contains(cmdLower, "|") &&
		(strings.Contains(cmdLower, "| sh") || strings.Contains(cmdLower, "| bash")) {
		return true
	}

	// Block commands that try to write outside workspace
	if strings.Contains(cmdLower, "> ") {
		// Check if redirecting outside workspace
		parts := strings.Split(cmdLower, ">")
		if len(parts) > 1 {
			target := strings.TrimSpace(parts[1])
			// Block redirects to absolute paths outside typical temp dirs
			if filepath.IsAbs(target) && !strings.HasPrefix(target, "/tmp/") && !strings.HasPrefix(target, "/var/tmp/") {
				// Only allow if it's within workspace - but this is hard to validate, so be conservative
				return true
			}
		}
	}

	return false
}

// RegisterSystemTools registers all system/command execution tools
func (r *Registry) RegisterSystemTools(workspacePath string) {
	r.logger.Info().Msg("Registering system tools in registry")

	r.Register("execute_command", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			Command    string   `json:"command"`
			Args       []string `json:"args"`
			Timeout    int      `json:"timeout"` // in seconds
			WorkingDir string   `json:"working_dir"`
			Stdin      string   `json:"stdin"`
		}
		if err := json.Unmarshal(args, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
		}

		// Validate command is not dangerous
		fullCommand := payload.Command
		if len(payload.Args) > 0 {
			fullCommand += " " + strings.Join(payload.Args, " ")
		}

		if isDangerousCommand(fullCommand) {
			r.logger.Warn().Str("agentID", agentID).Str("command", fullCommand).Msg("Blocked dangerous command from agent")
			return nil, fmt.Errorf("command blocked: this command appears to be dangerous and could damage the system or delete files. Please use safer alternatives and avoid commands that modify or delete files, format disks, or execute arbitrary code from the internet")
		}

		// Set default timeout
		timeoutSeconds := 30
		if payload.Timeout > 0 {
			timeoutSeconds = payload.Timeout
		}
		if timeoutSeconds > 300 {
			timeoutSeconds = 300 // Cap at 5 minutes
		}

		// Determine working directory
		workDir := workspacePath
		if payload.WorkingDir != "" {
			validWorkDir, err := validateWorkspacePath(workspacePath, payload.WorkingDir)
			if err != nil {
				return nil, fmt.Errorf("invalid working directory: %w", err)
			}
			workDir = validWorkDir
		}

		// Create context with timeout
		cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
		defer cancel()

		// Build command
		var cmd *exec.Cmd
		if len(payload.Args) > 0 {
			cmd = exec.CommandContext(cmdCtx, payload.Command, payload.Args...) //#nosec G204 -- intentional command execution
		} else {
			// If no args, try to split the command string (for shell commands)
			parts := strings.Fields(payload.Command)
			if len(parts) > 1 {
				cmd = exec.CommandContext(cmdCtx, parts[0], parts[1:]...) //#nosec G204 -- intentional command execution
			} else {
				cmd = exec.CommandContext(cmdCtx, payload.Command) //#nosec G204 -- intentional command execution
			}
		}

		cmd.Dir = workDir

		// Set stdin if provided
		if payload.Stdin != "" {
			cmd.Stdin = strings.NewReader(payload.Stdin)
		}

		// Capture stdout and stderr
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
		}

		// Start command
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("failed to start command: %w", err)
		}

		// Read output
		stdoutBytes := make([]byte, 0)
		stderrBytes := make([]byte, 0)

		// Use channels to read stdout and stderr concurrently
		stdoutDone := make(chan error, 1)
		stderrDone := make(chan error, 1)

		go func() {
			buf := make([]byte, 4096)
			for {
				n, err := stdout.Read(buf)
				if n > 0 {
					stdoutBytes = append(stdoutBytes, buf[:n]...)
					// Limit stdout size to prevent memory issues
					if len(stdoutBytes) > 1024*1024 { // 1MB limit
						stdoutDone <- fmt.Errorf("stdout exceeded 1MB limit")
						return
					}
				}
				if err != nil {
					stdoutDone <- err
					return
				}
			}
		}()

		go func() {
			buf := make([]byte, 4096)
			for {
				n, err := stderr.Read(buf)
				if n > 0 {
					stderrBytes = append(stderrBytes, buf[:n]...)
					// Limit stderr size to prevent memory issues
					if len(stderrBytes) > 1024*1024 { // 1MB limit
						stderrDone <- fmt.Errorf("stderr exceeded 1MB limit")
						return
					}
				}
				if err != nil {
					stderrDone <- err
					return
				}
			}
		}()

		// Wait for command to complete or timeout
		cmdDone := make(chan error, 1)
		go func() {
			cmdDone <- cmd.Wait()
		}()

		// Wait for all to complete or timeout
		select {
		case <-cmdCtx.Done():
			// Timeout or context cancelled
			_ = cmd.Process.Kill() // Ignore error if process already exited
			return nil, fmt.Errorf("command timed out after %d seconds", timeoutSeconds)
		case err := <-cmdDone:
			// Command finished, wait for output
			<-stdoutDone
			<-stderrDone

			exitCode := 0
			if err != nil {
				if exitError, ok := err.(*exec.ExitError); ok {
					exitCode = exitError.ExitCode()
				} else {
					return nil, fmt.Errorf("command failed: %w", err)
				}
			}

			return map[string]any{
				"command":   fullCommand,
				"exit_code": exitCode,
				"stdout":    string(stdoutBytes),
				"stderr":    string(stderrBytes),
				"success":   exitCode == 0,
			}, nil
		}
	})
}
