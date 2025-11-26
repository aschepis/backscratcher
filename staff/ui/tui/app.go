package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/aschepis/backscratcher/staff/logger"
	"github.com/aschepis/backscratcher/staff/ui"
	"github.com/aschepis/backscratcher/staff/ui/themes"
)

// App represents the main terminal UI application structure
type App struct {
	app     *tview.Application
	pages   *tview.Pages
	header  *tview.TextView
	content *tview.TextView
	footer  *tview.TextView
	sidebar *tview.List

	chatService ui.ChatService

	// Chat-related fields
	chatHistory map[string][]llm.Message // agentID -> conversation history
	chatMutex   sync.RWMutex             // protects chatHistory

	// Config-related fields
	configPath string
}

// NewApp creates a new App instance with the given chat service
// Uses STAFF_THEME environment variable or defaults to "solarized"
func NewApp(chatService ui.ChatService) *App {
	// Get theme from environment variable, default to "solarized"
	themeName := os.Getenv("STAFF_THEME")
	if themeName == "" {
		themeName = "solarized"
	}
	return NewAppWithTheme(chatService, themeName)
}

// NewAppWithTheme creates a new App instance with the given chat service and theme
func NewAppWithTheme(chatService ui.ChatService, themeName string) *App {
	// Apply theme based on provided theme name
	logger.Info("NewAppWithTheme: themeName=%s", themeName)
	tviewApp := tview.NewApplication()
	err := themes.ApplyByName(tviewApp, themeName)
	if err != nil {
		logger.Error("Failed to apply theme: %v. Continuing with no theme.", err)
	}

	return &App{
		app:         tviewApp,
		pages:       tview.NewPages(),
		chatService: chatService,
		chatHistory: make(map[string][]llm.Message),
	}
}

// SetConfigPath sets the config file path for the app
func (a *App) SetConfigPath(configPath string) {
	a.configPath = configPath
}

// setupUI initializes the UI components and layout
func (a *App) setupUI() {
	// Header
	a.header = tview.NewTextView()
	a.header.SetTextAlign(tview.AlignCenter)
	a.header.SetText("Staff - Terminal UI Application")
	// Header will use theme colors from tview.Styles automatically
	a.header.SetBorder(true)

	// Sidebar
	a.sidebar = tview.NewList().
		AddItem("Inbox", "View your inbox", '1', func() {
			a.showInbox()
		}).
		AddItem("Crew Members", "View all agents", '2', func() {
			a.showCrewMembers()
		}).
		AddItem("Settings", "Configure settings", '3', func() {
			a.showSettings()
		}).
		AddItem("Tools", "Tools", '4', func() {
			a.showTools()
		}).
		AddItem("About", "About this application", '4', func() {
			a.showAbout()
			// Set focus to content view for scrolling
			a.app.SetFocus(a.content)
		}).
		AddItem("Quit", "Exit the application", 'q', func() {
			a.app.Stop()
		})

	a.sidebar.SetBorder(true).SetTitle("Menu")

	// Main content
	a.content = tview.NewTextView()
	a.content.SetDynamicColors(true).
		SetWordWrap(true).
		SetBorder(true).
		SetTitle("Content")
	a.content.SetScrollable(true)

	// Initialize content with a simple welcome message
	// The inbox will be shown when user selects it from the menu
	a.content.SetText("Welcome to Staff\n\nSelect an option from the menu to get started.")

	// Add input capture for scrolling content view
	a.content.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyUp:
			row, col := a.content.GetScrollOffset()
			if row > 0 {
				a.content.ScrollTo(row-1, col)
			}
			return nil
		case tcell.KeyDown:
			row, col := a.content.GetScrollOffset()
			a.content.ScrollTo(row+1, col)
			return nil
		case tcell.KeyPgUp:
			row, col := a.content.GetScrollOffset()
			if row > 10 {
				a.content.ScrollTo(row-10, col)
			} else {
				a.content.ScrollTo(0, col)
			}
			return nil
		case tcell.KeyPgDn:
			row, col := a.content.GetScrollOffset()
			a.content.ScrollTo(row+10, col)
			return nil
		case tcell.KeyHome:
			_, col := a.content.GetScrollOffset()
			a.content.ScrollTo(0, col)
			return nil
		case tcell.KeyEnd:
			// Scroll to end (approximate - scroll to a large number)
			_, col := a.content.GetScrollOffset()
			a.content.ScrollTo(9999, col)
			return nil
		case tcell.KeyEsc, tcell.KeyTab:
			// Return focus to sidebar
			a.app.SetFocus(a.sidebar)
			return nil
		}
		return ev
	})

	// Footer
	a.footer = tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetText("↑/↓: Navigate | Enter: Select | Tab/Esc: Focus sidebar | q: Quit | Ctrl+C: Exit")
	// Footer will use theme colors from tview.Styles automatically

	// Layout
	contentArea := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.header, 3, 0, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexColumn).
			AddItem(a.sidebar, 30, 0, true).
			AddItem(a.content, 0, 1, false), 0, 1, true).
		AddItem(a.footer, 1, 0, false)

	a.pages.AddPage("main", contentArea, true, true)

	// Keybindings
	a.app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyCtrlC {
			a.app.Stop()
			return nil
		}
		return ev
	})
}

