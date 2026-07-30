package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
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
	fmt.Println("done.")

	if len(pending) == 0 && len(done) == 0 {
		fmt.Printf("No unsubscribe-pattern messages found in the last %d days.\n", lookbackDays)
		return
	}

	if len(pending) > 0 {
		fmt.Printf("Found %d conversation(s) with pending opt-out instructions.\n", len(pending))
		runUI(pending, modePending, ignored, func() ([]Convo, error) {
			p, _, e := readDB(ignored)
			return p, e
		})
	}

	if len(done) > 0 {
		fmt.Printf("\nFound %d already-unsubscribed conversation(s).\n", len(done))
		runUI(done, modeDone, ignored, func() ([]Convo, error) {
			_, d, e := readDB(ignored)
			return d, e
		})
	}

	fmt.Println("\nDone.")
}
