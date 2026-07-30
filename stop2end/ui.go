package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	modePending = "pending"
	modeDone    = "done"
)

// UI holds all tview state for one mode (pending or done).
type UI struct {
	app       *tview.Application
	pages     *tview.Pages
	table     *tview.Table
	titleView *tview.TextView
	helpBar   *tview.TextView // static key legend, always visible
	statusBar *tview.TextView // dynamic messages (errors, confirmations)
	convos    []Convo
	selected  map[int]bool
	ignored   map[string]struct{}
	mode      string
	getConvos func() ([]Convo, error)
}

// result is one action outcome shown in the results screen.
type result struct {
	phone string
	ok    bool
	msg   string
}

func runUI(convos []Convo, mode string, ignored map[string]struct{}, getConvos func() ([]Convo, error)) {
	ui := &UI{
		app:       tview.NewApplication(),
		pages:     tview.NewPages(),
		convos:    convos,
		selected:  make(map[int]bool),
		ignored:   ignored,
		mode:      mode,
		getConvos: getConvos,
	}
	ui.buildListPage()
	if err := ui.app.SetRoot(ui.pages, true).SetFocus(ui.table).Run(); err != nil {
		panic(err)
	}
}

// ── List page ────────────────────────────────────────────────────────────────

func (ui *UI) buildListPage() {
	ui.titleView = tview.NewTextView().SetDynamicColors(true)
	ui.helpBar = tview.NewTextView().
		SetDynamicColors(true).
		SetText(ui.keyHint())
	ui.statusBar = tview.NewTextView().SetDynamicColors(true)
	ui.updateTitle()

	ui.table = tview.NewTable().
		SetBorders(false).
		SetSelectable(true, false).
		SetFixed(1, 0).
		SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorWhite).Foreground(tcell.ColorBlack))

	// Prevent navigating onto the header row.
	ui.table.SetSelectionChangedFunc(func(row, _ int) {
		if row == 0 && len(ui.convos) > 0 {
			ui.table.Select(1, 0)
		}
	})

	ui.renderTable()
	ui.setupKeys()

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(ui.titleView, 1, 0, false).
		AddItem(ui.table, 0, 1, true).
		AddItem(ui.helpBar, 1, 0, false).  // always-visible key legend
		AddItem(ui.statusBar, 1, 0, false) // dynamic status messages

	ui.pages.AddPage("list", layout, true, true)
}

func (ui *UI) updateTitle() {
	label := "Pending opt-out conversations"
	if ui.mode == modeDone {
		label = "Already-unsubscribed conversations"
	}
	ui.titleView.SetText(fmt.Sprintf(
		"[::b]stop2end[-] — %s  [::d][%d/%d selected][-]",
		label, len(ui.selected), len(ui.convos),
	))
}

func (ui *UI) keyHint() string {
	if ui.mode == modePending {
		return "[teal]↑↓[-] nav  [teal]SPC[-] select  [teal]a[-] all  [teal]n[-] none  " +
			"[teal]v[-] view thread  [teal]u[-] unsubscribe  [teal]d[-] unsub+delete  " +
			"[teal]i[-] ignore  [teal]r[-] refresh  [teal]q[-] quit"
	}
	return "[teal]↑↓[-] nav  [teal]SPC[-] select  [teal]a[-] all  [teal]n[-] none  " +
		"[teal]v[-] view thread  [teal]d[-] delete  [teal]i[-] ignore  [teal]r[-] refresh  [teal]q[-] quit"
}

// setStatus shows a temporary message in the status bar and clears it after 4 s.
func (ui *UI) setStatus(msg string) {
	ui.statusBar.SetText(msg)
	go func() {
		// Simple debounce: if another setStatus fires within 4 s it will overwrite.
		// We just clear after the delay; a later call's goroutine will also clear, harmlessly.
		select {
		case <-time.After(4 * time.Second):
		}
		ui.app.QueueUpdateDraw(func() {
			// Only clear if this message is still showing (avoid clearing a newer one).
			if ui.statusBar.GetText(false) == msg {
				ui.statusBar.Clear()
			}
		})
	}()
}

