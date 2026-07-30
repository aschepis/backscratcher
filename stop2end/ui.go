package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// tab constants
const (
	tabSpam    = 0
	tabAll     = 1
	tabSearch  = 2
	tabBlocked = 3
)

// spam sub-modes
const (
	modePending = "pending"
	modeDone    = "done"
)

// UI holds all tview state.
type UI struct {
	app      *tview.Application
	pages    *tview.Pages
	tabBar   *tview.TextView
	table    *tview.Table
	helpBar  *tview.TextView
	statusBar *tview.TextView

	// data
	contacts     *ContactCache
	ignored      map[string]struct{}

	// per-tab convos
	spamPending  []Convo
	spamDone     []Convo
	allConvos    []Convo
	searchResult []Convo
	blockedList  []string // ordered phones

	// current state
	activeTab  int
	spamMode   string // modePending or modeDone
	selected   map[int]bool

	// refresh callbacks
	getSpam func() (pending, done []Convo, err error)
	getAll  func() ([]Convo, error)
}

// result is one action outcome shown in the results screen.
type result struct {
	phone string
	ok    bool
	msg   string
}

func runUI(
	spamPending, spamDone []Convo,
	allConvos []Convo,
	contacts *ContactCache,
	ignored map[string]struct{},
	getSpam func() ([]Convo, []Convo, error),
	getAll func() ([]Convo, error),
) {
	ui := &UI{
		app:         tview.NewApplication(),
		pages:       tview.NewPages(),
		contacts:    contacts,
		ignored:     ignored,
		spamPending: applyContacts(spamPending, contacts),
		spamDone:    applyContacts(spamDone, contacts),
		allConvos:   applyContacts(allConvos, contacts),
		selected:    make(map[int]bool),
		activeTab:   tabSpam,
		spamMode:    modePending,
		getSpam:     getSpam,
		getAll:      getAll,
	}
	ui.syncBlockedList()
	ui.build()

	if err := ui.app.SetRoot(ui.pages, true).SetFocus(ui.table).Run(); err != nil {
		panic(err)
	}
}

func applyContacts(convos []Convo, c *ContactCache) []Convo {
	for i := range convos {
		convos[i].DisplayName = c.Name(convos[i].Phone)
	}
	return convos
}

// ── Build ─────────────────────────────────────────────────────────────────────

func (ui *UI) build() {
	ui.tabBar = tview.NewTextView().SetDynamicColors(true)
	ui.helpBar = tview.NewTextView().SetDynamicColors(true)
	ui.statusBar = tview.NewTextView().SetDynamicColors(true)

	ui.table = tview.NewTable().
		SetBorders(false).
		SetSelectable(true, false).
		SetFixed(1, 0).
		SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorWhite).Foreground(tcell.ColorBlack))

	ui.table.SetSelectionChangedFunc(func(row, _ int) {
		if row == 0 && len(ui.currentConvos()) > 0 {
			ui.table.Select(1, 0)
		}
	})

	ui.renderTabBar()
	ui.renderTable()
	ui.renderHelp()
	ui.setupKeys()

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(ui.tabBar, 1, 0, false).
		AddItem(ui.table, 0, 1, true).
		AddItem(ui.helpBar, 1, 0, false).
		AddItem(ui.statusBar, 1, 0, false)

	ui.pages.AddPage("main", layout, true, true)
}

// ── Tab bar ───────────────────────────────────────────────────────────────────

func (ui *UI) renderTabBar() {
	tabs := []struct {
		key   string
		label string
		idx   int
	}{
		{"1", "Spam", tabSpam},
		{"2", "All", tabAll},
		{"3", "Search", tabSearch},
		{"4", "Blocked", tabBlocked},
	}
	var sb strings.Builder
	sb.WriteString("[::b]stop2end[-]  ")
	for _, t := range tabs {
		if t.idx == ui.activeTab {
			fmt.Fprintf(&sb, "[black:teal] %s:%s [-] ", t.key, t.label)
		} else {
			fmt.Fprintf(&sb, "[teal]%s[-]:[::d]%s[-]  ", t.key, t.label)
		}
	}
	// show spam sub-mode indicator
	if ui.activeTab == tabSpam {
		if ui.spamMode == modePending {
			fmt.Fprintf(&sb, "  [yellow](pending)[-]")
		} else {
			fmt.Fprintf(&sb, "  [green](done)[-]")
		}
	}
	ui.tabBar.SetText(sb.String())
}

