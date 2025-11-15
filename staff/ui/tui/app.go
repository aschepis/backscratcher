package tui

import (
	"os"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

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
	chatHistory map[string][]anthropic.MessageParam // agentID -> conversation history
	chatMutex   sync.RWMutex                        // protects chatHistory
}

// NewApp creates a new App instance with the given chat service
func NewApp(chatService ui.ChatService) *App {
	// Get theme from environment variable, default to "solarized"
	themeName := os.Getenv("STAFF_THEME")
	if themeName == "" {
		themeName = "solarized"
	}

	// Apply theme based on environment variable
	theme := getThemeByName(themeName)
	theme.Apply(nil)

	return &App{
		app:         tview.NewApplication(),
		pages:       tview.NewPages(),
		chatService: chatService,
		chatHistory: make(map[string][]anthropic.MessageParam),
	}
}

// getThemeByName returns a theme by name, defaulting to Solarized if invalid
func getThemeByName(themeName string) *themes.Theme {
	switch themeName {
	case "solarized":
		return themes.NewSolarized()
	case "gruvbox":
		return themes.NewGruvbox()
	case "zenburn":
		return themes.NewZenburn()
	case "apprentice":
		return themes.NewApprentice()
	case "cyberpunk":
		return themes.NewCyberpunk()
	case "cherryblossom":
		return themes.NewCherryBlossom()
	default:
		return themes.NewSolarized()
	}
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
			a.showContent("Settings", "Settings\n\nTheme can be changed by setting the STAFF_THEME environment variable.\n\nAvailable themes: solarized, gruvbox, zenburn, apprentice, cyberpunk, cherryblossom\n\nRestart the application for theme changes to take effect.")
		}).
		AddItem("About", "About this application", '4', func() {
			a.showContent("About", "Staff v1.0.0\n\nAn idiomatic Go terminal UI application using tview.\n\nPowered by the Agent Crew framework.")
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

	a.showInbox()

	// Footer
	a.footer = tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetText("↑/↓: Navigate | Enter: Select | q: Quit | Ctrl+C: Exit")
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
}

// Run starts the application
func (a *App) Run() error {
	a.setupUI()
	return a.app.SetRoot(a.pages, true).SetFocus(a.sidebar).Run()
}