func (ui *UI) renderTable() {
	savedRow, _ := ui.table.GetSelection()
	ui.table.Clear()

	// Header row
	var headers []string
	if ui.mode == modePending {
		headers = []string{"  #", "Sel", "Sender", "Last msg", "Kwd", "Msgs", "Preview"}
	} else {
		headers = []string{"  #", "Sel", "Sender", "Last msg", "Confirmed message"}
	}
	for col, h := range headers {
		ui.table.SetCell(0, col, tview.NewTableCell(h).
			SetTextColor(tcell.ColorTeal).
			SetAttributes(tcell.AttrBold).
			SetSelectable(false))
	}

	for i, c := range ui.convos {
		// tview treats anything in [...] as a color tag, so escape the brackets.
		checkText := tview.Escape("[ ]")
		checkColor := tcell.ColorDefault
		if ui.selected[i] {
			checkText = tview.Escape("[x]")
			checkColor = tcell.ColorGreen
		}
		dateStr := c.LastDate.Format("Jan 02 '06")
		row := i + 1
		ui.table.SetCell(row, 0, tview.NewTableCell(fmt.Sprintf("  %d.", i+1)).SetAlign(tview.AlignRight))
		ui.table.SetCell(row, 1, tview.NewTableCell(checkText).SetTextColor(checkColor))
		ui.table.SetCell(row, 2, tview.NewTableCell(c.Phone))
		ui.table.SetCell(row, 3, tview.NewTableCell(dateStr).SetTextColor(tcell.ColorGray))

		if ui.mode == modePending {
			ui.table.SetCell(row, 4, tview.NewTableCell(c.Keyword).SetTextColor(tcell.ColorYellow))
			ui.table.SetCell(row, 5, tview.NewTableCell(fmt.Sprintf("%4d", c.Count)).SetAlign(tview.AlignRight))
			// Escape SMS content — messages can contain brackets
			ui.table.SetCell(row, 6, tview.NewTableCell(`"`+tview.Escape(trunc(c.Sample, 50))+`"`))
		} else {
			ui.table.SetCell(row, 4, tview.NewTableCell(`"`+tview.Escape(trunc(c.Confirm, 60))+`"`))
		}
	}

	// Restore cursor position.
	if len(ui.convos) > 0 {
		target := savedRow
		if target < 1 {
			target = 1
		}
		if target > len(ui.convos) {
			target = len(ui.convos)
		}
		ui.table.Select(target, 0)
	}

	ui.updateTitle()
}

// ── Key handling ─────────────────────────────────────────────────────────────

func (ui *UI) setupKeys() {
	ui.table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		row, _ := ui.table.GetSelection()
		idx := row - 1 // subtract header row

		switch event.Rune() {
		case ' ':
			if idx >= 0 && idx < len(ui.convos) {
				ui.selected[idx] = !ui.selected[idx]
				next := row + 1
				ui.renderTable()
				if next <= len(ui.convos) {
					ui.table.Select(next, 0)
				}
			}
			return nil

		case 'a':
			for i := range ui.convos {
				ui.selected[i] = true
			}
			ui.renderTable()
			return nil

		case 'n':
			ui.selected = make(map[int]bool)
			ui.renderTable()
			return nil

		case 'v':
			if idx >= 0 && idx < len(ui.convos) {
				ui.showThread(ui.convos[idx])
			}
			return nil

		case 'r':
			ui.setStatus("[yellow]Refreshing…[-]")
			go func() {
				newConvos, err := ui.getConvos()
				ui.app.QueueUpdateDraw(func() {
					if err != nil {
						ui.setStatus("[red]Refresh error: " + tview.Escape(err.Error()) + "[-]")
					} else {
						ui.convos = newConvos
						ui.selected = make(map[int]bool)
						ui.renderTable()
						ui.setStatus(fmt.Sprintf("[green]Refreshed — %d conversation(s) found.[-]", len(newConvos)))
					}
				})
			}()
			return nil

		case 'q':
			ui.app.Stop()
			return nil

		case 'u':
			if ui.mode != modePending {
				return nil
			}
			targets := ui.selectedIndices()
			if len(targets) == 0 {
				ui.setStatus("[yellow]Select items first (SPACE / a).[-]")
				return nil
			}
			ui.doUnsubscribe(targets)
			return nil

		case 'd':
			targets := ui.selectedIndices()
			if len(targets) == 0 {
				ui.setStatus("[yellow]Select items first (SPACE / a).[-]")
				return nil
			}
			ui.showConfirmDelete(targets)
			return nil

		case 'i':
			targets := ui.selectedIndices()
			if len(targets) == 0 {
				ui.setStatus("[yellow]Select items first (SPACE / a).[-]")
				return nil
			}
			ui.doIgnore(targets)
			return nil
		}

		return event
	})
}

