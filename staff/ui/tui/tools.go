package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/aschepis/backscratcher/staff/logger"
)

// showTools displays the tools UI
func (a *App) showTools() {
	// Create a list for tool categories
	toolsList := tview.NewList()
	toolsList.SetBorder(true).SetTitle("Tools (Enter: Select, Esc: Back)")

	// Memory category
	toolsList.AddItem("Memory", "Memory operations", 'm', nil)
	toolsList.AddItem("  → Dump Memory", "Write all memory to file", 'd', func() {
		a.showDumpMemoryDialog()
	})
	toolsList.AddItem("  → Clear Memory", "Delete all memory items", 'c', func() {
		a.showClearMemoryDialog()
	})

	// Chat category
	toolsList.AddItem("Chat", "Conversation operations", 'h', nil)
	toolsList.AddItem("  → Dump Conversations", "Write conversations to files (one per agent)", 'd', func() {
		a.showDumpConversationsDialog()
	})
	toolsList.AddItem("  → Clear Conversations", "Delete all conversations", 'c', func() {
		a.showClearConversationsDialog()
	})

	// Stats category
	toolsList.AddItem("Stats", "Statistics operations", 's', nil)
	toolsList.AddItem("  → Reset Stats", "Reset all agent statistics", 'r', func() {
		a.showResetStatsDialog()
	})

	// Inbox category
	toolsList.AddItem("Inbox", "Inbox operations", 'i', nil)
	toolsList.AddItem("  → Dump Inbox", "Write all inbox items to file", 'd', func() {
		a.showDumpInboxDialog()
	})
	toolsList.AddItem("  → Clear Inbox", "Delete all inbox items", 'c', func() {
		a.showClearInboxDialog()
	})

	toolsList.AddItem("", "", ' ', nil) // Separator
	toolsList.AddItem("Back", "Return to main menu", 'b', func() {
		a.pages.SwitchToPage("main")
		a.app.SetFocus(a.sidebar)
	})

	// Handle Esc key
	toolsList.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEsc {
			a.pages.SwitchToPage("main")
			a.app.SetFocus(a.sidebar)
			return nil
		}
		return ev
	})

	// Create page for tools
	a.pages.AddPage("tools", toolsList, true, false)
	a.pages.SwitchToPage("tools")
	a.app.SetFocus(toolsList)
}

// showDumpMemoryDialog shows a dialog to get file path and dump memory
func (a *App) showDumpMemoryDialog() {
	form := tview.NewForm()
	form.SetBorder(true).SetTitle("Dump Memory")

	filePath := fmt.Sprintf("memory_dump_%s.json", time.Now().Format("20060102_150405"))
	form.AddInputField("File Path", filePath, 50, nil, func(text string) {
		filePath = text
	})

	form.AddButton("Dump", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Expand ~ to home directory
		if filePath[0] == '~' {
			homeDir, err := os.UserHomeDir()
			if err == nil {
				filePath = filepath.Join(homeDir, filePath[1:])
			}
		}

		// Ensure directory exists
		dir := filepath.Dir(filePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			a.showErrorModal("Dump Memory", fmt.Sprintf("Failed to create directory: %v", err))
			return
		}

		err := a.chatService.DumpMemory(ctx, filePath)
		if err != nil {
			a.showErrorModal("Dump Memory", fmt.Sprintf("Failed to dump memory: %v", err))
			return
		}

		modal := tview.NewModal().
			SetText(fmt.Sprintf("Memory dumped successfully to:\n%s", filePath)).
			AddButtons([]string{"OK"}).
			SetDoneFunc(func(buttonIndex int, buttonLabel string) {
				a.pages.RemovePage("dump_memory_modal")
				a.pages.SwitchToPage("tools")
			})
		a.pages.AddPage("dump_memory_modal", modal, true, true)
	})

	form.AddButton("Cancel", func() {
		a.pages.RemovePage("dump_memory_form")
		a.pages.SwitchToPage("tools")
	})

	form.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEsc {
			a.pages.RemovePage("dump_memory_form")
			a.pages.SwitchToPage("tools")
			return nil
		}
		return ev
	})

	a.pages.AddPage("dump_memory_form", form, true, true)
	a.app.SetFocus(form)
}

