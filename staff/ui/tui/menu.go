package tui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (a *App) showInbox() {
	content := "[yellow]Inbox[white]\n\n"
	content += "You have no messages in your inbox."

	a.content.SetTitle("Inbox")
	a.content.SetText(content)
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