// ── Help bar ──────────────────────────────────────────────────────────────────

func (ui *UI) renderHelp() {
	var s string
	base := "[teal]↑↓/jk[-] nav  [teal]SPC[-] sel  [teal]a/n[-] all/none  [teal]v[-] thread  [teal]r[-] refresh  [teal]/[-] search  [teal]e[-] export  [teal]q[-] quit"
	switch ui.activeTab {
	case tabSpam:
		if ui.spamMode == modePending {
			s = base + "  [teal]TAB[-] →done  [teal]u[-] unsub  [teal]d[-] unsub+del  [teal]b[-] block"
		} else {
			s = base + "  [teal]TAB[-] →pending  [teal]d[-] delete  [teal]b[-] block"
		}
	case tabAll:
		s = base + "  [teal]d[-] delete  [teal]b[-] block"
	case tabSearch:
		s = "[teal]↑↓/jk[-] nav  [teal]v[-] thread  [teal]e[-] export  [teal]/[-] new search  [teal]ESC/q[-] back"
	case tabBlocked:
		s = "[teal]↑↓/jk[-] nav  [teal]SPC[-] sel  [teal]DEL/x[-] unblock  [teal]I[-] import  [teal]X[-] export list  [teal]q[-] quit"
	}
	ui.helpBar.SetText(s)
}

// ── Status bar ────────────────────────────────────────────────────────────────

func (ui *UI) setStatus(msg string) {
	ui.statusBar.SetText(msg)
	go func() {
		time.Sleep(4 * time.Second)
		ui.app.QueueUpdateDraw(func() {
			if ui.statusBar.GetText(false) == msg {
				ui.statusBar.Clear()
			}
		})
	}()
}

// ── Current data ──────────────────────────────────────────────────────────────

func (ui *UI) currentConvos() []Convo {
	switch ui.activeTab {
	case tabSpam:
		if ui.spamMode == modePending {
			return ui.spamPending
		}
		return ui.spamDone
	case tabAll:
		return ui.allConvos
	case tabSearch:
		return ui.searchResult
	default:
		return nil
	}
}

func (ui *UI) setCurrentConvos(convos []Convo) {
	switch ui.activeTab {
	case tabSpam:
		if ui.spamMode == modePending {
			ui.spamPending = convos
		} else {
			ui.spamDone = convos
		}
	case tabAll:
		ui.allConvos = convos
	case tabSearch:
		ui.searchResult = convos
	}
}

// ── Table rendering ───────────────────────────────────────────────────────────

