package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// typedstreamBlob builds an attributedBody blob shaped like the ones macOS writes.
func typedstreamBlob(s string) []byte {
	b := []byte("\x04\x0bstreamtyped\x81\xe8\x03\x84\x01@\x84\x84\x84\x12NSAttributedString\x00\x84\x84\x08NSObject\x00\x85\x92\x84\x84\x84\x08NSString\x01\x94\x84\x01+")
	n := len(s)
	if n < 0x80 {
		b = append(b, byte(n))
	} else {
		b = append(b, 0x81, byte(n), byte(n>>8))
	}
	b = append(b, s...)
	return append(b, "\x86\x84\x02iI\x01\x05\x92\x84\x84\x84\x0cNSDictionary\x00"...)
}

func TestDecodeAttributedBody(t *testing.T) {
	for _, s := range []string{"hi", "héllo 👋", strings.Repeat("long ", 60)} {
		if got := decodeAttributedBody(typedstreamBlob(s)); got != s {
			t.Errorf("decode(%q) = %q", s, got)
		}
	}
	if got := decodeAttributedBody([]byte("garbage")); got != "" {
		t.Errorf("garbage decoded to %q", got)
	}
}

func TestMessageText(t *testing.T) {
	null := sql.NullString{}
	cases := []struct {
		name string
		text sql.NullString
		body []byte
		want string
	}{
		{"text wins", sql.NullString{String: "plain", Valid: true}, typedstreamBlob("other"), "plain"},
		{"null text decodes body", null, typedstreamBlob("Reply STOP to quit"), "Reply STOP to quit"},
		{"empty text decodes body", sql.NullString{String: "", Valid: true}, typedstreamBlob("hello"), "hello"},
		{"strips U+FFFC", null, typedstreamBlob("￼photo caption"), "photo caption"},
		{"attachment only", null, typedstreamBlob("￼"), ""},
		{"nothing", null, nil, ""},
	}
	for _, c := range cases {
		if got := messageText(c.text, c.body); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestQueriesUseAttributedBody runs readDB, readAllConvos and loadMessages
// against a minimal chat.db where the messages carry only attributedBody.
func TestQueriesUseAttributedBody(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "Library", "Messages")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE handle (rowid INTEGER PRIMARY KEY, id TEXT)`,
		`CREATE TABLE chat (rowid INTEGER PRIMARY KEY, chat_identifier TEXT, display_name TEXT, style INTEGER)`,
		`CREATE TABLE message (rowid INTEGER PRIMARY KEY, text TEXT, attributedBody BLOB, is_from_me INTEGER, date INTEGER, handle_id INTEGER)`,
		`CREATE TABLE chat_message_join (chat_id INTEGER, message_id INTEGER)`,
		`INSERT INTO handle VALUES (1, '+15551234567')`,
		`INSERT INTO chat VALUES (1, '+15551234567', '', 45)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	appleNs := func(d time.Duration) int64 {
		return (time.Now().Add(-d).Unix() - appleEpochOffset) * 1_000_000_000
	}
	add := func(id int, text any, body []byte, ago time.Duration) {
		if _, err := db.Exec(`INSERT INTO message VALUES (?, ?, ?, 0, ?, 1)`, id, text, body, appleNs(ago)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO chat_message_join VALUES (1, ?)`, id); err != nil {
			t.Fatal(err)
		}
	}
	add(1, "Old plain text", nil, 3*time.Hour)
	add(2, nil, typedstreamBlob("Vote today! Reply STOP to quit"), 2*time.Hour)
	add(3, nil, typedstreamBlob("Latest ￼news"), time.Hour)
	db.Close()

	convos, err := readAllConvos(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(convos) != 1 || convos[0].Sample != "Latest news" {
		t.Fatalf("readAllConvos = %+v, want one convo with preview %q", convos, "Latest news")
	}

	msgs, err := loadMessages("+15551234567")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range msgs {
		got = append(got, m.Text)
	}
	want := []string{"Old plain text", "Vote today! Reply STOP to quit", "Latest news"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("loadMessages = %q, want %q", got, want)
	}

	pending, _, err := readDB(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Keyword != "STOP" {
		t.Fatalf("readDB pending = %+v, want the attributedBody-only spam detected", pending)
	}
}
