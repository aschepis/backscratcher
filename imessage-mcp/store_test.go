package main

import (
	"context"
	"database/sql"
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

func fixture(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chat.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE handle (ROWID INTEGER PRIMARY KEY, id TEXT, service TEXT)`,
		`CREATE TABLE chat (ROWID INTEGER PRIMARY KEY, guid TEXT, chat_identifier TEXT, display_name TEXT, style INTEGER, service_name TEXT)`,
		`CREATE TABLE chat_handle_join (chat_id INTEGER, handle_id INTEGER)`,
		`CREATE TABLE message (ROWID INTEGER PRIMARY KEY, guid TEXT, text TEXT, attributedBody BLOB, handle_id INTEGER, is_from_me INTEGER, date INTEGER, item_type INTEGER, associated_message_type INTEGER, cache_has_attachments INTEGER, date_edited INTEGER, date_retracted INTEGER)`,
		`CREATE TABLE chat_message_join (chat_id INTEGER, message_id INTEGER, message_date INTEGER)`,
		`CREATE TABLE attachment (ROWID INTEGER PRIMARY KEY, filename TEXT, mime_type TEXT, transfer_name TEXT)`,
		`CREATE TABLE message_attachment_join (message_id INTEGER, attachment_id INTEGER)`,
		`INSERT INTO handle VALUES (1,'+15551234567','iMessage'),(2,'friend@example.com','iMessage'),(3,'12345','SMS')`,
		// Same person over iMessage (1) and SMS (2) should merge into one thread.
		`INSERT INTO chat VALUES (1,'a','+15551234567','',45,'iMessage'),(2,'b','+15551234567','',45,'SMS'),(3,'c','chat999','Trip',43,'iMessage'),(4,'d','12345','',45,'SMS'),(5,'e','secret@example.com','',45,'iMessage')`,
		`INSERT INTO chat_handle_join VALUES (1,1),(2,1),(3,1),(3,2),(4,3)`,
		`INSERT INTO attachment VALUES (1,'~/Library/Messages/Attachments/x/IMG_1.heic','image/heic','IMG_1.heic')`,
		`INSERT INTO message_attachment_join VALUES (4,1)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	now := time.Now()
	add := func(id, chat, handle int64, fromMe int, ago time.Duration, text any, body []byte, assoc, attach int) {
		d := toAppleNs(now.Add(-ago))
		if _, err := db.Exec(`INSERT INTO message VALUES (?,?,?,?,?,?,?,0,?,?,0,0)`, id, "g", text, body, handle, fromMe, d, assoc, attach); err != nil {
			t.Fatal(err)
		}
		db.Exec(`INSERT INTO chat_message_join VALUES (?,?,?)`, chat, id, d)
	}
	add(1, 1, 1, 0, 5*time.Hour, "Dinner tonight?", nil, 0, 0)
	add(2, 2, 1, 1, 4*time.Hour, nil, typedstreamBlob("Sure, pizza at 7"), 0, 0)
	add(3, 1, 1, 0, 3*time.Hour, `Loved "Sure, pizza at 7"`, nil, 2000, 0)
	add(4, 3, 2, 0, 2*time.Hour, "￼check this out", nil, 0, 1)
	add(5, 4, 3, 0, 1*time.Hour, "Your verification code is 482913", nil, 0, 0)
	add(6, 1, 1, 0, 400*24*time.Hour, "ancient pizza", nil, 0, 0)
	add(7, 5, 0, 0, 1*time.Hour, "hidden", nil, 0, 0)
	add(8, 1, 1, 0, 30*time.Minute, "Your login code: 1234", nil, 0, 0)
	db.Close()

	cfg := defaultConfig()
	cfg.ChatDB = path
	cfg.ExcludeChats = []string{"secret@example.com"}
	c := &Contacts{phones: map[string]string{"5551234567": "Alice"}, emails: map[string]string{}}
	s, err := openStore(cfg, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestListChats(t *testing.T) {
	s := fixture(t)
	chats, err := s.ListChats(context.Background(), "", time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, c := range chats {
		names = append(names, c.Name)
	}
	// Alice's two service chats merge; short code and excluded chat are hidden.
	if len(chats) != 2 || chats[0].Name != "Alice" || chats[1].Name != "Trip" {
		t.Fatalf("chats = %v", names)
	}
	if !strings.Contains(chats[0].Preview, "[redacted]") {
		t.Errorf("preview not redacted: %q", chats[0].Preview)
	}
	if got, _ := s.ListChats(context.Background(), "friend@", time.Time{}, 10); len(got) != 1 || got[0].Name != "Trip" {
		t.Errorf("participant search: %+v", got)
	}
}

func TestGetMessages(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	th, err := s.ResolveThreads(ctx, 0, "alice")
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := s.GetMessages(ctx, th, time.Time{}, time.Time{}, 50, false)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, m := range msgs {
		texts = append(texts, m.From+": "+m.Text)
	}
	want := []string{"Alice: Dinner tonight?", "me: Sure, pizza at 7", "Alice: Your login code: [redacted]"}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", texts)
	}
	msgs, _ = s.GetMessages(ctx, th, time.Time{}, time.Time{}, 50, true)
	if len(msgs) != 4 || !msgs[2].Reaction {
		t.Errorf("with reactions: %+v", msgs)
	}
	if _, err := s.ResolveThreads(ctx, 5, ""); err == nil {
		t.Error("excluded chat should not resolve")
	}
}

func TestSearch(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	th, _ := s.ResolveThreads(ctx, 0, "")
	msgs, err := s.Search(ctx, th, []string{"pizza"}, time.Time{}, time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	// attributedBody-only message is found; the 400-day-old one is past max_age_days.
	if len(msgs) != 1 || msgs[0].Text != "Sure, pizza at 7" || msgs[0].Chat != "Alice" {
		t.Fatalf("got %+v", msgs)
	}
	msgs, _ = s.Search(ctx, th, []string{"check"}, time.Time{}, time.Time{}, 10)
	if len(msgs) != 1 || msgs[0].Text != "check this out" || len(msgs[0].Attachments) != 1 || msgs[0].Attachments[0] != "IMG_1.heic (image/heic)" {
		t.Fatalf("attachment: %+v", msgs)
	}
}