func (ui *UI) renderTable() {
	if ui.activeTab == tabBlocked {
		ui.renderBlockedTable()
		return
	}

	savedRow, _ := ui.table.GetSelection()
	ui.table.Clear()

	convos := ui.currentConvos()

	// Header
	var headers []string
	switch ui.activeTab {
	case tabSpam:
		if ui.spamMode == modePending {
			headers = []string{"  #", "Sel", "Contact", "Last msg", "Kwd", "Msgs", "Preview"}
		} else {
			headers = []string{"  #", "Sel", "Contact", "Last msg", "Confirmed message"}
		}
	default:
		headers = []string{"  #", "Sel", "Contact", "Last msg", "Preview"}
	}
	for col, h := range headers {
		ui.table.SetCell(0, col, tview.NewTableCell(h).
			SetTextColor(tcell.ColorTeal).
			SetAttributes(tcell.AttrBold).
			SetSelectable(false))
	}

	for i, c := range convos {
		checkText := tview.Escape("[ ]")
		checkColor := tcell.ColorDefault
		if ui.selected[i] {
			checkText = tview.Escape("[x]")
			checkColor = tcell.ColorGreen
		}
		dateStr := c.LastDate.Format("Jan 02 '06")
		row := i + 1
		label := tview.Escape(c.Label())

		ui.table.SetCell(row, 0, tview.NewTableCell(fmt.Sprintf("  %d.", i+1)).SetAlign(tview.AlignRight))
		ui.table.SetCell(row, 1, tview.NewTableCell(checkText).SetTextColor(checkColor))
		ui.table.SetCell(row, 2, tview.NewTableCell(label))
		ui.table.SetCell(row, 3, tview.NewTableCell(dateStr).SetTextColor(tcell.ColorGray))

		switch {
		case ui.activeTab == tabSpam && ui.spamMode == modePending:
			ui.table.SetCell(row, 4, tview.NewTableCell(c.Keyword).SetTextColor(tcell.ColorYellow))
			ui.table.SetCell(row, 5, tview.NewTableCell(fmt.Sprintf("%4d", c.Count)).SetAlign(tview.AlignRight))
			ui.table.SetCell(row, 6, tview.NewTableCell(`"`+tview.Escape(trunc(c.Sample, 50))+`"`))
		case ui.activeTab == tabSpam && ui.spamMode == modeDone:
			ui.table.SetCell(row, 4, tview.NewTableCell(`"`+tview.Escape(trunc(c.Confirm, 60))+`"`))
		default:
			ui.table.SetCell(row, 4, tview.NewTableCell(`"`+tview.Escape(trunc(c.Sample, 60))+`"`))
		}
	}

	// Restore cursor
	if len(convos) > 0 {
		target := savedRow
		if target < 1 {
			target = 1
		}
		if target > len(convos) {
			target = len(convos)
		}
		ui.table.Select(target, 0)
	}

	ui.renderTabBar()
	ui.renderHelp()
}

func (ui *UI) renderBlockedTable() {
	savedRow, _ := ui.table.GetSelection()
	ui.table.Clear()

	headers := []string{"  #", "Sel", "Phone / Identifier"}
	for col, h := range headers {
		ui.table.SetCell(0, col, tview.NewTableCell(h).
			SetTextColor(tcell.ColorTeal).
			SetAttributes(tcell.AttrBold).
			SetSelectable(false))
	}
	for i, phone := range ui.blockedList {
		checkText := tview.Escape("[ ]")
		checkColor := tcell.ColorDefault
		if ui.selected[i] {
			checkText = tview.Escape("[x]")
			checkColor = tcell.ColorGreen
		}
		row := i + 1
		ui.table.SetCell(row, 0, tview.NewTableCell(fmt.Sprintf("  %d.", i+1)).SetAlign(tview.AlignRight))
		ui.table.SetCell(row, 1, tview.NewTableCell(checkText).SetTextColor(checkColor))
		name := ui.contacts.Name(phone)
		label := phone
		if name != phone {
			label = name + " (" + phone + ")"
		}
		ui.table.SetCell(row, 2, tview.NewTableCell(tview.Escape(label)))
	}
	if len(ui.blockedList) > 0 {
		target := savedRow
		if target < 1 {
			target = 1
		}
		if target > len(ui.blockedList) {
			target = len(ui.blockedList)
		}
		ui.table.Select(target, 0)
	}
	ui.renderTabBar()
	ui.renderHelp()
}

// ── Key handling ──────────────────────────────────────────────────────────────

