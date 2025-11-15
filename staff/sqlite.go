// Package main imports SQLite with FTS5 support enabled
//
// This file ensures the SQLite driver is compiled with FTS5 (Full Text Search 5) support
// which is required by the memory store.
package main

import (
	// Import SQLite driver - FTS5 is included by default in modern versions
	_ "github.com/mattn/go-sqlite3"
)
