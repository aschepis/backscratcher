package main

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	lookbackDays     = 730 // 2 years; political spam accumulates across election cycles
	appleEpochOffset = 978307200 // seconds: Unix epoch → 2001-01-01 00:00 UTC
)

// Standard CTIA SMS opt-out keywords (case-insensitive in practice).
// Source: https://help.genesys.cloud/articles/sms-in-or-opt-out-keywords/
// To add a keyword: update both validKeywords and the two regex consts below.
var validKeywords = map[string]bool{
	"STOP": true, "STOPALL": true, "UNSUBSCRIBE": true,
	"REVOKE": true, "OPTOUT": true, "CANCEL": true,
	"END": true, "QUIT": true, "ARRET": true,
}

// kwAlt is the regex alternation of all opt-out keywords (non-capturing).
const kwAlt = `(?:STOP|STOPALL|UNSUBSCRIBE|REVOKE|OPTOUT|CANCEL|END|QUIT|ARRET)`

// kwCap is the same alternation but capturing, used to extract the keyword.
const kwCap = `(STOP|STOPALL|UNSUBSCRIBE|REVOKE|OPTOUT|CANCEL|END|QUIT|ARRET)`

var phoneRe = regexp.MustCompile(`^\+?[\d]{5,15}$`)

// All patterns are case-insensitive via (?i).
var spamRe = regexp.MustCompile(
	`(?i)` +
		// "STOP 2 quit", "STOP2End" — digit 2 standing in for "to"
		`\b` + kwAlt + `\s*2\s*\w+\b` +
		// "STOP to quit", "STOP to cancel" — word "to"
		`|\b` + kwAlt + `\s+to\s+\w+` +
		// verb + keyword: "reply STOP", "text STOP", "type STOP", "SMS STOP", …
		`|\b(?:reply|replying|text|texting|txt|send|type|typing|sms|msg)\s+` + kwAlt + `\b` +
		// "to opt out", "to unsubscribe", "to stop receiving/messages/…"
		`|\bto\s+opt.?out\b` +
		`|\bto\s+unsubscribe\b` +
		`|\bto\s+stop\s+(?:receiving|messages|texts|updates|alerts)\b` +
		// Boilerplate footers that appear on virtually every marketing/political SMS
		`|\bMsg.{0,5}Data.{0,5}Rates.{0,5}May.{0,5}Apply\b` +
		`|\bMsg\s+frequency\s+varies\b`,
)

var kwPatterns = []*regexp.Regexp{
	// "reply STOP", "text STOP", "type STOP", etc.
	regexp.MustCompile(`(?i)\b(?:reply|replying|text|texting|txt|send|type|typing|sms|msg)\s+` + kwCap + `\b`),
	// "STOP 2 quit", "STOP2End"
	regexp.MustCompile(`(?i)\b` + kwCap + `\s*2\s*\w+\b`),
	// "STOP to quit", "STOP to cancel"
	regexp.MustCompile(`(?i)\b` + kwCap + `\s+to\s+\w+`),
}

var confirmRe = regexp.MustCompile(
	`(?i)` +
		`\bunsubscribed\b` +
		`|\bopt.?ed.?out\b` +
		`|\bno longer\s+(?:receive|get)\b` +
		`|\bwill\s+not\s+(?:receive|send)\b` +
		`|\bremoved\s+from\b` +
		`|\bsuccessfully\s+(?:removed|unsubscribed|opted)\b` +
		`|\bSTOP\s+confirmed\b` +
		`|\bconfirmed.*\bcancel\b`,
)

// Message is one SMS/iMessage in a conversation.
type Message struct {
	FromMe bool
	Time   time.Time
	Text   string
}

// Convo represents a conversation.
// Keyword and Count are set for spam-pending; Confirm is set for spam-done.
// DisplayName is populated from Contacts when available.
type Convo struct {
	Phone          string
	ChatIdentifier string
	DisplayName    string    // from Contacts cache; empty if not found
	IsGroup        bool
	Sample         string
	Keyword        string
	Count          int
	Confirm        string
	LastDate       time.Time
	Messages       []Message // may be nil for convos loaded without full messages
}

// Label returns the display name if available, otherwise the phone number.
func (c Convo) Label() string {
	if c.DisplayName != "" {
		return c.DisplayName
	}
	return c.Phone
}

func chatDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return home + "/Library/Messages/chat.db", nil
}