func (ui *UI) setupKeys() {
	ui.table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		row, _ := ui.table.GetSelection()
		idx := row - 1

		// Tab switch via number keys
		switch event.Rune() {
		case '1':
			ui.switchTab(tabSpam)
			return nil
		case '2':
			ui.switchTab(tabAll)
			return nil
		case '3':
			ui.switchTab(tabSearch)
			return nil
		case '4':
			ui.switchTab(tabBlocked)
			return nil
		}

		// vim navigation
		switch event.Rune() {
		case 'j':
			row, _ := ui.table.GetSelection()
			ui.table.Select(row+1, 0)
			return nil
		case 'k':
			row, _ := ui.table.GetSelection()
			if row > 1 {
				ui.table.Select(row-1, 0)
			}
			return nil
		}

		// Blocked tab has its own reduced key set
		if ui.activeTab == tabBlocked {
			return ui.blockedKeys(event, idx)
		}

		switch event.Rune() {
		case ' ':
			if idx >= 0 && idx < len(ui.currentConvos()) {
				ui.selected[idx] = !ui.selected[idx]
				ui.renderTable()
				if row+1 <= len(ui.currentConvos()) {
					ui.table.Select(row+1, 0)
				}
			}
			return nil

		case 'a':
			for i := range ui.currentConvos() {
				ui.selected[i] = true
			}
			ui.renderTable()
			return nil

		case 'n':
			ui.selected = make(map[int]bool)
			ui.renderTable()
			return nil

		case 'v':
			convos := ui.currentConvos()
			if idx >= 0 && idx < len(convos) {
				ui.showThread(convos[idx])
			}
			return nil

		case '/':
			ui.showSearchPrompt()
			return nil

		case 'e':
			targets := ui.selectedIndices()
			if len(targets) == 0 {
				ui.setStatus("[yellow]Select items first (SPACE / a).[-]")
				return nil
			}
			ui.showExportPrompt(targets)
			return nil

		case 'r':
			ui.doRefresh()
			return nil

		case 'q':
			ui.app.Stop()
			return nil

		case 'u':
			if ui.activeTab != tabSpam || ui.spamMode != modePending {
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

		case 'b':
			targets := ui.selectedIndices()
			if len(targets) == 0 {
				ui.setStatus("[yellow]Select items first (SPACE / a).[-]")
				return nil
			}
			ui.doBlock(targets)
			return nil
		}

		// TAB cycles spam sub-modes
		if event.Key() == tcell.KeyTab && ui.activeTab == tabSpam {
			if ui.spamMode == modePending {
				ui.spamMode = modeDone
			} else {
				ui.spamMode = modePending
			}
			ui.selected = make(map[int]bool)
			ui.renderTable()
			return nil
		}

		// ESC in search tab goes back to All
		if event.Key() == tcell.KeyEscape && ui.activeTab == tabSearch {
			ui.switchTab(tabAll)
			return nil
		}

		return event
	})
}

func (ui *UI) blockedKeys(event *tcell.EventKey, idx int) *tcell.EventKey {
	switch event.Rune() {
	case ' ':
		if idx >= 0 && idx < len(ui.blockedList) {
			ui.selected[idx] = !ui.selected[idx]
			row, _ := ui.table.GetSelection()
			ui.renderBlockedTable()
			if row+1 <= len(ui.blockedList) {
				ui.table.Select(row+1, 0)
			}
		}
		return nil
	case 'a':
		for i := range ui.blockedList {
			ui.selected[i] = true
		}
		ui.renderBlockedTable()
		return nil
	case 'n':
		ui.selected = make(map[int]bool)
		ui.renderBlockedTable()
		return nil
	case 'x':
		targets := ui.selectedIndices()
		if len(targets) == 0 && idx >= 0 && idx < len(ui.blockedList) {
			targets = []int{idx}
		}
		if len(targets) == 0 {
			return nil
		}
		ui.doUnblock(targets)
		return nil
	case 'I':
		ui.showBlockImportPrompt()
		return nil
	case 'X':
		ui.showBlockExportPrompt()
		return nil
	case 'q':
		ui.app.Stop()
		return nil
	}
	if event.Key() == tcell.KeyDelete {
		targets := ui.selectedIndices()
		if len(targets) == 0 && idx >= 0 && idx < len(ui.blockedList) {
			targets = []int{idx}
		}
		if len(targets) > 0 {
			ui.doUnblock(targets)
		}
		return nil
	}
	return event
}

