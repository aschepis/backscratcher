package main

import (
	"database/sql"
	"io"
	"os"
	"regexp"
	"strings"

	_ "modernc.org/sqlite"
)

// ContactCache maps normalized phone numbers to display names.
type ContactCache struct {
	names map[string]string // normalized digits → display name
}

var digitRe = regexp.MustCompile(`\D`)

// normalizePhone strips all non-digit characters and removes a leading "1"
// for North American numbers so "+1 (555) 123-4567" and "5551234567" match.
func normalizePhone(phone string) string {
	digits := digitRe.ReplaceAllString(phone, "")
	if len(digits) == 11 && strings.HasPrefix(digits, "1") {
		digits = digits[1:]
	}
	return digits
}

// Name returns the contact display name for phone, or phone itself if unknown.
func (c *ContactCache) Name(phone string) string {
	if c == nil {
		return phone
	}
	norm := normalizePhone(phone)
	if name, ok := c.names[norm]; ok {
		return name
	}
	return phone
}

// loadContacts queries the macOS AddressBook SQLite database and returns a
// ContactCache. Returns an empty cache (not nil) on any error so callers
// can always call .Name() safely.
func loadContacts() *ContactCache {
	home, err := os.UserHomeDir()
	if err != nil {
		return &ContactCache{names: map[string]string{}}
	}
	src := home + "/Library/Application Support/AddressBook/AddressBook-v22.abcddb"

	// Copy to temp — AddressBook holds a write lock on the live file.
	tmp, err := os.CreateTemp("", "stop2end-ab-*.db")
	if err != nil {
		return &ContactCache{names: map[string]string{}}
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	in, err := os.Open(src)
	if err != nil {
		return &ContactCache{names: map[string]string{}}
	}
	out, err := os.Create(tmpPath)
	if err != nil {
		in.Close()
		return &ContactCache{names: map[string]string{}}
	}
	_, copyErr := io.Copy(out, in)
	in.Close()
	out.Close()
	if copyErr != nil {
		return &ContactCache{names: map[string]string{}}
	}

	db, err := sql.Open("sqlite", "file:"+tmpPath+"?mode=ro")
	if err != nil {
		return &ContactCache{names: map[string]string{}}
	}
	defer db.Close()

	// Join person record to phone number records.
	rows, err := db.Query(`
		SELECT r.ZFIRSTNAME, r.ZLASTNAME, r.ZORGANIZATION, p.ZFULLNUMBER
		FROM ZABCDRECORD r
		JOIN ZABCDPHONENUMBER p ON p.ZOWNER = r.Z_PK
		WHERE p.ZFULLNUMBER IS NOT NULL
	`)
	if err != nil {
		// Table names may differ across macOS versions; return empty gracefully.
		return &ContactCache{names: map[string]string{}}
	}
	defer rows.Close()

	cache := &ContactCache{names: make(map[string]string)}
	for rows.Next() {
		var first, last, org, phone sql.NullString
		if err := rows.Scan(&first, &last, &org, &phone); err != nil {
			continue
		}
		if !phone.Valid || phone.String == "" {
			continue
		}
		name := buildName(first.String, last.String, org.String)
		if name == "" {
			continue
		}
		norm := normalizePhone(phone.String)
		if norm != "" {
			cache.names[norm] = name
		}
	}
	return cache
}

func buildName(first, last, org string) string {
	parts := make([]string, 0, 2)
	if first != "" {
		parts = append(parts, first)
	}
	if last != "" {
		parts = append(parts, last)
	}
	if len(parts) > 0 {
		return strings.Join(parts, " ")
	}
	return org
}
