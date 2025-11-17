package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/aschepis/backscratcher/staff/ui"
)

func (a *App) showInbox() {
	// Create a list for inbox items
	inboxList := tview.NewList()
	inboxList.SetBorder(true).SetTitle("Inbox - Select to View Details (a: Archive, r: Refresh)")

	// Store current items for archiving - this will be updated on each refresh
	var currentItems []*ui.InboxItem
	var itemsMutex sync.RWMutex

	// Load inbox items
	refreshInbox := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		items, err := a.chatService.ListInboxItems(ctx, false) // Don't include archived
		if err != nil {
			a.app.QueueUpdateDraw(func() {
				inboxList.Clear()
				inboxList.AddItem("Error", fmt.Sprintf("Failed to load inbox: %v", err), ' ', nil)
				itemsMutex.Lock()
				currentItems = nil
				itemsMutex.Unlock()
			})
			return
		}

		a.app.QueueUpdateDraw(func() {
			inboxList.Clear()
			itemsMutex.Lock()
			currentItems = items // Store items for archiving
			itemsMutex.Unlock()
			if len(items) == 0 {
				inboxList.AddItem("No messages", "Your inbox is empty", ' ', nil)
			} else {
				for i, item := range items {
					// Format item display
					agentID := item.AgentID
					if agentID == "" {
						agentID = "System"
					}

					// Truncate message for display
					msgPreview := item.Message
					if len(msgPreview) > 50 {
						msgPreview = msgPreview[:47] + "..."
					}

					// Format time
					timeStr := item.CreatedAt.Format("Jan 2, 15:04")
					label := fmt.Sprintf("[%s] %s", timeStr, msgPreview)
					secondaryText := fmt.Sprintf("From: %s", agentID)
					if item.RequiresResponse {
						secondaryText += " [red](Response Required)[white]"
					}

					// Capture item in closure
					itemCopy := item
					inboxList.AddItem(label, secondaryText, rune('1'+i), func() {
						a.showInboxItemDetail(itemCopy)
					})
				}
			}
			inboxList.AddItem("Back", "Return to main menu", 'b', func() {
				a.pages.SwitchToPage("main")
				a.app.SetFocus(a.sidebar)
			})
		})
	}

	// Initial load - run asynchronously so it doesn't block UI startup
	go refreshInbox()

	// Handle input
	inboxList.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyEsc:
			a.pages.SwitchToPage("main")
			a.app.SetFocus(a.sidebar)
			return nil
		case tcell.KeyRune:
			switch ev.Rune() {
			case 'r', 'R':
				// Refresh inbox
				go refreshInbox()
				return nil
			case 'a', 'A':
				// Archive selected item
				currentItemIndex := inboxList.GetCurrentItem()

				itemsMutex.RLock()
				items := currentItems
				itemsCount := len(items)
				itemsMutex.RUnlock()

				originalTitle := inboxList.GetTitle()

				// Validate selection
				if currentItemIndex < 0 {
					return nil
				}

				// Check if we're trying to archive the "Back" item
				// The "Back" item is always the last item in the list
				if items == nil || currentItemIndex >= itemsCount {
					return nil
				}

				item := items[currentItemIndex]

				// Perform ALL work in background goroutine to avoid blocking the input handler
				go func() {
					// Update title to show archiving status
					a.app.QueueUpdateDraw(func() {
						inboxList.SetTitle(fmt.Sprintf("Inbox - Archiving item %d/%d...", currentItemIndex+1, itemsCount))
					})

					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()

					err := a.chatService.ArchiveInboxItem(ctx, item.ID)

					a.app.QueueUpdateDraw(func() {
						if err != nil {
							inboxList.SetTitle(fmt.Sprintf("Inbox - Archive failed: %v", err))
							// Reset title after a delay
							go func() {
								time.Sleep(2 * time.Second)
								a.app.QueueUpdateDraw(func() {
									inboxList.SetTitle(originalTitle)
								})
							}()
						} else {
							inboxList.SetTitle(originalTitle)
						}
					})

					// Refresh inbox AFTER the UI update, in a separate goroutine
					// to avoid nesting QueueUpdateDraw calls (which causes deadlock)
					if err == nil {
						go refreshInbox()
					}
				}()
				return nil
			}
		}
		return ev
	})

	// Create page for inbox
	a.pages.AddPage("inbox", inboxList, true, false)
	a.pages.SwitchToPage("inbox")
	a.app.SetFocus(inboxList)
}

func (a *App) showInboxItemDetail(item *ui.InboxItem) {
	detailView := tview.NewTextView()
	detailView.SetDynamicColors(true).
		SetWordWrap(true).
		SetBorder(true).
		SetTitle(fmt.Sprintf("Inbox Item #%d", item.ID))

	// Format detail display
	var content strings.Builder
	content.WriteString(fmt.Sprintf("[yellow]From[white]: %s\n", item.AgentID))
	if item.ThreadID != "" {
		content.WriteString(fmt.Sprintf("[yellow]Thread[white]: %s\n", item.ThreadID))
	}
	content.WriteString(fmt.Sprintf("[yellow]Date[white]: %s\n\n", item.CreatedAt.Format("2006-01-02 15:04:05")))

	content.WriteString(fmt.Sprintf("[cyan]Message[white]:\n%s\n\n", item.Message))

	if item.RequiresResponse {
		content.WriteString("[red]⚠ Response Required[white]\n\n")
	}

	if item.Response != "" {
		responseTime := ""
		if item.ResponseAt != nil {
			responseTime = item.ResponseAt.Format("2006-01-02 15:04:05")
		}
		content.WriteString(fmt.Sprintf("[green]Response[white] (%s):\n%s\n\n", responseTime, item.Response))
	}

	if item.ArchivedAt != nil {
		content.WriteString(fmt.Sprintf("[gray]Archived: %s[white]\n", item.ArchivedAt.Format("2006-01-02 15:04:05")))
	}

	detailView.SetText(content.String())

	// Handle navigation
	detailView.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEsc {
			a.pages.SwitchToPage("inbox")
			// Get the inbox list from the pages
			_, inboxPage := a.pages.GetFrontPage()
			if inboxList, ok := inboxPage.(*tview.List); ok {
				a.app.SetFocus(inboxList)
			}
			return nil
		}
		return ev
	})

	// Add page for detail
	pageName := fmt.Sprintf("inbox_detail_%d", item.ID)
	a.pages.AddPage(pageName, detailView, true, false)
	a.pages.SwitchToPage(pageName)
	a.app.SetFocus(detailView)
}

func (a *App) showCrewMembers() {
	agents := a.chatService.ListAgents()

	// Create a selectable list of agents
	agentList := tview.NewList()
	agentList.SetBorder(true).SetTitle("Crew Members - Select an Agent to Chat")

	for _, ag := range agents {
		agentID := ag.ID // Capture in closure
		agentName := ag.Name

		agentList.AddItem(agentName, "", 0, func() {
			a.showChat(agentID)
		})
	}

	agentList.AddItem("Back", "Return to main menu", 'b', func() {
		a.pages.SwitchToPage("main")
		a.app.SetFocus(a.sidebar)
	})

	// Handle Esc key to go back
	agentList.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEsc {
			a.pages.SwitchToPage("main")
			a.app.SetFocus(a.sidebar)
			return nil
		}
		return ev
	})

	// Create a page for the agent list
	a.pages.AddPage("agent_list", agentList, true, false)
	a.pages.SwitchToPage("agent_list")
	a.app.SetFocus(agentList)
}
