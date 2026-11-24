package main

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const appleEpoch = 978307200 // 2001-01-01T00:00:00Z in Unix seconds

func toAppleNs(t time.Time) int64 { return (t.Unix() - appleEpoch) * 1_000_000_000 }

func fromAppleNs(ns int64) time.Time {
	return time.Unix(ns/1_000_000_000+appleEpoch, ns%1_000_000_000).Local()
}

// Store reads chat.db live. The database is opened read-only with
// query_only set, so nothing here can modify your messages.
type Store struct {
	db       *sql.DB
	cfg      Config
	contacts *Contacts
	exclude  map[string]bool
	// optional columns, present only on newer macOS versions
	hasEdited, hasRetracted bool
}

func openStore(cfg Config, contacts *Contacts) (*Store, error) {
	path, err := cfg.chatDBPath()
	if err != nil {
		return nil, err
	}
	// mode=ro opens the file read-only; WAL is still honoured, so we see
	// messages as soon as Messages.app writes them.
	dsn := "file:" + path + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w (does this binary have Full Disk Access?)", path, err)
	}
	s := &Store{db: db, cfg: cfg, contacts: contacts, exclude: map[string]bool{}}
	for _, e := range cfg.ExcludeChats {
		s.exclude[strings.ToLower(strings.TrimSpace(e))] = true
	}
	cols, err := s.columns("message")
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("read schema: %w (does this binary have Full Disk Access?)", err)
	}
	s.hasEdited, s.hasRetracted = cols["date_edited"], cols["date_retracted"]
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) columns(table string) (map[string]bool, error) {
	rows, err := s.db.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			out[n] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("table %q not found", table)
	}
	return out, rows.Err()
}

// cutoff is the oldest time any tool may return.
func (s *Store) cutoff() time.Time {
	if s.cfg.MaxAgeDays <= 0 {
		return time.Unix(appleEpoch, 0)
	}
	return time.Now().AddDate(0, 0, -s.cfg.MaxAgeDays)
}

// ---------------------------------------------------------------------------
// Threads
//
// Messages keeps separate chat rows per service (iMessage vs SMS) for the same
// person. We merge rows that share a chat_identifier into one "thread" whose
// ID is the lowest chat ROWID.

type Thread struct {
	ID           int64
	Identifier   string
	Name         string
	IsGroup      bool
	Participants []string
	RowIDs       []int64
}

var shortCodeRe = regexp.MustCompile(`^\d{3,6}$`)