func (ui *UI) selectedIndices() []int {
	out := make([]int, 0, len(ui.selected))
	for i := range ui.selected {
		if ui.selected[i] {
			out = append(out, i)
		}
	}
	sort.Ints(out)
	return out
}

// ── Thread view ───────────────────────────────────────────────────────────────

func (ui *UI) showThread(convo Convo) {
	var sb strings.Builder
	for _, msg := range convo.Messages {
		if strings.TrimSpace(msg.Text) == "" {
			continue
		}
		ts := msg.Time.Format("Jan 02 15:04")
		who := "   me"
		if !msg.FromMe {
			who = " them"
		}
		fmt.Fprintf(&sb, "%s  %s  %s\n\n", ts, who, strings.ReplaceAll(msg.Text, "\n", " "))
	}

	content := tview.NewTextView().
		SetText(sb.String()).
		SetScrollable(true).
		SetWordWrap(true)
	content.ScrollToEnd()

	header := tview.NewTextView().
		SetDynamicColors(true).
		SetText(fmt.Sprintf("[::b]Thread: %s[-]  [::d]↑↓ scroll · ESC/q return[-]", tview.Escape(convo.Phone)))

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(header, 1, 0, false).
		AddItem(content, 0, 1, true)

	content.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape || event.Rune() == 'q' {
			ui.pages.RemovePage("thread")
			ui.app.SetFocus(ui.table)
			return nil
		}
		return event
	})

	ui.pages.AddPage("thread", layout, true, true)
	ui.app.SetFocus(content)
}

// ── Delete confirmation ───────────────────────────────────────────────────────

func (ui *UI) showConfirmDelete(targets []int) {
	var sb strings.Builder
	verb := "Unsubscribe + delete"
	if ui.mode == modeDone {
		verb = "Delete"
	}
	fmt.Fprintf(&sb, "%s %d conversation(s):\n\n", verb, len(targets))
	for _, idx := range targets {
		c := ui.convos[idx]
		if ui.mode == modePending {
			fmt.Fprintf(&sb, "  • %s  [reply: %s  %d msg(s)]\n",
				tview.Escape(c.Phone), c.Keyword, c.Count)
		} else {
			fmt.Fprintf(&sb, "  • %s\n", tview.Escape(c.Phone))
		}
		sample := c.Sample
		if c.Confirm != "" {
			sample = c.Confirm
		}
		fmt.Fprintf(&sb, "    %s\n\n", tview.Escape(trunc(sample, 80)))
	}

	infoView := tview.NewTextView().
		SetDynamicColors(true).
		SetText(sb.String())

	errorLine := tview.NewTextView().
		SetDynamicColors(true)

	input := tview.NewInputField().
		SetLabel(`Type "delete" to confirm (ESC cancels): `).
		SetFieldWidth(10)

	dismiss := func() {
		ui.pages.RemovePage("confirm")
		ui.app.SetFocus(ui.table)
	}

	input.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			dismiss()
			return nil
		}
		return event
	})

	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEscape {
			dismiss()
			return
		}
		if key == tcell.KeyEnter {
			if input.GetText() == "delete" {
				dismiss()
				ui.doDelete(targets)
			} else {
				input.SetText("")
				errorLine.SetText(`[red]Must type exactly "delete" — try again or press ESC.[-]`)
			}
		}
	})

	titleRow := tview.NewTextView().
		SetDynamicColors(true).
		SetText("[::b]  Confirm Deletion[-]")

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(titleRow, 1, 0, false).
		AddItem(infoView, 0, 1, false).
		AddItem(errorLine, 1, 0, false).
		AddItem(input, 1, 0, true)
	layout.SetBorder(true).SetBorderColor(tcell.ColorRed)

	ui.pages.AddPage("confirm", layout, true, true)
	ui.app.SetFocus(input)
}

// ── Actions ───────────────────────────────────────────────────────────────────

