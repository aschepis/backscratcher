package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type exportMsg struct {
	Time   string `json:"time"`
	FromMe bool   `json:"from_me"`
	Text   string `json:"text"`
}

type exportConvoJSON struct {
	Phone       string      `json:"phone"`
	DisplayName string      `json:"display_name,omitempty"`
	Messages    []exportMsg `json:"messages"`
}

// ExportFormat selects the output file format.
type ExportFormat int

const (
	ExportJSON     ExportFormat = iota
	ExportMarkdown ExportFormat = iota
)

// exportConvos writes each conversation to a separate file in outDir.
// outDir must already exist. Returns the list of files written.
func exportConvos(convos []Convo, format ExportFormat, outDir string) ([]string, error) {
	outDir = filepath.Clean(outDir)
	if info, err := os.Stat(outDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("output directory does not exist: %s", outDir)
	}

	var written []string
	for _, c := range convos {
		name := sanitizeFilename(c.Label())
		var ext, content string
		var err error
		if format == ExportJSON {
			ext = ".json"
			content, err = toJSON(c)
		} else {
			ext = ".md"
			content, err = toMarkdown(c)
		}
		if err != nil {
			return written, fmt.Errorf("encode %s: %w", c.Phone, err)
		}
		path := filepath.Join(outDir, name+ext)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return written, fmt.Errorf("write %s: %w", path, err)
		}
		written = append(written, path)
	}
	return written, nil
}

func toJSON(c Convo) (string, error) {
	rec := exportConvoJSON{
		Phone:       c.Phone,
		DisplayName: c.DisplayName,
		Messages:    make([]exportMsg, 0, len(c.Messages)),
	}
	for _, m := range c.Messages {
		rec.Messages = append(rec.Messages, exportMsg{
			Time:   m.Time.Format("2006-01-02T15:04:05"),
			FromMe: m.FromMe,
			Text:   m.Text,
		})
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func toMarkdown(c Convo) (string, error) {
	var sb strings.Builder
	label := c.Label()
	fmt.Fprintf(&sb, "# Conversation with %s\n\n", label)
	if c.DisplayName != "" && c.Phone != c.DisplayName {
		fmt.Fprintf(&sb, "**Phone:** %s\n\n", c.Phone)
	}
	for _, m := range c.Messages {
		if strings.TrimSpace(m.Text) == "" {
			continue
		}
		sender := label
		if m.FromMe {
			sender = "Me"
		}
		fmt.Fprintf(&sb, "**%s** _%s_\n%s\n\n",
			sender, m.Time.Format("Jan 02, 2006 15:04"), m.Text)
	}
	return sb.String(), nil
}

// sanitizeFilename replaces characters unsafe for filenames with underscores.
func sanitizeFilename(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' ||
			r == '"' || r == '<' || r == '>' || r == '|' || r == '\x00' {
			sb.WriteRune('_')
		} else {
			sb.WriteRune(r)
		}
	}
	name := strings.TrimSpace(sb.String())
	if name == "" {
		name = "conversation"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	return name
}
