package tui

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// urlRegex matches URLs in text for hyperlink formatting
var urlRegex = regexp.MustCompile(`https?://[^\s\[\]<>]+`)

// formatURLsAsHyperlinks converts URLs in text to OSC 8 terminal hyperlinks
// This makes URLs clickable in supported terminals (iTerm2, GNOME Terminal, Windows Terminal, etc.)
// Display format: [Link to domain.tld]
func formatURLsAsHyperlinks(text string) string {
	return urlRegex.ReplaceAllStringFunc(text, func(rawURL string) string {
		// Extract domain from URL
		parsedURL, err := url.Parse(rawURL)
		if err != nil {
			// Fallback to showing full URL if parsing fails
			return fmt.Sprintf("\033]8;;%s\007%s\033]8;;\007", rawURL, rawURL)
		}

		domain := parsedURL.Host
		displayText := fmt.Sprintf("[Link to %s]", domain)

		// OSC 8 hyperlink format: ESC ] 8 ; ; URL BEL text ESC ] 8 ; ; BEL
		// Using \033 for ESC and \007 for BEL
		return fmt.Sprintf("\033]8;;%s\007%s\033]8;;\007", rawURL, displayText)
	})
}

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
		SetTitle(fmt.Sprintf("Chat with %s (Esc: back, Tab: focus input, /reset: reset context, /compress: compress context, exit: leave)", agentName))
	chatDisplay.SetScrollable(true)

	// Display existing chat history
	a.updateChatDisplay(chatDisplay, agentID, agentName, threadID)

	// Input field
	inputField := tview.NewInputField()
	inputField.SetLabel("You: ").
		SetFieldWidth(0).
		SetBorder(true).
		SetTitle("Message (Enter: send, Tab: scroll chat, /reset: reset context, /compress: compress context, exit: leave, Esc: back)")

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

			// Check if user wants to exit the chat
			if strings.EqualFold(message, "exit") {
				inputField.SetText("")
				a.pages.SwitchToPage("main")
				a.app.SetFocus(a.sidebar)
				return
			}

			// Check for special commands
			if strings.HasPrefix(message, "/") {
				command := strings.ToLower(strings.TrimSpace(message))
				inputField.SetText("")

				switch command {
				case "/reset":
					go a.handleResetContext(agentID, agentName, threadID, chatDisplay)
					return
				case "/compress":
					go a.handleCompressContext(agentID, agentName, threadID, chatDisplay)
					return
				default:
					// Unknown command - show error and don't send
					a.app.QueueUpdateDraw(func() {
						_, _ = fmt.Fprintf(chatDisplay, "[red]Unknown command: %s[white]\n", message)
						_, _ = fmt.Fprintf(chatDisplay, "[gray]Available commands: /reset, /compress, exit[white]\n\n")
						chatDisplay.ScrollToEnd()
					})
					return
				}
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

func (a *App) updateChatDisplay(chatDisplay *tview.TextView, agentID, agentName, threadID string) {
	chatDisplay.Clear()

	// Get thread ID if not provided
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if threadID == "" {
		var err error
		threadID, err = a.chatService.GetOrCreateThreadID(ctx, agentID)
		if err != nil {
			threadID = fmt.Sprintf("chat-%s-%d", agentID, time.Now().Unix())
		}
	}

	// Load conversation history
	history, err := a.chatService.LoadConversationHistory(ctx, agentID, threadID)
	if err != nil {
		history = []anthropic.MessageParam{}
	}

	// Load system messages (context breaks)
	systemMessages, err := a.chatService.LoadSystemMessages(ctx, agentID, threadID)
	if err != nil {
		systemMessages = []map[string]interface{}{}
	}

	// Display conversation history from database
	if len(history) == 0 && len(systemMessages) == 0 {
		_, _ = fmt.Fprintf(chatDisplay, "[gray]Start a conversation with %s...[white]\n\n", agentName)
	} else {
		// Create a combined list of all messages with timestamps for sorting
		type messageItem struct {
			timestamp int64
			isSystem  bool
			msg       anthropic.MessageParam
			sysMsg    map[string]interface{}
		}

		var allMessages []messageItem

		// Load regular messages with their actual timestamps
		regularMsgsWithTimestamps, err := a.chatService.LoadMessagesWithTimestamps(ctx, agentID, threadID)
		if err != nil {
			// Fallback: use history without timestamps (will sort by order)
			for i, msg := range history {
				allMessages = append(allMessages, messageItem{
					timestamp: int64(i) * 1000,
					isSystem:  false,
					msg:       msg,
				})
			}
		} else {
			// Add regular messages with their actual timestamps
			for _, msgWithTs := range regularMsgsWithTimestamps {
				allMessages = append(allMessages, messageItem{
					timestamp: msgWithTs.Timestamp,
					isSystem:  false,
					msg:       msgWithTs.Message,
				})
			}
		}

		// Add system messages with their actual timestamps
		for _, sysMsg := range systemMessages {
			var ts int64
			switch tsVal := sysMsg["timestamp"].(type) {
			case int64:
				ts = tsVal
			case float64:
				ts = int64(tsVal)
			}
			allMessages = append(allMessages, messageItem{
				timestamp: ts,
				isSystem:  true,
				sysMsg:    sysMsg,
			})
		}

		// Sort by timestamp
		sort.Slice(allMessages, func(i, j int) bool {
			return allMessages[i].timestamp < allMessages[j].timestamp
		})

		// Render all messages in chronological order
		for _, item := range allMessages {
			if item.isSystem {
				// Render system message
				msgType, _ := item.sysMsg["type"].(string)
				message, _ := item.sysMsg["message"].(string)

				// Display with visual separator based on type
				switch msgType {
				case "reset":
					_, _ = fmt.Fprintf(chatDisplay, "[yellow]━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━[white]\n")
					_, _ = fmt.Fprintf(chatDisplay, "[yellow]  ⚠ Context Reset[white]\n")
					_, _ = fmt.Fprintf(chatDisplay, "[yellow]━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━[white]\n\n")
				case "compress":
					originalSize, _ := item.sysMsg["original_size"].(float64)
					compressedSize, _ := item.sysMsg["compressed_size"].(float64)
					_, _ = fmt.Fprintf(chatDisplay, "[magenta]━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━[white]\n")
					_, _ = fmt.Fprintf(chatDisplay, "[magenta]  📦 Context Compressed[white]\n")
					if originalSize > 0 && compressedSize > 0 {
						_, _ = fmt.Fprintf(chatDisplay, "[magenta]  Size: %.0f → %.0f chars[white]\n", originalSize, compressedSize)
					}
					// Extract summary from message if it contains "Context compressed: "
					if strings.HasPrefix(message, "Context compressed: ") {
						summary := strings.TrimPrefix(message, "Context compressed: ")
						_, _ = fmt.Fprintf(chatDisplay, "[magenta]  Summary: %s[white]\n", summary)
					}
					_, _ = fmt.Fprintf(chatDisplay, "[magenta]━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━[white]\n\n")
				default:
					_, _ = fmt.Fprintf(chatDisplay, "[gray]━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━[white]\n")
					_, _ = fmt.Fprintf(chatDisplay, "[gray]  System: %s[white]\n", message)
					_, _ = fmt.Fprintf(chatDisplay, "[gray]━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━[white]\n\n")
				}
			} else {
				// Render regular message
				var textBuilder strings.Builder

				// Extract text from message content blocks
				for _, blockUnion := range item.msg.Content {
					// Check if this is a text block
					if blockUnion.OfText != nil {
						textBuilder.WriteString(blockUnion.OfText.Text)
					}
				}

				text := strings.TrimSpace(textBuilder.String())
				if text == "" {
					continue // Skip empty messages
				}

				// Display based on role - format URLs as clickable hyperlinks
				formattedText := formatURLsAsHyperlinks(text)
				switch item.msg.Role {
				case "user":
					_, _ = fmt.Fprintf(chatDisplay, "[cyan]You[white]: %s\n\n", formattedText)
				case "assistant":
					_, _ = fmt.Fprintf(chatDisplay, "[green]%s[white]: %s\n\n", agentName, formattedText)
				default:
					_, _ = fmt.Fprintf(chatDisplay, "[gray]%s[white]: %s\n\n", item.msg.Role, formattedText)
				}
			}
		}

		chatDisplay.ScrollToEnd()
	}
}

// handleChatMessage processes a chat message in the background
func (a *App) handleChatMessage(agentID, agentName, message string, chatDisplay *tview.TextView) {
	// Recover from panics and display the full stack trace
	defer func() {
		if r := recover(); r != nil {
			stackTrace := string(debug.Stack())
			// Display panic in UI
			a.app.QueueUpdateDraw(func() {
				_, _ = fmt.Fprintf(chatDisplay, "\n[red]PANIC: %v[white]\n\n", r)
				_, _ = fmt.Fprintf(chatDisplay, "[red]Stack trace:[white]\n%s\n", stackTrace)
				chatDisplay.ScrollToEnd()
			})
			// Also print to stderr for logging (will be visible in terminal)
			fmt.Fprintf(os.Stderr, "PANIC: %v\nStack trace:\n%s\n", r, stackTrace)
		}
	}()

	// Update UI to show user message and thinking indicator
	a.app.QueueUpdateDraw(func() {
		formattedMessage := formatURLsAsHyperlinks(message)
		_, _ = fmt.Fprintf(chatDisplay, "[cyan]You[white]: %s\n\n", formattedMessage)
		_, _ = fmt.Fprintf(chatDisplay, "[yellow]%s is thinking...[white]\n", agentName)
		chatDisplay.ScrollToEnd()
	})

	// Use configurable timeout from chat service
	timeout := a.chatService.GetChatTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Get or create thread ID for this agent
	threadID, err := a.chatService.GetOrCreateThreadID(ctx, agentID)
	if err != nil {
		// Fallback if we can't get/create thread ID
		threadID = fmt.Sprintf("chat-%s-%d", agentID, time.Now().Unix())
	}

	// Save user message to database
	_ = a.chatService.SaveMessage(ctx, agentID, threadID, "user", message)

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
				_, _ = fmt.Fprintf(chatDisplay, "[green]%s[white]: ", agentName)
			}

			// Append text directly to the display - format URLs as clickable hyperlinks
			formattedText := formatURLsAsHyperlinks(text)
			_, _ = fmt.Fprint(chatDisplay, formattedText)
			chatDisplay.ScrollToEnd()
		})
		return nil
	}

	// Create debug callback for showing tool invocations, API calls, etc.
	debugCallback := func(debugMsg string) {
		// Display debug info in dark gray
		a.app.QueueUpdateDraw(func() {
			_, _ = fmt.Fprintf(chatDisplay, "[gray]DEBUG: %s[white]\n", debugMsg)
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
				_, _ = fmt.Fprintf(chatDisplay, "[red]Error[white]: %v\n\n", err)
			} else {
				_, _ = fmt.Fprintf(chatDisplay, "\n\n[red]Error[white]: %v\n\n", err)
			}
			chatDisplay.ScrollToEnd()
		} else {
			// Finalize the response with a newline
			if started {
				_, _ = fmt.Fprint(chatDisplay, "\n\n")
			}

			// Save assistant response to database
			_ = a.chatService.SaveMessage(ctx, agentID, threadID, "assistant", response)

			// Update history in memory
			a.chatMutex.Lock()
			a.chatHistory[agentID] = append(a.chatHistory[agentID],
				anthropic.NewUserMessage(anthropic.NewTextBlock(message)),
				anthropic.NewAssistantMessage(anthropic.NewTextBlock(response)),
			)
			a.chatMutex.Unlock()

			// Ensure we're scrolled to the end
			chatDisplay.ScrollToEnd()
		}
	})
}

