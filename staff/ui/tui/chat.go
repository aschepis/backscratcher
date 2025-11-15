package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (a *App) showChat(agentID string) {
	agents := a.chatService.ListAgents()
	var agentName string
	for _, ag := range agents {
		if ag.ID == agentID {
			agentName = ag.Name
			break
		}
	}

	// Initialize chat history if needed
	a.chatMutex.Lock()
	if _, exists := a.chatHistory[agentID]; !exists {
		a.chatHistory[agentID] = []anthropic.MessageParam{}
	}
	a.chatMutex.Unlock()

	// Chat history display
	chatDisplay := tview.NewTextView()
	chatDisplay.SetDynamicColors(true).
		SetWordWrap(true).
		SetBorder(true).
		SetTitle(fmt.Sprintf("Chat with %s (Esc to go back)", agentName))
	chatDisplay.SetScrollable(true)

	// Display existing chat history
	a.updateChatDisplay(chatDisplay, agentID, agentName)

	// Input field
	inputField := tview.NewInputField()
	inputField.SetLabel("You: ").
		SetFieldWidth(0).
		SetBorder(true).
		SetTitle("Message (Enter to send, Esc to go back)")

	// Handle input submission - use a helper function to avoid blocking
	inputField.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			message := strings.TrimSpace(inputField.GetText())
			if message == "" {
				return
			}

			// Clear input immediately - this must be fast and non-blocking
			inputField.SetText("")

			// Launch message handling in background immediately
			// This ensures the input handler returns immediately
			go a.handleChatMessage(agentID, agentName, message, chatDisplay)
		}
	})

	// Layout: chat display on top, input at bottom
	chatLayout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(chatDisplay, 0, 1, false).
		AddItem(inputField, 3, 0, true)

	// Add back button functionality
	chatLayout.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEsc {
			a.pages.SwitchToPage("main")
			a.app.SetFocus(a.sidebar)
			return nil
		}
		return ev
	})

	// Create a unique page name for this chat
	pageName := fmt.Sprintf("chat_%s", agentID)
	a.pages.AddPage(pageName, chatLayout, true, false)
	a.pages.SwitchToPage(pageName)
	a.app.SetFocus(inputField)
}

func (a *App) updateChatDisplay(chatDisplay *tview.TextView, agentID string, agentName string) {
	chatDisplay.Clear()

	a.chatMutex.RLock()
	historyLen := len(a.chatHistory[agentID])
	a.chatMutex.RUnlock()

	// Display conversation history
	// Messages are displayed as they're sent during streaming
	// For initial display, show a welcome message if history is empty
	if historyLen == 0 {
		fmt.Fprintf(chatDisplay, "[gray]Start a conversation with %s...[white]\n\n", agentName)
	}
	// Note: History is already displayed during streaming, so we don't need to reconstruct it here
	// If you want to persist and reload history, you'd need to implement proper message parsing
}

// handleChatMessage processes a chat message in the background
func (a *App) handleChatMessage(agentID, agentName, message string, chatDisplay *tview.TextView) {
	// Update UI to show user message and thinking indicator
	a.app.QueueUpdateDraw(func() {
		fmt.Fprintf(chatDisplay, "[cyan]You[white]: %s\n\n", message)
		fmt.Fprintf(chatDisplay, "[yellow]%s is thinking...[white]\n", agentName)
		chatDisplay.ScrollToEnd()
	})

	// Use a shorter timeout for better UX - 60 seconds should be plenty
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	threadID := fmt.Sprintf("chat-%s", agentID)

	// Get conversation history
	a.chatMutex.RLock()
	history := a.chatHistory[agentID]
	a.chatMutex.RUnlock()

	// Track if we've started showing the response (shared with callback and error handler)
	responseStartedMu := &sync.Mutex{}
	responseStarted := false

	// Create callback for streaming updates
	streamCallback := func(text string) error {
		if text == "" {
			return nil
		}

		// Queue update on main thread - this is non-blocking
		a.app.QueueUpdateDraw(func() {
			responseStartedMu.Lock()
			isFirstUpdate := !responseStarted
			if isFirstUpdate {
				responseStarted = true
			}
			responseStartedMu.Unlock()

			// Remove "thinking" message if this is the first update
			if isFirstUpdate {
				// Get current text and remove thinking message
				currentText := chatDisplay.GetText(false)
				if strings.Contains(currentText, "thinking") {
					lines := strings.Split(currentText, "\n")
					newLines := make([]string, 0, len(lines))
					for _, line := range lines {
						if !strings.Contains(line, "thinking") {
							newLines = append(newLines, line)
						}
					}
					chatDisplay.Clear()
					if len(newLines) > 0 {
						chatDisplay.SetText(strings.Join(newLines, "\n"))
					}
				}
				// Start the agent response line
				fmt.Fprintf(chatDisplay, "[green]%s[white]: ", agentName)
			}

			// Append text directly to the display - this is efficient
			fmt.Fprint(chatDisplay, text)
			chatDisplay.ScrollToEnd()
		})
		return nil
	}

	// Create debug callback for showing tool invocations, API calls, etc.
	debugCallback := func(debugMsg string) {
		// Display debug info in dark gray
		a.app.QueueUpdateDraw(func() {
			fmt.Fprintf(chatDisplay, "[gray]DEBUG: %s[white]\n", debugMsg)
			chatDisplay.ScrollToEnd()
		})
	}

	// Run agent with streaming using the chat service
	response, err := a.chatService.SendMessageStream(ctx, agentID, threadID, message, history, streamCallback, debugCallback)

	// Update UI in main thread
	a.app.QueueUpdateDraw(func() {
		responseStartedMu.Lock()
		started := responseStarted
		responseStartedMu.Unlock()

		if err != nil {
			// Remove thinking message and show error
			if !started {
				currentText := chatDisplay.GetText(false)
				lines := strings.Split(currentText, "\n")
				newLines := []string{}
				for _, line := range lines {
					if !strings.Contains(line, "thinking") {
						newLines = append(newLines, line)
					}
				}
				chatDisplay.Clear()
				if len(newLines) > 0 {
					chatDisplay.SetText(strings.Join(newLines, "\n"))
				}
				fmt.Fprintf(chatDisplay, "[red]Error[white]: %v\n\n", err)
			} else {
				fmt.Fprintf(chatDisplay, "\n\n[red]Error[white]: %v\n\n", err)
			}
			chatDisplay.ScrollToEnd()
		} else {
			// Finalize the response with a newline
			if started {
				fmt.Fprint(chatDisplay, "\n\n")
			}

			// Update history
			a.chatMutex.Lock()
			a.chatHistory[agentID] = append(a.chatHistory[agentID],
				anthropic.NewUserMessage(anthropic.NewTextBlock(message)),
			)
			a.chatHistory[agentID] = append(a.chatHistory[agentID],
				anthropic.NewAssistantMessage(anthropic.NewTextBlock(response)),
			)
			a.chatMutex.Unlock()

			// Ensure we're scrolled to the end
			chatDisplay.ScrollToEnd()
		}
	})
}

