package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var ignorePath string

func init() {
	home, _ := os.UserHomeDir()
	ignorePath = filepath.Join(home, ".config", "stop2end", "ignored.json")
}

func loadIgnored() (map[string]struct{}, error) {
	data, err := os.ReadFile(ignorePath)
	if os.IsNotExist(err) {
		return make(map[string]struct{}), nil
	}
	if err != nil {
		return nil, err
	}
	var phones []string
	if err := json.Unmarshal(data, &phones); err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(phones))
	for _, p := range phones {
		out[p] = struct{}{}
	}
	return out, nil
}

func saveIgnored(ignored map[string]struct{}) error {
	phones := make([]string, 0, len(ignored))
	for p := range ignored {
		phones = append(phones, p)
	}
	sort.Strings(phones)
	data, err := json.MarshalIndent(phones, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ignorePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(ignorePath, data, 0o644)
}

const appleScriptTimeout = 10 * time.Second

func runAppleScript(script string) error {
	ctx, cancel := context.WithTimeout(context.Background(), appleScriptTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("timed out after %s — Messages.app may be busy or showing a dialog", appleScriptTimeout)
	}
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// sendUnsubscribe sends the opt-out keyword to phone via Messages.app.
// Both values are validated before embedding in the AppleScript string.
func sendUnsubscribe(phone, keyword string) error {
	if !validKeywords[keyword] {
		return fmt.Errorf("invalid keyword: %q", keyword)
	}
	if !validatePhone(phone) {
		return fmt.Errorf("invalid phone: %q", phone)
	}
	return runAppleScript(fmt.Sprintf(
		"tell application \"Messages\"\n"+
			"    set svc to first service whose service type = SMS\n"+
			"    set bud to buddy \"%s\" of svc\n"+
			"    send \"%s\" to bud\n"+
			"end tell",
		phone, keyword,
	))
}

// deleteConversation deletes the conversation from Messages.app (syncs via iCloud).
// chatIdentifier is the phone number — validated and quote-stripped before use.
func deleteConversation(chatIdentifier string) error {
	safe := strings.ReplaceAll(chatIdentifier, `"`, "")
	return runAppleScript(fmt.Sprintf(
		"tell application \"Messages\"\n"+
			"    set tgt to (first chat whose name is \"%s\")\n"+
			"    delete tgt\n"+
			"end tell",
		safe,
	))
}
