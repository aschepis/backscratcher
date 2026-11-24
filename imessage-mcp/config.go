package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds user-editable settings. It lives at
// ~/Library/Application Support/imessage-mcp/config.json.
type Config struct {
	// Listen is the local address to bind. Keep it on loopback; expose it to
	// the internet only through a tunnel (cloudflared) that terminates TLS.
	Listen string `json:"listen"`

	// PublicURL is the https origin the tunnel serves, e.g.
	// "https://imessage.example.com". Required for OAuth (Claude connectors).
	PublicURL string `json:"public_url"`

	// MaxAgeDays hides anything older than this many days from every tool.
	// 0 means no limit.
	MaxAgeDays int `json:"max_age_days"`

	// ExcludeChats hides chats whose identifier (phone, email, or group id)
	// or display name matches one of these entries exactly.
	ExcludeChats []string `json:"exclude_chats"`

	// HideShortCodes hides chats with 5–6 digit senders (banks, 2FA, marketing).
	HideShortCodes bool `json:"hide_short_codes"`

	// RedactCodes replaces 4–8 digit numbers with [redacted] in messages that
	// look like verification / one-time codes.
	RedactCodes bool `json:"redact_codes"`

	// AllowedRedirectHosts limits which OAuth clients can complete sign-in.
	AllowedRedirectHosts []string `json:"allowed_redirect_hosts"`

	// ChatDB overrides the chat.db path (mostly for testing).
	ChatDB string `json:"chat_db,omitempty"`
}

func defaultConfig() Config {
	return Config{
		Listen:               "127.0.0.1:8765",
		MaxAgeDays:           365,
		HideShortCodes:       true,
		RedactCodes:          true,
		AllowedRedirectHosts: []string{"claude.ai", "claude.com", "localhost", "127.0.0.1"},
	}
}

func configDir() (string, error) {
	if d := os.Getenv("IMESSAGE_MCP_HOME"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "imessage-mcp"), nil
}

func loadConfig(dir string) (Config, error) {
	cfg := defaultConfig()
	path := filepath.Join(dir, "config.json")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// writeConfigIfMissing writes the default config so users have something to edit.
func writeConfigIfMissing(dir string) (string, error) {
	path := filepath.Join(dir, "config.json")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return path, err
	}
	b, _ := json.MarshalIndent(defaultConfig(), "", "  ")
	return path, os.WriteFile(path, append(b, '\n'), 0o600)
}

func (c Config) chatDBPath() (string, error) {
	if c.ChatDB != "" {
		return c.ChatDB, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Messages", "chat.db"), nil
}