// showClearMemoryDialog shows a confirmation dialog before clearing memory
func (a *App) showClearMemoryDialog() {
	modal := tview.NewModal().
		SetText("Are you sure you want to clear ALL memory?\n\nThis action cannot be undone!").
		AddButtons([]string{"Yes, Clear", "Cancel"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			a.pages.RemovePage("clear_memory_modal")
			if buttonLabel == "Yes, Clear" {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()

				err := a.chatService.ClearMemory(ctx)
				if err != nil {
					a.showErrorModal("Clear Memory", fmt.Sprintf("Failed to clear memory: %v", err))
					return
				}

				successModal := tview.NewModal().
					SetText("Memory cleared successfully.").
					AddButtons([]string{"OK"}).
					SetDoneFunc(func(buttonIndex int, buttonLabel string) {
						a.pages.RemovePage("clear_memory_success")
						a.pages.SwitchToPage("tools")
					})
				a.pages.AddPage("clear_memory_success", successModal, true, true)
			} else {
				a.pages.SwitchToPage("tools")
			}
		})
	a.pages.AddPage("clear_memory_modal", modal, true, true)
}

// showDumpConversationsDialog shows a dialog to get output directory and dump conversations
func (a *App) showDumpConversationsDialog() {
	form := tview.NewForm()
	form.SetBorder(true).SetTitle("Dump Conversations")

	outputDir := fmt.Sprintf("conversations_dump_%s", time.Now().Format("20060102_150405"))
	form.AddInputField("Output Directory", outputDir, 50, nil, func(text string) {
		outputDir = text
	})

	form.AddButton("Dump", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		// Expand ~ to home directory
		if len(outputDir) > 0 && outputDir[0] == '~' {
			homeDir, err := os.UserHomeDir()
			if err == nil {
				outputDir = filepath.Join(homeDir, outputDir[1:])
			}
		}

		err := a.chatService.DumpConversations(ctx, outputDir)
		if err != nil {
			a.showErrorModal("Dump Conversations", fmt.Sprintf("Failed to dump conversations: %v", err))
			return
		}

		modal := tview.NewModal().
			SetText(fmt.Sprintf("Conversations dumped successfully to:\n%s", outputDir)).
			AddButtons([]string{"OK"}).
			SetDoneFunc(func(buttonIndex int, buttonLabel string) {
				a.pages.RemovePage("dump_conversations_modal")
				a.pages.SwitchToPage("tools")
			})
		a.pages.AddPage("dump_conversations_modal", modal, true, true)
	})

	form.AddButton("Cancel", func() {
		a.pages.RemovePage("dump_conversations_form")
		a.pages.SwitchToPage("tools")
	})

	form.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEsc {
			a.pages.RemovePage("dump_conversations_form")
			a.pages.SwitchToPage("tools")
			return nil
		}
		return ev
	})

	a.pages.AddPage("dump_conversations_form", form, true, true)
	a.app.SetFocus(form)
}

// showClearConversationsDialog shows a confirmation dialog before clearing conversations
func (a *App) showClearConversationsDialog() {
	modal := tview.NewModal().
		SetText("Are you sure you want to clear ALL conversations?\n\nThis action cannot be undone!").
		AddButtons([]string{"Yes, Clear", "Cancel"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			a.pages.RemovePage("clear_conversations_modal")
			if buttonLabel == "Yes, Clear" {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()

				err := a.chatService.ClearConversations(ctx)
				if err != nil {
					a.showErrorModal("Clear Conversations", fmt.Sprintf("Failed to clear conversations: %v", err))
					return
				}

				successModal := tview.NewModal().
					SetText("Conversations cleared successfully.").
					AddButtons([]string{"OK"}).
					SetDoneFunc(func(buttonIndex int, buttonLabel string) {
						a.pages.RemovePage("clear_conversations_success")
						a.pages.SwitchToPage("tools")
					})
				a.pages.AddPage("clear_conversations_success", successModal, true, true)
			} else {
				a.pages.SwitchToPage("tools")
			}
		})
	a.pages.AddPage("clear_conversations_modal", modal, true, true)
}