// handleResetContext handles the /reset command
func (a *App) handleResetContext(agentID, agentName, threadID string, chatDisplay *tview.TextView) {
	// Show status message
	a.app.QueueUpdateDraw(func() {
		_, _ = fmt.Fprintf(chatDisplay, "[yellow]Resetting context...[white]\n\n")
		chatDisplay.ScrollToEnd()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := a.chatService.ResetContext(ctx, agentID, threadID)
	if err != nil {
		a.app.QueueUpdateDraw(func() {
			_, _ = fmt.Fprintf(chatDisplay, "[red]Error resetting context: %v[white]\n\n", err)
			chatDisplay.ScrollToEnd()
		})
		return
	}

	// Refresh chat display to show the reset indicator
	a.app.QueueUpdateDraw(func() {
		// Reload the chat display with updated history
		// We need to get the threadID again to ensure we have the latest
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()

		// Update the chat history in memory
		history, err := a.chatService.LoadConversationHistory(ctx2, agentID, threadID)
		if err == nil {
			a.chatMutex.Lock()
			a.chatHistory[agentID] = history
			a.chatMutex.Unlock()
		}

		// Clear and refresh the display
		chatDisplay.Clear()
		a.updateChatDisplay(chatDisplay, agentID, agentName, threadID)
		chatDisplay.ScrollToEnd()
	})
}

// handleCompressContext handles the /compress command
func (a *App) handleCompressContext(agentID, agentName, threadID string, chatDisplay *tview.TextView) {
	// Show status message
	a.app.QueueUpdateDraw(func() {
		_, _ = fmt.Fprintf(chatDisplay, "[magenta]Compressing context (this may take a moment)...[white]\n\n")
		chatDisplay.ScrollToEnd()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := a.chatService.CompressContext(ctx, agentID, threadID)
	if err != nil {
		a.app.QueueUpdateDraw(func() {
			_, _ = fmt.Fprintf(chatDisplay, "[red]Error compressing context: %v[white]\n\n", err)
			chatDisplay.ScrollToEnd()
		})
		return
	}

	// Refresh chat display to show the compression indicator
	a.app.QueueUpdateDraw(func() {
		// Reload the chat display with updated history
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()

		// Update the chat history in memory
		history, err := a.chatService.LoadConversationHistory(ctx2, agentID, threadID)
		if err == nil {
			a.chatMutex.Lock()
			a.chatHistory[agentID] = history
			a.chatMutex.Unlock()
		}

		// Clear and refresh the display
		chatDisplay.Clear()
		a.updateChatDisplay(chatDisplay, agentID, agentName, threadID)
		chatDisplay.ScrollToEnd()
	})
}