func (ui *UI) switchTab(tab int) {
	ui.activeTab = tab
	ui.selected = make(map[int]bool)
	ui.renderTable()
	ui.table.Select(1, 0)
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
	// Lazy load messages if not yet populated (All tab loads metadata only).
	if len(convo.Messages) == 0 {
		msgs, err := loadMessages(convo.ChatIdentifier)
		if err != nil {
			ui.setStatus("[red]Could not load messages: " + tview.Escape(err.Error()) + "[-]")
			return
		}
		convo.Messages = msgs
		// Update the stored convo so subsequent opens are fast.
		convos := ui.currentConvos()
		for i, c := range convos {
			if c.ChatIdentifier == convo.ChatIdentifier {
				convos[i].Messages = msgs
				ui.setCurrentConvos(convos)
				break
			}
		}
	}

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

	label := tview.Escape(convo.Label())
	if convo.DisplayName != "" && convo.Phone != convo.DisplayName {
		label = tview.Escape(convo.DisplayName) + "  [::d]" + tview.Escape(convo.Phone) + "[-]"
	}
	header := tview.NewTextView().
		SetDynamicColors(true).
		SetText(fmt.Sprintf("[::b]Thread: %s[-]  [::d]↑↓ scroll · ESC/q return[-]", label))

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

// ── Search modal ──────────────────────────────────────────────────────────────

func (ui *UI) showSearchPrompt() {
	input := tview.NewInputField().
		SetLabel("Search: ").
		SetFieldWidth(40)

	dismiss := func() {
		ui.pages.RemovePage("search-prompt")
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
			q := strings.TrimSpace(input.GetText())
			dismiss()
			if q == "" {
				return
			}
			// Search across all convos (all + spam).
			all := append(ui.allConvos, ui.spamPending...)
			all = append(all, ui.spamDone...)
			ui.searchResult = searchConvos(q, all)
			ui.activeTab = tabSearch
			ui.selected = make(map[int]bool)
			ui.renderTable()
			ui.table.Select(1, 0)
			ui.setStatus(fmt.Sprintf("[green]%d result(s) for %q[-]", len(ui.searchResult), q))
		}
	})

	box := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tview.NewTextView().SetDynamicColors(true).SetText("[::b]  Search Messages[-]"), 1, 0, false).
		AddItem(input, 1, 0, true).
		AddItem(tview.NewTextView().SetText("  ESC to cancel"), 1, 0, false)
	box.SetBorder(true).SetBorderColor(tcell.ColorTeal)

	ui.pages.AddPage("search-prompt", box, true, true)
	ui.app.SetFocus(input)
}

// ── Export modal ──────────────────────────────────────────────────────────────