func (a *App) showContent(title, text string) {
	a.content.SetTitle(title)
	a.content.SetText(text)
	// Scroll to top when showing new content
	a.content.ScrollToBeginning()
}

func (a *App) updateFooter(text string) {
	a.footer.SetText(text)
}

// showAbout displays system information including LLM provider, MCP servers, and tools
func (a *App) showAbout() {
	ctx := context.Background()
	sysInfo, err := a.chatService.GetSystemInfo(ctx)
	if err != nil {
		a.showContent("About", fmt.Sprintf("Staff v1.0.0\n\nError loading system information: %v", err))
		return
	}

	var sb strings.Builder
	sb.WriteString("Staff v1.0.0\n\n")
	sb.WriteString("An idiomatic Go terminal UI application using tview.\n")
	sb.WriteString("Powered by the Agent Crew framework.\n\n")

	// LLM Provider
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString("LLM Provider\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString(fmt.Sprintf("Provider: %s\n\n", sysInfo.LLMProvider))

	// MCP Servers
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString("MCP Servers\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	if len(sysInfo.MCPServers) == 0 {
		sb.WriteString("No MCP servers configured.\n\n")
	} else {
		for _, server := range sysInfo.MCPServers {
			status := "Disabled"
			if server.Enabled {
				status = "Active"
			}
			sb.WriteString(fmt.Sprintf("• %s (%s)\n", server.Name, status))
			if len(server.Tools) > 0 {
				sb.WriteString(fmt.Sprintf("  Tools (%d): %s\n", len(server.Tools), strings.Join(server.Tools, ", ")))
			} else {
				sb.WriteString("  No tools available\n")
			}
		}
		sb.WriteString("\n")
	}

	// Tools
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString("Available Tools\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	if len(sysInfo.Tools) == 0 {
		sb.WriteString("No tools available.\n")
	} else {
		// Group tools by server
		nativeTools := make([]ui.ToolInfo, 0)
		serverTools := make(map[string][]ui.ToolInfo)
		for _, tool := range sysInfo.Tools {
			if tool.Server == "" {
				nativeTools = append(nativeTools, tool)
			} else {
				serverTools[tool.Server] = append(serverTools[tool.Server], tool)
			}
		}

		// Show native tools first
		if len(nativeTools) > 0 {
			sb.WriteString("Native Tools:\n")
			for _, tool := range nativeTools {
				sb.WriteString(fmt.Sprintf("  • %s", tool.Name))
				if tool.Description != "" {
					sb.WriteString(fmt.Sprintf(" - %s", tool.Description))
				}
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		}

		// Show MCP tools grouped by server
		for serverName, tools := range serverTools {
			sb.WriteString(fmt.Sprintf("MCP Server '%s' Tools:\n", serverName))
			for _, tool := range tools {
				sb.WriteString(fmt.Sprintf("  • %s", tool.Name))
				if tool.Description != "" {
					sb.WriteString(fmt.Sprintf(" - %s", tool.Description))
				}
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		}

		sb.WriteString(fmt.Sprintf("Total: %d tools\n", len(sysInfo.Tools)))
	}

	a.showContent("About", sb.String())
	// Update footer with scrolling instructions
	a.updateFooter("↑/↓: Scroll | PgUp/PgDn: Page | Home/End: Top/Bottom | Tab/Esc: Back to menu | q: Quit")
}

// Run starts the application
func (a *App) Run() error {
	a.setupUI()
	return a.app.SetRoot(a.pages, true).SetFocus(a.sidebar).Run()
}
