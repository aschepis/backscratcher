package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	fmt.Print("Loading contacts… ")
	contacts := loadContacts()
	fmt.Printf("done (%d names).\n", len(contacts.names))

	fmt.Print("Scanning iMessage database… ")

	ignored, err := loadIgnored()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nWarning: could not load ignore list: %v\n", err)
		ignored = make(map[string]struct{})
	}

	pending, done, err := readDB(ignored)
	if err != nil {
		fmt.Fprintln(os.Stderr, "\nError:", err)
		msg := err.Error()
		if strings.Contains(msg, "permission") || strings.Contains(msg, "Operation not permitted") {
			fmt.Fprintln(os.Stderr, "\nGrant Full Disk Access to your terminal:")
			fmt.Fprintln(os.Stderr, "  System Settings > Privacy & Security > Full Disk Access")
		}
		os.Exit(1)
	}

	all, err := readAllConvos(ignored)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nWarning: could not load all conversations: %v\n", err)
		all = nil
	}

	fmt.Printf("done (%d spam, %d total).\n", len(pending)+len(done), len(all))

	runUI(
		pending, done, all,
		contacts, ignored,
		func() ([]Convo, []Convo, error) {
			return readDB(ignored)
		},
		func() ([]Convo, error) {
			return readAllConvos(ignored)
		},
	)

	fmt.Println("\nDone.")
}