func (s *Store) threads(ctx context.Context) (map[int64]*Thread, error) {
	parts := map[int64][]string{}
	prow, err := s.db.QueryContext(ctx, `
		SELECT chj.chat_id, h.id FROM chat_handle_join chj
		JOIN handle h ON h.ROWID = chj.handle_id`)
	if err != nil {
		return nil, err
	}
	for prow.Next() {
		var id int64
		var h string
		if prow.Scan(&id, &h) == nil {
			parts[id] = append(parts[id], h)
		}
	}
	prow.Close()

	rows, err := s.db.QueryContext(ctx, `
		SELECT ROWID, COALESCE(chat_identifier,''), COALESCE(display_name,''), COALESCE(style,0)
		FROM chat ORDER BY ROWID`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byIdent := map[string]*Thread{}
	byRow := map[int64]*Thread{}
	for rows.Next() {
		var id int64
		var ident, display string
		var style int
		if rows.Scan(&id, &ident, &display, &style) != nil {
			continue
		}
		t := byIdent[ident]
		if t == nil {
			t = &Thread{ID: id, Identifier: ident, IsGroup: style == 43}
			byIdent[ident] = t
		}
		if display != "" {
			t.Name = display
		}
		t.RowIDs = append(t.RowIDs, id)
		for _, h := range parts[id] {
			if !contains(t.Participants, h) {
				t.Participants = append(t.Participants, h)
			}
		}
		byRow[id] = t
	}

	visible := map[int64]*Thread{}
	for id, t := range byRow {
		if t.Name == "" {
			t.Name = s.threadName(t)
		}
		if s.hidden(t) {
			continue
		}
		visible[id] = t
	}
	return visible, rows.Err()
}

func (s *Store) threadName(t *Thread) string {
	if !t.IsGroup {
		return s.contacts.Label(t.Identifier)
	}
	var names []string
	for i, p := range t.Participants {
		if i == 4 {
			names = append(names, fmt.Sprintf("+%d more", len(t.Participants)-4))
			break
		}
		names = append(names, s.contacts.Label(p))
	}
	if len(names) == 0 {
		return "Group chat"
	}
	return strings.Join(names, ", ")
}

func (s *Store) hidden(t *Thread) bool {
	if s.cfg.HideShortCodes && !t.IsGroup && shortCodeRe.MatchString(t.Identifier) {
		return true
	}
	for _, k := range []string{t.Identifier, t.Name, s.contacts.Name(t.Identifier)} {
		if k != "" && s.exclude[strings.ToLower(k)] {
			return true
		}
	}
	return false
}

// uniqueThreads collapses the rowid→thread map to one entry per thread.
func uniqueThreads(m map[int64]*Thread) []*Thread {
	seen := map[int64]bool{}
	var out []*Thread
	for _, t := range m {
		if !seen[t.ID] {
			seen[t.ID] = true
			out = append(out, t)
		}
	}
	return out
}

// ChatSummary is returned by list_chats.
type ChatSummary struct {
	ChatID       int64    `json:"chat_id"`
	Name         string   `json:"name"`
	IsGroup      bool     `json:"is_group"`
	Participants []string `json:"participants,omitempty"`
	LastMessage  string   `json:"last_message_time"`
	Preview      string   `json:"last_message_preview,omitempty"`
}

func (s *Store) ListChats(ctx context.Context, query string, since time.Time, limit int) ([]ChatSummary, error) {
	ts, err := s.threads(ctx)
	if err != nil {
		return nil, err
	}
	if c := s.cutoff(); since.Before(c) {
		since = c
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT chat_id, MAX(message_date) FROM chat_message_join
		WHERE message_date > ? GROUP BY chat_id`, toAppleNs(since))
	if err != nil {
		return nil, err
	}
	last := map[int64]int64{} // thread ID → latest date
	for rows.Next() {
		var id, d int64
		if rows.Scan(&id, &d) != nil {
			continue
		}
		if t := ts[id]; t != nil && d > last[t.ID] {
			last[t.ID] = d
		}
	}
	rows.Close()

	q := strings.ToLower(strings.TrimSpace(query))
	var list []*Thread
	for _, t := range uniqueThreads(ts) {
		if _, ok := last[t.ID]; !ok {
			continue
		}
		if q != "" && !s.threadMatches(t, q) {
			continue
		}
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool { return last[list[i].ID] > last[list[j].ID] })
	if len(list) > limit {
		list = list[:limit]
	}

	out := make([]ChatSummary, 0, len(list))
	for _, t := range list {
		cs := ChatSummary{
			ChatID:      t.ID,
			Name:        t.Name,
			IsGroup:     t.IsGroup,
			LastMessage: fromAppleNs(last[t.ID]).Format(time.RFC3339),
		}
		for _, p := range t.Participants {
			cs.Participants = append(cs.Participants, s.participantLabel(p))
		}
		if msgs, err := s.messages(ctx, msgQuery{threads: []*Thread{t}, limit: 1}); err == nil && len(msgs) > 0 {
			m := msgs[0]
			cs.Preview = m.From + ": " + truncate(m.Text, 120)
		}
		out = append(out, cs)
	}
	return out, nil
}

func (s *Store) participantLabel(h string) string {
	if n := s.contacts.Name(h); n != "" {
		return n + " <" + h + ">"
	}
	return h
}

func (s *Store) threadMatches(t *Thread, q string) bool {
	if strings.Contains(strings.ToLower(t.Name), q) || strings.Contains(strings.ToLower(t.Identifier), q) {
		return true
	}
	qd := normalizePhone(q)
	for _, p := range t.Participants {
		if strings.Contains(strings.ToLower(p), q) || strings.Contains(strings.ToLower(s.contacts.Name(p)), q) {
			return true
		}
		if len(qd) >= 7 && strings.Contains(normalizePhone(p), qd) {
			return true
		}
	}
	return false
}

// ResolveThreads turns a chat_id and/or a contact query into threads.
func (s *Store) ResolveThreads(ctx context.Context, chatID int64, contact string) ([]*Thread, error) {
	ts, err := s.threads(ctx)
	if err != nil {
		return nil, err
	}
	if chatID != 0 {
		t := ts[chatID]
		if t == nil {
			return nil, fmt.Errorf("no visible chat with chat_id %d", chatID)
		}
		return []*Thread{t}, nil
	}
	q := strings.ToLower(strings.TrimSpace(contact))
	if q == "" {
		return uniqueThreads(ts), nil
	}
	// "messages with Alice" means the 1:1 thread, not every group she's in.
	// Fall back to group chats only when no 1:1 thread matches.
	var direct, groups []*Thread
	for _, t := range uniqueThreads(ts) {
		if !s.threadMatches(t, q) {
			continue
		}
		if t.IsGroup {
			groups = append(groups, t)
		} else {
			direct = append(direct, t)
		}
	}
	out := direct
	if len(out) == 0 {
		out = groups
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no chat matches %q; try list_chats", contact)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Messages

type Message struct {
	ID          int64    `json:"id"`
	ChatID      int64    `json:"chat_id"`
	Chat        string   `json:"chat,omitempty"`
	Time        string   `json:"time"`
	From        string   `json:"from"`
	Text        string   `json:"text"`
	Attachments []string `json:"attachments,omitempty"`
	Edited      bool     `json:"edited,omitempty"`
	Unsent      bool     `json:"unsent,omitempty"`
	Reaction    bool     `json:"reaction,omitempty"`
}

type msgQuery struct {
	threads          []*Thread
	after, before    time.Time
	limit            int
	includeReactions bool
	includeChatName  bool
	// terms, when set, must all appear (case-insensitive) in the text.
	terms []string
}

const maxTextLen = 4000

// messages returns matching messages, newest first.
func (s *Store) messages(ctx context.Context, q msgQuery) ([]Message, error) {
	if len(q.threads) == 0 || q.limit <= 0 {
		return nil, nil
	}
	after := q.after
	if c := s.cutoff(); after.Before(c) {
		after = c
	}
	before := q.before
	if before.IsZero() {
		before = time.Now().Add(24 * time.Hour)
	}

	threadOf := map[int64]*Thread{}
	var ids []string
	for _, t := range q.threads {
		for _, r := range t.RowIDs {
			threadOf[r] = t
			ids = append(ids, strconv.FormatInt(r, 10))
		}
	}

	extra := ", 0, 0"
	if s.hasEdited && s.hasRetracted {
		extra = ", COALESCE(m.date_edited,0), COALESCE(m.date_retracted,0)"
	}
	where := []string{
		"cmj.chat_id IN (" + strings.Join(ids, ",") + ")", // integers only; safe to inline
		"m.date > ?", "m.date < ?",
		"COALESCE(m.item_type,0) = 0", // skip group renames, member changes, etc.
	}
	if !q.includeReactions {
		where = append(where, "COALESCE(m.associated_message_type,0) = 0")
	}
	if len(q.terms) > 0 {
		// Rows whose text is only in attributedBody can't be filtered in SQL,
		// so let them through and filter after decoding.
		where = append(where, "(m.text LIKE ? ESCAPE '\\' OR m.text IS NULL OR m.text = '')")
	}

	sqlText := `
		SELECT m.ROWID, cmj.chat_id, m.text, m.attributedBody, m.is_from_me, m.date,
		       COALESCE(h.id,''), COALESCE(m.cache_has_attachments,0),
		       COALESCE(m.associated_message_type,0)` + extra + `
		FROM message m
		JOIN chat_message_join cmj ON cmj.message_id = m.ROWID
		LEFT JOIN handle h ON h.ROWID = m.handle_id
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY m.date DESC`
	args := []any{toAppleNs(after), toAppleNs(before)}
	if len(q.terms) > 0 {
		args = append(args, "%"+likeEscape(q.terms[0])+"%")
	} else {
		sqlText += " LIMIT " + strconv.Itoa(q.limit)
	}

	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Message
	var withAttach []int64
	seen := map[int64]bool{}
	for rows.Next() && len(out) < q.limit {
		var (
			id, chatID, date, edited, retracted int64
			text                                sql.NullString
			body                                []byte
			fromMe, hasAttach, assocType        int
			handle                              string
		)
		if err := rows.Scan(&id, &chatID, &text, &body, &fromMe, &date, &handle, &hasAttach, &assocType, &edited, &retracted); err != nil {
			return nil, err
		}
		if seen[id] {
			continue
		}
		seen[id] = true

		t := text.String
		if strings.TrimSpace(t) == "" && len(body) > 0 {
			t = decodeAttributedBody(body)
		}
		t = cleanText(t)
		if len(q.terms) > 0 && !matchesAll(t, q.terms) {
			continue
		}
		if s.cfg.RedactCodes {
			t = redactCodes(t)
		}

		m := Message{
			ID:       id,
			ChatID:   threadOf[chatID].ID,
			Time:     fromAppleNs(date).Format(time.RFC3339),
			Text:     truncate(t, maxTextLen),
			Edited:   edited > 0,
			Unsent:   retracted > 0,
			Reaction: assocType != 0,
		}
		if fromMe != 0 {
			m.From = "me"
		} else if handle != "" {
			m.From = s.contacts.Label(handle)
		} else {
			m.From = s.contacts.Label(threadOf[chatID].Identifier)
		}
		if q.includeChatName {
			m.Chat = threadOf[chatID].Name
		}
		if hasAttach != 0 {
			withAttach = append(withAttach, id)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(withAttach) > 0 {
		s.fillAttachments(ctx, out, withAttach)
	}
	return out, nil
}

func (s *Store) fillAttachments(ctx context.Context, msgs []Message, ids []int64) {
	strs := make([]string, len(ids))
	for i, id := range ids {
		strs[i] = strconv.FormatInt(id, 10)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT maj.message_id, COALESCE(a.transfer_name, a.filename, ''), COALESCE(a.mime_type, '')
		FROM message_attachment_join maj JOIN attachment a ON a.ROWID = maj.attachment_id
		WHERE maj.message_id IN (`+strings.Join(strs, ",")+`)`)
	if err != nil {
		return
	}
	defer rows.Close()
	byID := map[int64][]string{}
	for rows.Next() {
		var id int64
		var name, mime string
		if rows.Scan(&id, &name, &mime) != nil {
			continue
		}
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		label := name
		if mime != "" {
			label += " (" + mime + ")"
		}
		byID[id] = append(byID[id], label)
	}
	for i := range msgs {
		msgs[i].Attachments = byID[msgs[i].ID]
	}
}

// GetMessages returns messages in chronological order.
func (s *Store) GetMessages(ctx context.Context, threads []*Thread, after, before time.Time, limit int, reactions bool) ([]Message, error) {
	msgs, err := s.messages(ctx, msgQuery{
		threads: threads, after: after, before: before, limit: limit,
		includeReactions: reactions, includeChatName: len(threads) > 1,
	})
	if err != nil {
		return nil, err
	}
	reverse(msgs)
	return msgs, nil
}

// Search returns messages containing every term, newest first.
func (s *Store) Search(ctx context.Context, threads []*Thread, terms []string, after, before time.Time, limit int) ([]Message, error) {
	return s.messages(ctx, msgQuery{
		threads: threads, after: after, before: before, limit: limit,
		terms: terms, includeChatName: true,
	})
}

// ---------------------------------------------------------------------------
// helpers

var (
	codeContextRe = regexp.MustCompile(`(?i)\b(code|passcode|verification|verify|otp|one[- ]time|2fa|two[- ]factor|security|login|log in|sign[- ]in|pin)\b`)
	codeDigitsRe  = regexp.MustCompile(`\b\d{4,8}\b|\b\d{3}[- ]\d{3}\b`)
)

func redactCodes(t string) string {
	if !codeContextRe.MatchString(t) {
		return t
	}
	return codeDigitsRe.ReplaceAllString(t, "[redacted]")
}

func cleanText(t string) string {
	// U+FFFC is the inline placeholder Messages uses for attachments.
	t = strings.ReplaceAll(t, "￼", "")
	return strings.TrimSpace(t)
}

func matchesAll(text string, terms []string) bool {
	lt := strings.ToLower(text)
	for _, term := range terms {
		if !strings.Contains(lt, term) {
			return false
		}
	}
	return true
}

func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
