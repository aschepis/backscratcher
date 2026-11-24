package main

import (
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Contacts maps phone numbers and emails to display names using the local
// macOS AddressBook databases. It refreshes itself periodically.
type Contacts struct {
	mu     sync.RWMutex
	phones map[string]string // normalized digits → name
	emails map[string]string // lowercased email → name
}

var nonDigit = regexp.MustCompile(`\D`)

// normalizePhone keeps the last 10 digits so "+1 (555) 123-4567",
// "15551234567" and "5551234567" all match. Shorter numbers (short codes,
// some international numbers) are kept as-is.
func normalizePhone(p string) string {
	d := nonDigit.ReplaceAllString(p, "")
	if len(d) > 10 {
		d = d[len(d)-10:]
	}
	return d
}

func newContacts() *Contacts {
	c := &Contacts{phones: map[string]string{}, emails: map[string]string{}}
	c.reload()
	return c
}

func (c *Contacts) refreshEvery(d time.Duration) {
	go func() {
		for range time.Tick(d) {
			c.reload()
		}
	}()
}

// Name returns the contact name for a handle (phone or email), or "" if unknown.
func (c *Contacts) Name(handle string) string {
	if handle == "" {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if strings.Contains(handle, "@") {
		return c.emails[strings.ToLower(handle)]
	}
	if n := normalizePhone(handle); n != "" {
		return c.phones[n]
	}
	return ""
}

// Label returns "Name" when known, else the raw handle.
func (c *Contacts) Label(handle string) string {
	if n := c.Name(handle); n != "" {
		return n
	}
	return handle
}

func (c *Contacts) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.phones) + len(c.emails)
}

func (c *Contacts) reload() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	base := filepath.Join(home, "Library", "Application Support", "AddressBook")
	paths := []string{filepath.Join(base, "AddressBook-v22.abcddb")}
	// Synced accounts (iCloud, Google, …) each have their own database here;
	// this is usually where the real contacts live.
	more, _ := filepath.Glob(filepath.Join(base, "Sources", "*", "AddressBook-v22.abcddb"))
	paths = append(paths, more...)

	phones := map[string]string{}
	emails := map[string]string{}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if err := loadAddressBook(p, phones, emails); err != nil {
			slog.Debug("addressbook read failed", "path", p, "err", err)
		}
	}

	c.mu.Lock()
	c.phones, c.emails = phones, emails
	c.mu.Unlock()
}

func loadAddressBook(path string, phones, emails map[string]string) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(3000)")
	if err != nil {
		return err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT r.ZFIRSTNAME, r.ZLASTNAME, r.ZORGANIZATION, p.ZFULLNUMBER
		FROM ZABCDRECORD r JOIN ZABCDPHONENUMBER p ON p.ZOWNER = r.Z_PK
		WHERE p.ZFULLNUMBER IS NOT NULL`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var first, last, org, val sql.NullString
		if rows.Scan(&first, &last, &org, &val) != nil {
			continue
		}
		if name := buildName(first.String, last.String, org.String); name != "" {
			if n := normalizePhone(val.String); n != "" {
				phones[n] = name
			}
		}
	}
	rows.Close()

	rows, err = db.Query(`
		SELECT r.ZFIRSTNAME, r.ZLASTNAME, r.ZORGANIZATION, e.ZADDRESS
		FROM ZABCDRECORD r JOIN ZABCDEMAILADDRESS e ON e.ZOWNER = r.Z_PK
		WHERE e.ZADDRESS IS NOT NULL`)
	if err != nil {
		return nil // phones loaded; emails table may be missing on some versions
	}
	defer rows.Close()
	for rows.Next() {
		var first, last, org, val sql.NullString
		if rows.Scan(&first, &last, &org, &val) != nil {
			continue
		}
		if name := buildName(first.String, last.String, org.String); name != "" {
			emails[strings.ToLower(strings.TrimSpace(val.String))] = name
		}
	}
	return nil
}

func buildName(first, last, org string) string {
	name := strings.TrimSpace(strings.TrimSpace(first) + " " + strings.TrimSpace(last))
	if name == "" {
		return strings.TrimSpace(org)
	}
	return name
}