func appleNsToTime(ns int64) time.Time {
	sec := ns/1_000_000_000 + appleEpochOffset
	nsec := ns % 1_000_000_000
	return time.Unix(sec, nsec).Local()
}

func validatePhone(p string) bool {
	return len(p) >= 5 && phoneRe.MatchString(p)
}

func extractKeyword(text string) string {
	for _, pat := range kwPatterns {
		m := pat.FindStringSubmatch(text)
		if len(m) > 1 && validKeywords[strings.ToUpper(m[1])] {
			return strings.ToUpper(m[1])
		}
	}
	return "STOP"
}

func trunc(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n]
	}
	return s
}

type rawMsg struct {
	text   string
	fromMe bool
	date   int64
}

type chatEntry struct {
	phone      string
	chatID     string
	messages   []rawMsg
}

func readDB(ignored map[string]struct{}) (pending, done []Convo, err error) {
	db, cleanup, err := openDBCopy()
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()

	cutoff := fmt.Sprintf(
		"(strftime('%%s',datetime('now','-%d days'))-strftime('%%s','2001-01-01'))*1000000000",
		lookbackDays,
	)
	rows, err := db.Query(fmt.Sprintf(`
		SELECT m.text, m.is_from_me, m.date, h.id, c.rowid, c.chat_identifier
		FROM message m
		JOIN handle h              ON m.handle_id  = h.rowid
		JOIN chat_message_join cmj ON cmj.message_id = m.rowid
		JOIN chat c                ON c.rowid       = cmj.chat_id
		WHERE m.date > %s
		ORDER BY m.date ASC
	`, cutoff))
	if err != nil {
		return nil, nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	chats := map[int64]*chatEntry{}
	for rows.Next() {
		var textNull sql.NullString
		var fromMe int
		var date, chatRowid int64
		var phone, chatID string
		if err := rows.Scan(&textNull, &fromMe, &date, &phone, &chatRowid, &chatID); err != nil {
			continue
		}
		if chats[chatRowid] == nil {
			chats[chatRowid] = &chatEntry{phone: phone, chatID: chatID}
		}
		text := ""
		if textNull.Valid {
			text = textNull.String
		}
		chats[chatRowid].messages = append(chats[chatRowid].messages, rawMsg{
			text: text, fromMe: fromMe != 0, date: date,
		})
	}

	for _, entry := range chats {
		phone := entry.phone
		if !validatePhone(phone) {
			continue
		}
		if _, ign := ignored[phone]; ign {
			continue
		}

		var msgs []Message
		var inbound, outbound []string

		for _, m := range entry.messages {
			msgs = append(msgs, Message{FromMe: m.fromMe, Time: appleNsToTime(m.date), Text: m.text})
			if m.fromMe {
				outbound = append(outbound, m.text)
			} else {
				inbound = append(inbound, m.text)
			}
		}

		var spamMsgs []string
		for _, t := range inbound {
			if spamRe.MatchString(t) {
				spamMsgs = append(spamMsgs, t)
			}
		}
		if len(spamMsgs) == 0 {
			continue
		}

		sentStop := false
		for _, t := range outbound {
			if validKeywords[strings.ToUpper(strings.TrimSpace(t))] {
				sentStop = true
				break
			}
		}

		lastDate := msgs[len(msgs)-1].Time

		if sentStop {
			var confirms []string
			for _, t := range inbound {
				if confirmRe.MatchString(t) {
					confirms = append(confirms, t)
				}
			}
			if len(confirms) > 0 {
				done = append(done, Convo{
					Phone:          phone,
					ChatIdentifier: entry.chatID,
					Sample:         trunc(spamMsgs[len(spamMsgs)-1], 80),
					Confirm:        trunc(confirms[len(confirms)-1], 80),
					LastDate:       lastDate,
					Messages:       msgs,
				})
			}
			continue
		}

		pending = append(pending, Convo{
			Phone:          phone,
			ChatIdentifier: entry.chatID,
			Sample:         trunc(spamMsgs[len(spamMsgs)-1], 80),
			Keyword:        extractKeyword(strings.Join(spamMsgs, " ")),
			Count:          len(spamMsgs),
			LastDate:       lastDate,
			Messages:       msgs,
		})
	}

	return pending, done, nil
}

// openDBCopy copies chat.db to a temp file and opens it read-only.
// Caller must close the returned *sql.DB and the cleanup func removes the temp file.
func openDBCopy() (*sql.DB, func(), error) {
	path, err := chatDBPath()
	if err != nil {
		return nil, nil, fmt.Errorf("home dir: %w", err)
	}
	tmp, err := os.CreateTemp("", "stop2end-*.db")
	if err != nil {
		return nil, nil, fmt.Errorf("temp file: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()

	src, err := os.Open(path)
	if err != nil {
		os.Remove(tmpPath)
		return nil, nil, fmt.Errorf("open chat.db: %w", err)
	}
	dst, err := os.Create(tmpPath)
	if err != nil {
		src.Close()
		os.Remove(tmpPath)
		return nil, nil, fmt.Errorf("write temp: %w", err)
	}
	_, copyErr := io.Copy(dst, src)
	src.Close()
	dst.Close()
	if copyErr != nil {
		os.Remove(tmpPath)
		return nil, nil, fmt.Errorf("copy chat.db: %w", copyErr)
	}

	db, err := sql.Open("sqlite", "file:"+tmpPath+"?mode=ro")
	if err != nil {
		os.Remove(tmpPath)
		return nil, nil, fmt.Errorf("open sqlite: %w", err)
	}
	return db, func() { db.Close(); os.Remove(tmpPath) }, nil
}

// readAllConvos returns every conversation in chat.db within the lookback window,
// ordered by most-recent message descending. Messages are not loaded; call
// loadMessages to populate them on demand.
func readAllConvos(ignored map[string]struct{}) ([]Convo, error) {
	db, cleanup, err := openDBCopy()
	if err != nil {
		return nil, err
	}
	defer cleanup()

	cutoff := fmt.Sprintf(
		"(strftime('%%s',datetime('now','-%d days'))-strftime('%%s','2001-01-01'))*1000000000",
		lookbackDays,
	)
	rows, err := db.Query(fmt.Sprintf(`
		SELECT
			c.chat_identifier,
			c.display_name,
			c.style,
			MAX(m.date) AS last_date,
			(SELECT text FROM message m2
			 JOIN chat_message_join cmj2 ON cmj2.message_id = m2.rowid
			 WHERE cmj2.chat_id = c.rowid AND m2.text IS NOT NULL AND m2.text != ''
			 ORDER BY m2.date DESC LIMIT 1) AS last_text
		FROM chat c
		JOIN chat_message_join cmj ON cmj.chat_id = c.rowid
		JOIN message m ON m.rowid = cmj.message_id
		WHERE m.date > %s
		GROUP BY c.rowid
		ORDER BY last_date DESC
	`, cutoff))
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var convos []Convo
	for rows.Next() {
		var chatID string
		var displayName sql.NullString
		var style int
		var lastDate int64
		var lastText sql.NullString
		if err := rows.Scan(&chatID, &displayName, &style, &lastDate, &lastText); err != nil {
			continue
		}
		phone := chatID
		if displayName.Valid && displayName.String != "" && style == 43 {
			// style 43 = group chat; chat_identifier is a UUID-like group ID
			phone = displayName.String
		}
		if _, ign := ignored[phone]; ign {
			continue
		}
		convos = append(convos, Convo{
			Phone:          phone,
			ChatIdentifier: chatID,
			IsGroup:        style == 43,
			Sample:         trunc(lastText.String, 80),
			LastDate:       appleNsToTime(lastDate),
		})
	}
	return convos, nil
}

// loadMessages fetches all messages for the given chat_identifier.
func loadMessages(chatIdentifier string) ([]Message, error) {
	db, cleanup, err := openDBCopy()
	if err != nil {
		return nil, err
	}
	defer cleanup()

	rows, err := db.Query(`
		SELECT m.text, m.is_from_me, m.date
		FROM message m
		JOIN chat_message_join cmj ON cmj.message_id = m.rowid
		JOIN chat c ON c.rowid = cmj.chat_id
		WHERE c.chat_identifier = ?
		ORDER BY m.date ASC
	`, chatIdentifier)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var textNull sql.NullString
		var fromMe int
		var date int64
		if err := rows.Scan(&textNull, &fromMe, &date); err != nil {
			continue
		}
		msgs = append(msgs, Message{
			FromMe: fromMe != 0,
			Time:   appleNsToTime(date),
			Text:   textNull.String,
		})
	}
	return msgs, nil
}