func (ui *UI) showExportPrompt(targets []int) {
	convos := ui.currentConvos()
	selected := make([]Convo, 0, len(targets))
	for _, idx := range targets {
		if idx < len(convos) {
			selected = append(selected, convos[idx])
		}
	}

	fmtInput := tview.NewInputField().
		SetLabel("Format (j=JSON, m=Markdown): ").
		SetFieldWidth(3)
	dirInput := tview.NewInputField().
		SetLabel("Output directory: ").
		SetFieldWidth(40).
		SetText(os.Getenv("HOME"))
	errorLine := tview.NewTextView().SetDynamicColors(true)

	focused := 0 // 0=fmtInput, 1=dirInput
	fields := []tview.Primitive{fmtInput, dirInput}

	dismiss := func() {
		ui.pages.RemovePage("export-prompt")
		ui.app.SetFocus(ui.table)
	}

	doExport := func() {
		fmtStr := strings.ToLower(strings.TrimSpace(fmtInput.GetText()))
		dir := strings.TrimSpace(dirInput.GetText())

		var format ExportFormat
		switch fmtStr {
		case "j", "json":
			format = ExportJSON
		case "m", "md", "markdown":
			format = ExportMarkdown
		default:
			errorLine.SetText("[red]Format must be j or m[-]")
			return
		}

		dir = filepath.Clean(dir)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			errorLine.SetText("[red]Directory does not exist: " + tview.Escape(dir) + "[-]")
			return
		}

		// Load messages for any convos that only have metadata.
		for i, c := range selected {
			if len(c.Messages) == 0 {
				msgs, err := loadMessages(c.ChatIdentifier)
				if err == nil {
					selected[i].Messages = msgs
				}
			}
		}

		written, err := exportConvos(selected, format, dir)
		dismiss()
		if err != nil {
			ui.setStatus("[red]Export error: " + tview.Escape(err.Error()) + "[-]")
		} else {
			ui.setStatus(fmt.Sprintf("[green]Exported %d file(s) to %s[-]", len(written), tview.Escape(dir)))
		}
	}

	capInput := func(input *tview.InputField, next func()) *tview.InputField {
		input.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
			if event.Key() == tcell.KeyEscape {
				dismiss()
				return nil
			}
			if event.Key() == tcell.KeyTab {
				next()
				return nil
			}
			return event
		})
		input.SetDoneFunc(func(key tcell.Key) {
			if key == tcell.KeyEnter {
				if focused == 1 {
					doExport()
				} else {
					next()
				}
			}
		})
		return input
	}

	capInput(fmtInput, func() {
		focused = 1
		ui.app.SetFocus(dirInput)
	})
	capInput(dirInput, func() {
		focused = 0
		ui.app.SetFocus(fmtInput)
	})
	_ = fields

	info := tview.NewTextView().
		SetText(fmt.Sprintf("  Export %d conversation(s)  [TAB to switch fields, ENTER on dir to confirm, ESC cancel]", len(selected))).
		SetTextColor(tcell.ColorGray)

	box := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tview.NewTextView().SetDynamicColors(true).SetText("[::b]  Export Conversations[-]"), 1, 0, false).
		AddItem(info, 1, 0, false).
		AddItem(fmtInput, 1, 0, true).
		AddItem(dirInput, 1, 0, false).
		AddItem(errorLine, 1, 0, false)
	box.SetBorder(true).SetBorderColor(tcell.ColorTeal)

	ui.pages.AddPage("export-prompt", box, true, true)
	ui.app.SetFocus(fmtInput)
}

// ── Delete confirmation ───────────────────────────────────────────────────────

func (ui *UI) showConfirmDelete(targets []int) {
	convos := ui.currentConvos()
	var sb strings.Builder
	verb := "Delete"
	if ui.activeTab == tabSpam && ui.spamMode == modePending {
		verb = "Unsubscribe + delete"
	}
	fmt.Fprintf(&sb, "%s %d conversation(s):\n\n", verb, len(targets))
	for _, idx := range targets {
		if idx >= len(convos) {
			continue
		}
		c := convos[idx]
		if ui.activeTab == tabSpam && ui.spamMode == modePending {
			fmt.Fprintf(&sb, "  • %s  [reply: %s  %d msg(s)]\n",
				tview.Escape(c.Label()), c.Keyword, c.Count)
		} else {
			fmt.Fprintf(&sb, "  • %s\n", tview.Escape(c.Label()))
		}
		sample := c.Sample
		if c.Confirm != "" {
			sample = c.Confirm
		}
		fmt.Fprintf(&sb, "    %s\n\n", tview.Escape(trunc(sample, 80)))
	}

	infoView := tview.NewTextView().SetText(sb.String())
	errorLine := tview.NewTextView().SetDynamicColors(true)
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

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tview.NewTextView().SetDynamicColors(true).SetText("[::b]  Confirm Deletion[-]"), 1, 0, false).
		AddItem(infoView, 0, 1, false).
		AddItem(errorLine, 1, 0, false).
		AddItem(input, 1, 0, true)
	layout.SetBorder(true).SetBorderColor(tcell.ColorRed)

	ui.pages.AddPage("confirm", layout, true, true)
	ui.app.SetFocus(input)
}

// ── Blocked contact management ────────────────────────────────────────────────

func (ui *UI) syncBlockedList() {
	phones := make([]string, 0, len(ui.ignored))
	for p := range ui.ignored {
		phones = append(phones, p)
	}
	sort.Strings(phones)
	ui.blockedList = phones
}

