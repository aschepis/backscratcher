package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/aschepis/backscratcher/staff/config"
	"github.com/aschepis/backscratcher/staff/logger"
)

// showSettings displays the settings UI
func (a *App) showSettings() {
	// Load current config
	configPath := a.configPath
	if configPath == "" {
		configPath = config.GetConfigPath()
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		a.showContent("Settings", fmt.Sprintf("Error loading config: %v\n\nPress Esc to go back.", err))
		return
	}

	// Load Claude config to get available projects
	var availableProjects []string
	var projectSelections map[string]bool
	claudeConfigPath := cfg.ClaudeMCP.ConfigPath
	if claudeConfigPath == "" {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			claudeConfigPath = filepath.Join(homeDir, ".claude.json")
		}
	}

	if claudeConfigPath != "" {
		claudeCfg, err := config.LoadClaudeConfig(claudeConfigPath)
		if err == nil {
			// Add "Global" if there are global MCP servers
			if len(claudeCfg.MCPServers) > 0 {
				availableProjects = append(availableProjects, "Global")
			}
			// Add all project paths
			for projectPath := range claudeCfg.Projects {
				availableProjects = append(availableProjects, projectPath)
			}
		}
	}

	// Initialize project selections based on current config
	projectSelections = make(map[string]bool)
	// Default: all projects are disabled (unselected)
	for _, proj := range availableProjects {
		projectSelections[proj] = false
	}
	// If projects are specified in config, mark them as selected
	if len(cfg.ClaudeMCP.Projects) > 0 {
		for _, selectedProj := range cfg.ClaudeMCP.Projects {
			projectSelections[selectedProj] = true
		}
	}

	// Project selection list (only shown if Claude MCP is enabled)
	projectList := tview.NewList()
	projectList.SetBorder(true).SetTitle("Select Projects (Enter/Space: Toggle, ← or F: Back to form)")

	// Claude MCP Enable/Disable
	claudeEnabled := cfg.ClaudeMCP.Enabled

	// Message Summarization settings
	summarizationDisabled := !cfg.MessageSummarization.Disabled
	summarizationModel := cfg.MessageSummarization.Model
	summarizationMaxChars := fmt.Sprintf("%d", cfg.MessageSummarization.MaxChars)
	summarizationMaxLines := fmt.Sprintf("%d", cfg.MessageSummarization.MaxLines)
	summarizationMaxLineBreaks := fmt.Sprintf("%d", cfg.MessageSummarization.MaxLineBreaks)

	var updateProjectList func()
	updateProjectList = func() {
		projectList.Clear()
		if !claudeEnabled {
			projectList.AddItem("Enable Claude MCP to select projects", "", ' ', nil)
			return
		}

		if len(availableProjects) == 0 {
			projectList.AddItem("No projects or global servers found in Claude config", "", ' ', nil)
			projectList.AddItem("", fmt.Sprintf("Config path: %s", claudeConfigPath), ' ', nil)
			return
		}

		for _, proj := range availableProjects {
			projCopy := proj
			selected := projectSelections[proj]
			marker := "☐"
			if selected {
				marker = "☑"
			}
			// Add special label for Global
			label := proj
			if proj == "Global" {
				label = "Global (root-level MCP servers)"
			}
			projectList.AddItem(fmt.Sprintf("%s %s", marker, label), "", ' ', func() {
				projectSelections[projCopy] = !projectSelections[projCopy]
				updateProjectList()
			})
		}
	}

	updateProjectList()

	// Create form for settings
	form := tview.NewForm()
	form.SetBorder(true).SetTitle("Settings (Tab: Navigate form, → or P: Go to projects)")

	// Claude MCP Enable/Disable
	form.AddCheckbox("Enable Claude MCP Integration", claudeEnabled, func(checked bool) {
		claudeEnabled = checked
		updateProjectList()
	})

	// Message Summarization settings
	form.AddCheckbox("Disable Message Summarization", summarizationDisabled, func(checked bool) {
		summarizationDisabled = checked
	})

	form.AddInputField("Ollama Model", summarizationModel, 30, nil, func(text string) {
		summarizationModel = text
	})

	form.AddInputField("Max Characters", summarizationMaxChars, 10, nil, func(text string) {
		summarizationMaxChars = text
	})

	form.AddInputField("Max Lines", summarizationMaxLines, 10, nil, func(text string) {
		summarizationMaxLines = text
	})

	form.AddInputField("Max Line Breaks", summarizationMaxLineBreaks, 10, nil, func(text string) {
		summarizationMaxLineBreaks = text
	})

	// Save button
	form.AddButton("Save", func() {
		// Update config
		cfg.ClaudeMCP.Enabled = claudeEnabled

		// Collect selected projects
		var selectedProjects []string
		if claudeEnabled {
			// Only include selected projects
			for _, proj := range availableProjects {
				if projectSelections[proj] {
					selectedProjects = append(selectedProjects, proj)
				}
			}
			// If no projects selected, leave empty (which means no servers will be loaded)
		} else {
			selectedProjects = nil
		}
		cfg.ClaudeMCP.Projects = selectedProjects

		// Update message summarization settings
		cfg.MessageSummarization.Disabled = summarizationDisabled
		if summarizationModel != "" {
			cfg.MessageSummarization.Model = summarizationModel
		}

		// Parse threshold values
		if maxChars, err := strconv.Atoi(summarizationMaxChars); err == nil && maxChars > 0 {
			cfg.MessageSummarization.MaxChars = maxChars
		} else {
			cfg.MessageSummarization.MaxChars = 2000
		}

		if maxLines, err := strconv.Atoi(summarizationMaxLines); err == nil && maxLines > 0 {
			cfg.MessageSummarization.MaxLines = maxLines
		} else {
			cfg.MessageSummarization.MaxLines = 50
		}

		if maxLineBreaks, err := strconv.Atoi(summarizationMaxLineBreaks); err == nil && maxLineBreaks > 0 {
			cfg.MessageSummarization.MaxLineBreaks = maxLineBreaks
		} else {
			cfg.MessageSummarization.MaxLineBreaks = 10
		}

		// Save config
		if err := config.SaveConfig(cfg, configPath); err != nil {
			logger.Error("Failed to save config: %v", err)
			modal := tview.NewModal().
				SetText(fmt.Sprintf("Error saving config:\n%v\n\nPress Enter to continue.", err)).
				AddButtons([]string{"OK"}).
				SetDoneFunc(func(buttonIndex int, buttonLabel string) {
					a.pages.RemovePage("settings_modal")
				})
			a.pages.AddPage("settings_modal", modal, true, true)
			return
		}

		// Show success message
		modal := tview.NewModal().
			SetText("Settings saved successfully!\n\nPlease restart the application for changes to take effect.").
			AddButtons([]string{"OK"}).
			SetDoneFunc(func(buttonIndex int, buttonLabel string) {
				a.pages.RemovePage("settings_modal")
				a.pages.SwitchToPage("main")
				a.app.SetFocus(a.sidebar)
			})
		a.pages.AddPage("settings_modal", modal, true, true)
	})

	form.AddButton("Cancel", func() {
		a.pages.SwitchToPage("main")
		a.app.SetFocus(a.sidebar)
	})

	// Create layout with form and project list side by side
	settingsFlex := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(form, 0, 1, true).
		AddItem(projectList, 0, 1, false)

	// Handle Esc key
	settingsFlex.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEsc {
			a.pages.SwitchToPage("main")
			a.app.SetFocus(a.sidebar)
			return nil
		}
		return ev
	})

	// Add page
	a.pages.AddPage("settings", settingsFlex, true, false)
	a.pages.SwitchToPage("settings")

	// Let form handle Tab normally for navigation between checkbox and buttons
	// Use Right Arrow or 'P' key to switch to project list from form
	form.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyRight || (ev.Key() == tcell.KeyRune && (ev.Rune() == 'p' || ev.Rune() == 'P')) {
			a.app.SetFocus(projectList)
			return nil
		}
		// Let form handle Tab normally for internal navigation
		return ev
	})

	projectList.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		// Use Left Arrow or 'F' key to switch back to form from project list
		if ev.Key() == tcell.KeyLeft || (ev.Key() == tcell.KeyRune && (ev.Rune() == 'f' || ev.Rune() == 'F')) {
			a.app.SetFocus(form)
			return nil
		}
		// Allow Space or Enter to toggle selection
		if (ev.Key() == tcell.KeyRune && ev.Rune() == ' ') || ev.Key() == tcell.KeyEnter {
			// Toggle current selection
			currentItem := projectList.GetCurrentItem()
			if currentItem >= 0 && currentItem < len(availableProjects) {
				proj := availableProjects[currentItem]
				projectSelections[proj] = !projectSelections[proj]
				updateProjectList()
				projectList.SetCurrentItem(currentItem)
			}
			return nil
		}
		return ev
	})

	a.app.SetFocus(form)
}
