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

	// Get or create thread ID for this agent
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	threadID, err := a.chatService.GetOrCreateThreadID(ctx, agentID)
	if err != nil {
		// If we can't get/create thread ID, use a fallback
		threadID = fmt.Sprintf("chat-%s-%d", agentID, time.Now().Unix())
	}

	// Load existing conversation history from database
	history, err := a.chatService.LoadConversationHistory(ctx, agentID, threadID)
	if err != nil {
		// Log error but continue with empty history
		history = []anthropic.MessageParam{}
	}

	// Initialize chat history in memory from database
	a.chatMutex.Lock()
	a.chatHistory[agentID] = history
	a.chatMutex.Unlock()

	// Chat history display
	chatDisplay := tview.NewTextView()
	chatDisplay.SetDynamicColors(true).
		SetWordWrap(true).
		SetBorder(true).
		SetTitle(fmt.Sprintf("Chat with %s (Esc to go back, Tab to focus input)", agentName))
	chatDisplay.SetScrollable(true)

	// Display existing chat history
	a.updateChatDisplay(chatDisplay, agentID, agentName, threadID)

	// Input field
	inputField := tview.NewInputField()
	inputField.SetLabel("You: ").
		SetFieldWidth(0).
		SetBorder(true).
		SetTitle("Message (Enter to send, Tab to scroll chat, Esc to go back)")

	// Add input capture to chat display for arrow key scrolling
	// Must be after inputField is declared so we can reference it
	chatDisplay.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyUp:
			// Scroll up
			row, col := chatDisplay.GetScrollOffset()
			if row > 0 {
				chatDisplay.ScrollTo(row-1, col)
			}
			return nil
		case tcell.KeyDown:
			// Scroll down
			row, col := chatDisplay.GetScrollOffset()
			chatDisplay.ScrollTo(row+1, col)
			return nil
		case tcell.KeyPgUp:
			// Page up - scroll by visible height
			row, col := chatDisplay.GetScrollOffset()
			_, _, _, height := chatDisplay.GetInnerRect()
			newRow := row - height
			if newRow < 0 {
				newRow = 0
			}
			chatDisplay.ScrollTo(newRow, col)
			return nil
		case tcell.KeyPgDn:
			// Page down - scroll by visible height
			row, col := chatDisplay.GetScrollOffset()
			_, _, _, height := chatDisplay.GetInnerRect()
			chatDisplay.ScrollTo(row+height, col)
			return nil
		case tcell.KeyHome:
			// Scroll to top
			_, col := chatDisplay.GetScrollOffset()
			chatDisplay.ScrollTo(0, col)
			return nil
		case tcell.KeyEnd:
			// Scroll to end (latest messages)
			chatDisplay.ScrollToEnd()
			return nil
		case tcell.KeyTab:
			// Switch focus to input field
			a.app.SetFocus(inputField)
			return nil
		case tcell.KeyEsc:
			// Go back to main menu
			a.pages.SwitchToPage("main")
			a.app.SetFocus(a.sidebar)
			return nil
		}
		return ev
	})

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

	// Add input capture to input field to handle Tab for switching focus and Esc to exit
	inputField.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyTab:
			// Switch focus to chat display
			a.app.SetFocus(chatDisplay)
			return nil
		case tcell.KeyEsc:
			// Go back to main menu
			a.pages.SwitchToPage("main")
			a.app.SetFocus(a.sidebar)
			return nil
		}
		return ev
	})

	// Layout: chat display on top, input at bottom
	chatLayout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(chatDisplay, 0, 1, false).
		AddItem(inputField, 3, 0, true)

	// Note: Esc and Tab are now handled by individual components
	// This handler only handles layout-level keys if needed
	chatLayout.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		// Let individual components handle their own keys
		return ev
	})

	// Create a unique page name for this chat
	pageName := fmt.Sprintf("chat_%s", agentID)
	a.pages.AddPage(pageName, chatLayout, true, false)
	a.pages.SwitchToPage(pageName)
	a.app.SetFocus(inputField)
}

func (a *App) updateChatDisplay(chatDisplay *tview.TextView, agentID string, agentName string, threadID string) {
	chatDisplay.Clear()

	a.chatMutex.RLock()
	history := a.chatHistory[agentID]
	a.chatMutex.RUnlock()

	// Display conversation history from database
	if len(history) == 0 {
		fmt.Fprintf(chatDisplay, "[gray]Start a conversation with %s...[white]\n\n", agentName)
	} else {
		// Reconstruct and display the conversation history
		for _, msg := range history {
			var textBuilder strings.Builder

			// Extract text from message content blocks
			for _, blockUnion := range msg.Content {
				// Check if this is a text block
				if blockUnion.OfText != nil {
					textBuilder.WriteString(blockUnion.OfText.Text)
				}
			}

			text := strings.TrimSpace(textBuilder.String())
			if text == "" {
				continue // Skip empty messages
			}

			// Display based on role
			if msg.Role == "user" {
				fmt.Fprintf(chatDisplay, "[cyan]You[white]: %s\n\n", text)
			} else if msg.Role == "assistant" {
				fmt.Fprintf(chatDisplay, "[green]%s[white]: %s\n\n", agentName, text)
			}
		}
		chatDisplay.ScrollToEnd()
	}
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

	// Get or create thread ID for this agent
	threadID, err := a.chatService.GetOrCreateThreadID(ctx, agentID)
	if err != nil {
		// Fallback if we can't get/create thread ID
		threadID = fmt.Sprintf("chat-%s-%d", agentID, time.Now().Unix())
	}

	// Save user message to database
	if saveErr := a.chatService.SaveMessage(ctx, agentID, threadID, "user", message); saveErr != nil {
		// Log error but continue - don't block the chat
		// Error is silently ignored to keep chat functional
	}

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

			// Save assistant response to database
			if saveErr := a.chatService.SaveMessage(ctx, agentID, threadID, "assistant", response); saveErr != nil {
				// Log error but continue - don't block the chat
				// Error is silently ignored to keep chat functional
			}

			// Update history in memory
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