func (ui *UI) doUnblock(targets []int) {
	for _, idx := range targets {
		if idx < len(ui.blockedList) {
			delete(ui.ignored, ui.blockedList[idx])
		}
	}
	if err := saveIgnored(ui.ignored); err != nil {
		ui.setStatus("[red]Could not save block list: " + tview.Escape(err.Error()) + "[-]")
		return
	}
	ui.syncBlockedList()
	ui.selected = make(map[int]bool)
	ui.setStatus(fmt.Sprintf("[green]Unblocked %d contact(s).[-]", len(targets)))
	ui.renderBlockedTable()
}

func (ui *UI) showBlockImportPrompt() {
	input := tview.NewInputField().
		SetLabel("Import JSON file path: ").
		SetFieldWidth(50)
	errorLine := tview.NewTextView().SetDynamicColors(true)

	dismiss := func() {
		ui.pages.RemovePage("block-import")
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
			path := filepath.Clean(strings.TrimSpace(input.GetText()))
			phones, err := importBlockList(path)
			if err != nil {
				errorLine.SetText("[red]" + tview.Escape(err.Error()) + "[-]")
				return
			}
			added := 0
			for _, p := range phones {
				if _, exists := ui.ignored[p]; !exists {
					ui.ignored[p] = struct{}{}
					added++
				}
			}
			if err := saveIgnored(ui.ignored); err != nil {
				errorLine.SetText("[red]" + tview.Escape(err.Error()) + "[-]")
				return
			}
			ui.syncBlockedList()
			dismiss()
			ui.renderBlockedTable()
			ui.setStatus(fmt.Sprintf("[green]Imported %d new contact(s).[-]", added))
		}
	})

	box := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tview.NewTextView().SetDynamicColors(true).SetText("[::b]  Import Block List[-]"), 1, 0, false).
		AddItem(input, 1, 0, true).
		AddItem(errorLine, 1, 0, false).
		AddItem(tview.NewTextView().SetText("  File must be a JSON array of phone strings. ESC to cancel.").SetTextColor(tcell.ColorGray), 1, 0, false)
	box.SetBorder(true)

	ui.pages.AddPage("block-import", box, true, true)
	ui.app.SetFocus(input)
}

func (ui *UI) showBlockExportPrompt() {
	defaultPath := filepath.Join(os.Getenv("HOME"), "blocked-contacts.json")
	input := tview.NewInputField().
		SetLabel("Export to file: ").
		SetFieldWidth(50).
		SetText(defaultPath)
	errorLine := tview.NewTextView().SetDynamicColors(true)

	dismiss := func() {
		ui.pages.RemovePage("block-export")
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
			path := filepath.Clean(strings.TrimSpace(input.GetText()))
			if err := exportBlockList(ui.blockedList, path); err != nil {
				errorLine.SetText("[red]" + tview.Escape(err.Error()) + "[-]")
				return
			}
			dismiss()
			ui.setStatus("[green]Block list exported to " + tview.Escape(path) + "[-]")
		}
	})

	box := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tview.NewTextView().SetDynamicColors(true).SetText("[::b]  Export Block List[-]"), 1, 0, false).
		AddItem(input, 1, 0, true).
		AddItem(errorLine, 1, 0, false)
	box.SetBorder(true)

	ui.pages.AddPage("block-export", box, true, true)
	ui.app.SetFocus(input)
}

// ── Actions ───────────────────────────────────────────────────────────────────

func (ui *UI) doUnsubscribe(targets []int) {
	ui.showWorking("Sending unsubscribe replies…")
	go func() {
		var results []result
		var succeeded []int
		for _, idx := range targets {
			c := ui.spamPending[idx]
			err := sendUnsubscribe(c.Phone, c.Keyword)
			ok := err == nil
			msg := fmt.Sprintf("'%s' sent", c.Keyword)
			if !ok {
				msg = "FAILED: " + err.Error()
			} else {
				succeeded = append(succeeded, idx)
			}
			results = append(results, result{c.Label(), ok, msg})
		}
		ui.app.QueueUpdateDraw(func() {
			ui.pages.RemovePage("working")
			ui.removeConvosFromCurrent(succeeded)
			ui.showResults(results)
		})
	}()
}