// showResetStatsDialog shows a confirmation dialog before resetting stats
func (a *App) showResetStatsDialog() {
	modal := tview.NewModal().
		SetText("Are you sure you want to reset ALL agent statistics?\n\nThis action cannot be undone!").
		AddButtons([]string{"Yes, Reset", "Cancel"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			a.pages.RemovePage("reset_stats_modal")
			if buttonLabel == "Yes, Reset" {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()

				err := a.chatService.ResetStats(ctx)
				if err != nil {
					a.showErrorModal("Reset Stats", fmt.Sprintf("Failed to reset stats: %v", err))
					return
				}

				successModal := tview.NewModal().
					SetText("Stats reset successfully.").
					AddButtons([]string{"OK"}).
					SetDoneFunc(func(buttonIndex int, buttonLabel string) {
						a.pages.RemovePage("reset_stats_success")
						a.pages.SwitchToPage("tools")
					})
				a.pages.AddPage("reset_stats_success", successModal, true, true)
			} else {
				a.pages.SwitchToPage("tools")
			}
		})
	a.pages.AddPage("reset_stats_modal", modal, true, true)
}

// showDumpInboxDialog shows a dialog to get file path and dump inbox
func (a *App) showDumpInboxDialog() {
	form := tview.NewForm()
	form.SetBorder(true).SetTitle("Dump Inbox")

	filePath := fmt.Sprintf("inbox_dump_%s.json", time.Now().Format("20060102_150405"))
	form.AddInputField("File Path", filePath, 50, nil, func(text string) {
		filePath = text
	})

	form.AddButton("Dump", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Expand ~ to home directory
		if len(filePath) > 0 && filePath[0] == '~' {
			homeDir, err := os.UserHomeDir()
			if err == nil {
				filePath = filepath.Join(homeDir, filePath[1:])
			}
		}

		// Ensure directory exists
		dir := filepath.Dir(filePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			a.showErrorModal("Dump Inbox", fmt.Sprintf("Failed to create directory: %v", err))
			return
		}

		err := a.chatService.DumpInbox(ctx, filePath)
		if err != nil {
			a.showErrorModal("Dump Inbox", fmt.Sprintf("Failed to dump inbox: %v", err))
			return
		}

		modal := tview.NewModal().
			SetText(fmt.Sprintf("Inbox dumped successfully to:\n%s", filePath)).
			AddButtons([]string{"OK"}).
			SetDoneFunc(func(buttonIndex int, buttonLabel string) {
				a.pages.RemovePage("dump_inbox_modal")
				a.pages.SwitchToPage("tools")
			})
		a.pages.AddPage("dump_inbox_modal", modal, true, true)
	})

	form.AddButton("Cancel", func() {
		a.pages.RemovePage("dump_inbox_form")
		a.pages.SwitchToPage("tools")
	})

	form.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEsc {
			a.pages.RemovePage("dump_inbox_form")
			a.pages.SwitchToPage("tools")
			return nil
		}
		return ev
	})

	a.pages.AddPage("dump_inbox_form", form, true, true)
	a.app.SetFocus(form)
}

// showClearInboxDialog shows a confirmation dialog before clearing inbox
func (a *App) showClearInboxDialog() {
	modal := tview.NewModal().
		SetText("Are you sure you want to clear ALL inbox items?\n\nThis action cannot be undone!").
		AddButtons([]string{"Yes, Clear", "Cancel"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			a.pages.RemovePage("clear_inbox_modal")
			if buttonLabel == "Yes, Clear" {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()

				err := a.chatService.ClearInbox(ctx)
				if err != nil {
					a.showErrorModal("Clear Inbox", fmt.Sprintf("Failed to clear inbox: %v", err))
					return
				}

				successModal := tview.NewModal().
					SetText("Inbox cleared successfully.").
					AddButtons([]string{"OK"}).
					SetDoneFunc(func(buttonIndex int, buttonLabel string) {
						a.pages.RemovePage("clear_inbox_success")
						a.pages.SwitchToPage("tools")
						if toolsPage := a.pages.GetPage("tools"); toolsPage != nil {
							a.app.SetFocus(toolsPage.(*tview.List))
						}
					})
				a.pages.AddPage("clear_inbox_success", successModal, true, true)
			} else {
				a.pages.SwitchToPage("tools")
			}
		})
	a.pages.AddPage("clear_inbox_modal", modal, true, true)
}

// showErrorModal displays an error message in a modal
func (a *App) showErrorModal(title, message string) {
	modal := tview.NewModal().
		SetText(fmt.Sprintf("%s\n\n%s", title, message)).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			// Remove the error modal
			pageName := fmt.Sprintf("error_modal_%s", title)
			a.pages.RemovePage(pageName)
		})
	pageName := fmt.Sprintf("error_modal_%s", title)
	a.pages.AddPage(pageName, modal, true, true)
	logger.Error("%s: %s", title, message)
}
