# stop2end

A terminal UI for macOS that finds SMS spam asking you to "Reply STOP to quit"
(political texts, marketing blasts, short codes) and lets you unsubscribe from
it in bulk. It sends the opt-out keyword through Messages.app and can then
delete the conversation. It doubles as a read-only browser for every
conversation in your iMessage history, with search and export.

It runs locally on your Mac. There is no server and nothing to deploy beyond
installing the binary.

## Requirements

- macOS with Messages.app signed in. Your iPhone must have **Text Message
  Forwarding** enabled for this Mac, or SMS conversations will not be in
  `chat.db` and STOP replies cannot be sent as SMS.
- Go 1.22 or newer, to build it (`brew install go`).

## Install

From this directory:

```bash
go install .
```

That builds and puts `stop2end` in `$(go env GOPATH)/bin` (usually
`~/go/bin`). Make sure that directory is on your `PATH`.

Or build a binary in place:

```bash
go build -o stop2end .
```

The SQLite driver is pure Go (no cgo), so you can also cross-compile it on
another machine and copy the binary over:

```bash
GOOS=darwin GOARCH=arm64 go build -o stop2end .
```

Use `GOARCH=amd64` for an Intel Mac.

## macOS permissions

On first run, grant these to the terminal app you launch it from (Terminal,
iTerm2, Ghostty, …), not to the binary:

1. **Full Disk Access** (System Settings → Privacy & Security → Full Disk
   Access). Needed to read `~/Library/Messages/chat.db` and the Contacts
   database. Without it, stop2end exits with an "Operation not permitted"
   error. Restart the terminal after granting it.
2. **Automation → Messages**. macOS prompts for this the first time you
   unsubscribe or delete; click OK. If you denied it, re-enable it under
   System Settings → Privacy & Security → Automation.

## Usage

```bash
stop2end
```

It loads your contacts, scans the last two years of messages, and opens a
four-tab UI. Switch tabs with `1`–`4`.

| Tab | What it shows |
| --- | --- |
| 1 Spam | Conversations with opt-out language. `TAB` toggles between **pending** (you haven't replied STOP) and **done** (you replied and got a confirmation). |
| 2 All | Every conversation, newest first, with a preview of the last message. |
| 3 Search | Results of a full-text search (`/`). |
| 4 Blocked | Numbers you've hidden from stop2end. |

Common keys: `↑↓`/`jk` move, `SPACE` selects, `a`/`n` select all/none, `v`
opens the thread, `r` refreshes, `/` searches, `e` exports the selected
conversations to Markdown or JSON, `q` quits.

On the Spam tab (pending):

- `u` sends the detected keyword (STOP, END, CANCEL, …) to each selected number.
- `d` does the same, then deletes the conversation in Messages.app. The
  deletion syncs through iCloud.
- `b` blocks the number. It is only hidden from stop2end; nothing changes in
  Messages.

On the Blocked tab, `x`/`DEL` unblocks, `I` imports a block list, and `X`
exports one (a JSON array of phone numbers).

Unsubscribing sends real text messages and deleting cannot be undone, so check
the selection first.

## Files

- `~/.config/stop2end/ignored.json` is the block list.
- `chat.db` and the Contacts database are copied to a temp file and opened
  read-only. The live databases are never written to.

## Notes

- Since macOS Ventura, Messages often stores a message's text only in
  `message.attributedBody`, a typedstream-archived NSAttributedString, and
  leaves `message.text` NULL. stop2end decodes that blob
  (`typedstream.go`), so those messages are still detected and displayed.
- `stop2end.py` is the original Python/curses prototype. The Go version
  replaces it.

## Development

```bash
go vet ./...
```

```bash
go test ./...
```

The tests build a small fake `chat.db` in a temp directory and need no
access to your real data, so they also run on Linux.