func (ui *UI) doDelete(targets []int) {
	ui.showWorking("Processing…")
	go func() {
		var results []result
		for _, idx := range targets {
			convos := ui.currentConvos()
			if idx >= len(convos) {
				continue
			}
			c := convos[idx]
			var parts []string
			allOK := true

			if ui.activeTab == tabSpam && ui.spamMode == modePending {
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

			results = append(results, result{c.Label(), allOK, strings.Join(parts, "  ")})
		}
		ui.app.QueueUpdateDraw(func() {
			ui.pages.RemovePage("working")
			ui.removeConvosFromCurrent(targets)
			ui.showResults(results)
		})
	}()
}

func (ui *UI) doBlock(targets []int) {
	convos := ui.currentConvos()
	for _, idx := range targets {
		if idx < len(convos) {
			ui.ignored[convos[idx].Phone] = struct{}{}
		}
	}
	if err := saveIgnored(ui.ignored); err != nil {
		ui.setStatus("[red]Warning: could not save block list: " + tview.Escape(err.Error()) + "[-]")
	} else {
		ui.setStatus(fmt.Sprintf("[green]Blocked %d contact(s).[-]", len(targets)))
	}
	ui.syncBlockedList()
	ui.removeConvosFromCurrent(targets)
}

func (ui *UI) doRefresh() {
	ui.setStatus("[yellow]Refreshing…[-]")
	go func() {
		switch ui.activeTab {
		case tabSpam:
			pending, done, err := ui.getSpam()
			ui.app.QueueUpdateDraw(func() {
				if err != nil {
					ui.setStatus("[red]Refresh error: " + tview.Escape(err.Error()) + "[-]")
					return
				}
				ui.spamPending = applyContacts(pending, ui.contacts)
				ui.spamDone = applyContacts(done, ui.contacts)
				ui.selected = make(map[int]bool)
				ui.renderTable()
				ui.setStatus(fmt.Sprintf("[green]Refreshed — %d pending, %d done.[-]", len(pending), len(done)))
			})
		case tabAll, tabSearch:
			all, err := ui.getAll()
			ui.app.QueueUpdateDraw(func() {
				if err != nil {
					ui.setStatus("[red]Refresh error: " + tview.Escape(err.Error()) + "[-]")
					return
				}
				ui.allConvos = applyContacts(all, ui.contacts)
				ui.selected = make(map[int]bool)
				ui.renderTable()
				ui.setStatus(fmt.Sprintf("[green]Refreshed — %d conversation(s).[-]", len(all)))
			})
		}
	}()
}

// ── Shared helpers ────────────────────────────────────────────────────────────

func (ui *UI) showWorking(msg string) {
	view := tview.NewTextView().
		SetText("\n\n  " + msg).
		SetTextAlign(tview.AlignCenter)
	view.SetBorder(true)
	ui.pages.AddPage("working", view, true, true)
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
		if len(ui.currentConvos()) == 0 && ui.activeTab != tabAll {
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

func (ui *UI) removeConvosFromCurrent(indices []int) {
	if len(indices) == 0 {
		return
	}
	convos := ui.currentConvos()
	removed := make(map[int]bool, len(indices))
	for _, idx := range indices {
		removed[idx] = true
	}
	newConvos := make([]Convo, 0, len(convos)-len(removed))
	newSelected := make(map[int]bool)
	newIdx := 0
	for oldIdx, c := range convos {
		if removed[oldIdx] {
			continue
		}
		if ui.selected[oldIdx] {
			newSelected[newIdx] = true
		}
		newConvos = append(newConvos, c)
		newIdx++
	}
	ui.setCurrentConvos(newConvos)
	ui.selected = newSelected
	ui.renderTable()
}