func (ui *UI) doUnsubscribe(targets []int) {
	ui.showWorking("Sending unsubscribe replies…")
	go func() {
		var results []result
		var succeeded []int
		for _, idx := range targets {
			c := ui.convos[idx]
			err := sendUnsubscribe(c.Phone, c.Keyword)
			ok := err == nil
			msg := fmt.Sprintf("'%s' sent", c.Keyword)
			if !ok {
				msg = "FAILED: " + err.Error()
			} else {
				succeeded = append(succeeded, idx)
			}
			results = append(results, result{c.Phone, ok, msg})
		}
		ui.app.QueueUpdateDraw(func() {
			ui.pages.RemovePage("working")
			ui.removeConvos(succeeded)
			ui.showResults(results)
		})
	}()
}

func (ui *UI) doDelete(targets []int) {
	ui.showWorking("Processing…")
	go func() {
		var results []result
		for _, idx := range targets {
			c := ui.convos[idx]
			var parts []string
			allOK := true

			if ui.mode == modePending {
				if err := sendUnsubscribe(c.Phone, c.Keyword); err != nil {
					parts = append(parts, "unsub FAILED: "+err.Error())
					allOK = false
				} else {
					parts = append(parts, "unsub ok")
				}
			}

			if err := deleteConversation(c.ChatIdentifier); err != nil {
				parts = append(parts, "del FAILED: "+err.Error())
				allOK = false
			} else {
				parts = append(parts, "del ok")
			}

			results = append(results, result{c.Phone, allOK, strings.Join(parts, "  ")})
		}
		ui.app.QueueUpdateDraw(func() {
			ui.pages.RemovePage("working")
			ui.removeConvos(targets) // remove regardless of per-step failures
			ui.showResults(results)
		})
	}()
}

func (ui *UI) doIgnore(targets []int) {
	for _, idx := range targets {
		ui.ignored[ui.convos[idx].Phone] = struct{}{}
	}
	if err := saveIgnored(ui.ignored); err != nil {
		ui.setStatus("[red]Warning: could not save ignore list: " + tview.Escape(err.Error()) + "[-]")
	} else {
		ui.setStatus(fmt.Sprintf("[green]Added %d to ignore list.[-]", len(targets)))
	}
	ui.removeConvos(targets)
}

// ── Shared helpers ────────────────────────────────────────────────────────────

func (ui *UI) showWorking(msg string) {
	view := tview.NewTextView().
		SetText("\n\n  " + msg).
		SetTextAlign(tview.AlignCenter)
	view.SetBorder(true)
	ui.pages.AddPage("working", view, true, true)
	// Do not call app.Draw() here — we're inside the tview event handler and
	// Draw() can deadlock if the application mutex is already held. tview will
	// redraw automatically after the handler returns, before the goroutine gets
	// meaningful CPU time.
}

func (ui *UI) showResults(results []result) {
	var sb strings.Builder
	for _, r := range results {
		mark := "[green]✓[-]"
		if !r.ok {
			mark = "[red]✗[-]"
		}
		fmt.Fprintf(&sb, "  %s  %s  %s\n", mark, tview.Escape(r.phone), tview.Escape(r.msg))
	}

	content := tview.NewTextView().
		SetDynamicColors(true).
		SetText(sb.String())

	footer := tview.NewTextView().
		SetText("  Press any key to continue…").
		SetTextColor(tcell.ColorGray)

	dismiss := func() {
		ui.pages.RemovePage("results")
		if len(ui.convos) == 0 {
			ui.app.Stop()
			return
		}
		ui.app.SetFocus(ui.table)
	}

	content.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		dismiss()
		return nil
	})

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tview.NewTextView().SetDynamicColors(true).SetText("[::b]  Results[-]"), 1, 0, false).
		AddItem(content, 0, 1, true).
		AddItem(footer, 1, 0, false)
	layout.SetBorder(true)

	ui.pages.AddPage("results", layout, true, true)
	ui.app.SetFocus(content)
}

// removeConvos removes the items at indices and rebuilds the selected set
// with corrected index offsets.
func (ui *UI) removeConvos(indices []int) {
	if len(indices) == 0 {
		return
	}
	removed := make(map[int]bool, len(indices))
	for _, idx := range indices {
		removed[idx] = true
	}

	newConvos := make([]Convo, 0, len(ui.convos)-len(removed))
	newSelected := make(map[int]bool)
	newIdx := 0
	for oldIdx, c := range ui.convos {
		if removed[oldIdx] {
			continue
		}
		if ui.selected[oldIdx] {
			newSelected[newIdx] = true
		}
		newConvos = append(newConvos, c)
		newIdx++
	}
	ui.convos = newConvos
	ui.selected = newSelected
	ui.renderTable()
}
