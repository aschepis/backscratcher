#!/usr/bin/env python3
"""stop2end — Batch unsubscribe from SMS spam with Stop2End/Stop2Stop opt-out patterns."""

import curses
import json
import os
import re
import shutil
import sqlite3
import subprocess
import sys
import tempfile
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Callable

DB_PATH = Path.home() / "Library" / "Messages" / "chat.db"
IGNORE_FILE = Path.home() / ".config" / "stop2end" / "ignored.json"
LOOKBACK_DAYS = 120

# Whitelist — the only values ever embedded in an osascript call
VALID_KEYWORDS = {"STOP", "END", "CANCEL", "QUIT", "UNSUBSCRIBE", "STOPALL"}

_PHONE_RE = re.compile(r"^\+?[\d]{5,15}$")

_SPAM_PATTERNS = [
    r"\bStop\s*2\s*End\b",
    r"\bStop\s*2\s*Stop\b",
    r"\bEnd\s*2\s*End\b",
    r"\breply\s+(?:STOP|END|CANCEL|QUIT|UNSUBSCRIBE|STOPALL)\b",
    r"\btext\s+(?:STOP|END|CANCEL|QUIT|UNSUBSCRIBE|STOPALL)\b",
    r"\btxt\s+(?:STOP|END|CANCEL|QUIT|UNSUBSCRIBE|STOPALL)\b",
    r"\bsend\s+(?:STOP|END|CANCEL|QUIT|UNSUBSCRIBE|STOPALL)\b",
    r"\bto\s+opt.?out\b",
    r"\bto\s+unsubscribe\b",
    r"\bto\s+stop\s+(?:receiving|messages|texts|updates|alerts)\b",
    r"\bSTOP\s*=\s*opt.?out\b",
]
_SPAM_RE = re.compile("|".join(_SPAM_PATTERNS), re.IGNORECASE)

_KW_PATTERNS = [
    re.compile(r"\b(?:reply|text|txt|send)\s+(STOP|END|CANCEL|QUIT|UNSUBSCRIBE|STOPALL)\b", re.IGNORECASE),
    re.compile(r"\b(STOP|END|CANCEL|QUIT|UNSUBSCRIBE|STOPALL)\s*2\s*\w+\b", re.IGNORECASE),
]

_CONFIRM_RE = re.compile(
    r"\bunsubscribed\b"
    r"|\bopt.?ed.?out\b"
    r"|\bno longer\s+(?:receive|get)\b"
    r"|\bwill\s+not\s+(?:receive|send)\b"
    r"|\bremoved\s+from\b"
    r"|\bsuccessfully\s+(?:removed|unsubscribed|opted)\b"
    r"|\bSTOP\s+confirmed\b"
    r"|\bconfirmed.*\bcancel\b",
    re.IGNORECASE,
)

_APPLE_EPOCH_OFFSET = 978307200  # seconds from Unix epoch to 2001-01-01 00:00 UTC


# ---------------------------------------------------------------------------
# Data classes
# ---------------------------------------------------------------------------

@dataclass
class Message:
    from_me: bool
    ts: datetime
    text: str


@dataclass
class SpamConvo:
    phone: str
    chat_identifier: str
    sample: str
    keyword: str
    count: int
    messages: list[Message] = field(default_factory=list)


@dataclass
class DoneConvo:
    phone: str
    chat_identifier: str
    sample: str
    confirm: str
    messages: list[Message] = field(default_factory=list)


# ---------------------------------------------------------------------------
# Ignore list
# ---------------------------------------------------------------------------

def load_ignored() -> set[str]:
    if IGNORE_FILE.exists():
        return set(json.loads(IGNORE_FILE.read_text()))
    return set()


def save_ignored(phones: set[str]) -> None:
    IGNORE_FILE.parent.mkdir(parents=True, exist_ok=True)
    IGNORE_FILE.write_text(json.dumps(sorted(phones), indent=2))


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _validate_phone(phone: str) -> bool:
    return bool(_PHONE_RE.match(phone)) and len(phone) >= 5


def _extract_keyword(text: str) -> str:
    for pat in _KW_PATTERNS:
        m = pat.search(text)
        if m:
            kw = m.group(1).upper()
            if kw in VALID_KEYWORDS:
                return kw
    return "STOP"


def _apple_ns_to_dt(ns: int) -> datetime:
    return datetime.fromtimestamp(
        ns / 1e9 + _APPLE_EPOCH_OFFSET, tz=timezone.utc
    ).astimezone()


# ---------------------------------------------------------------------------
# Database
# ---------------------------------------------------------------------------

def _read_db(ignored: set[str]) -> tuple[list[SpamConvo], list[DoneConvo]]:
    if not DB_PATH.exists():
        sys.exit(
            f"Cannot find {DB_PATH}\n"
            "Grant Full Disk Access to your terminal in:\n"
            "  System Settings > Privacy & Security > Full Disk Access"
        )
    tmp = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
    tmp.close()
    try:
        shutil.copy2(DB_PATH, tmp.name)
        return _query(tmp.name, ignored)
    finally:
        os.unlink(tmp.name)


def _query(db: str, ignored: set[str]) -> tuple[list[SpamConvo], list[DoneConvo]]:
    conn = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    cutoff_ns = (
        f"(strftime('%s', datetime('now', '-{LOOKBACK_DAYS} days')) "
        f"- strftime('%s', '2001-01-01')) * 1000000000"
    )
    cur.execute(f"""
        SELECT
            m.text,
            m.is_from_me,
            m.date,
            h.id               AS phone,
            c.rowid            AS chat_id,
            c.chat_identifier
        FROM message m
        JOIN handle h              ON m.handle_id = h.rowid
        JOIN chat_message_join cmj ON cmj.message_id = m.rowid
        JOIN chat c                ON c.rowid = cmj.chat_id
        WHERE m.date > {cutoff_ns}
        ORDER BY m.date ASC
    """)
    rows = cur.fetchall()
    conn.close()

    chats: dict[int, dict] = {}
    for row in rows:
        cid = row["chat_id"]
        if cid not in chats:
            chats[cid] = {
                "phone": row["phone"],
                "chat_identifier": row["chat_identifier"],
                "messages": [],
            }
        chats[cid]["messages"].append({
            "text": row["text"] or "",
            "from_me": bool(row["is_from_me"]),
            "date": row["date"],
        })

    spam: list[SpamConvo] = []
    done: list[DoneConvo] = []

    for cid, data in chats.items():
        phone = data["phone"]
        if not _validate_phone(phone) or phone in ignored:
            continue

        raw = data["messages"]
        msg_objs = [
            Message(from_me=m["from_me"], ts=_apple_ns_to_dt(m["date"]), text=m["text"])
            for m in raw
        ]
        inbound  = [m["text"] for m in raw if not m["from_me"]]
        outbound = [m["text"] for m in raw if m["from_me"]]

        spam_msgs = [t for t in inbound if _SPAM_RE.search(t)]
        if not spam_msgs:
            continue

        sent_stop = any(t.upper().strip() in VALID_KEYWORDS for t in outbound)
        if sent_stop:
            confirms = [t for t in inbound if _CONFIRM_RE.search(t)]
            if confirms:
                done.append(DoneConvo(
                    phone=phone,
                    chat_identifier=data["chat_identifier"],
                    sample=spam_msgs[-1][:80],
                    confirm=confirms[-1][:80],
                    messages=msg_objs,
                ))
            continue

        spam.append(SpamConvo(
            phone=phone,
            chat_identifier=data["chat_identifier"],
            sample=spam_msgs[-1][:80],
            keyword=_extract_keyword(" ".join(spam_msgs)),
            count=len(spam_msgs),
            messages=msg_objs,
        ))

    return spam, done


# ---------------------------------------------------------------------------
# AppleScript actions
# ---------------------------------------------------------------------------

def _run_osa(script: str) -> bool:
    return subprocess.run(["osascript", "-e", script], capture_output=True, text=True).returncode == 0


def send_unsubscribe(phone: str, keyword: str) -> bool:
    assert keyword in VALID_KEYWORDS
    assert _validate_phone(phone)
    return _run_osa(
        'tell application "Messages"\n'
        '    set svc to first service whose service type = SMS\n'
        f'    set bud to buddy "{phone}" of svc\n'
        f'    send "{keyword}" to bud\n'
        'end tell'
    )


def delete_conversation(chat_identifier: str) -> bool:
    safe = chat_identifier.replace('"', "")
    return _run_osa(
        'tell application "Messages"\n'
        f'    set tgt to (first chat whose name is "{safe}")\n'
        '    delete tgt\n'
        'end tell'
    )


# ---------------------------------------------------------------------------
# List mutation helper
# ---------------------------------------------------------------------------

def _remove_indices(lst: list, indices: list[int], selected: set[int]) -> None:
    """Remove items at indices (preserving order), update selected set in-place."""
    removed = set(indices)
    for idx in sorted(removed, reverse=True):
        lst.pop(idx)
    new_sel: set[int] = set()
    for s in selected:
        if s in removed:
            continue
        shift = sum(1 for r in removed if r < s)
        new_sel.add(s - shift)
    selected.clear()
    selected.update(new_sel)


# ---------------------------------------------------------------------------
# Curses drawing helpers
# ---------------------------------------------------------------------------

# Color pair IDs
_CP_CURSOR   = 1
_CP_SELECTED = 2
_CP_HEADER   = 3
_CP_DIM      = 4
_CP_STATUS   = 5


def _init_colors() -> None:
    curses.start_color()
    curses.use_default_colors()
    curses.init_pair(_CP_CURSOR,   curses.COLOR_BLACK, curses.COLOR_WHITE)
    curses.init_pair(_CP_SELECTED, curses.COLOR_GREEN, -1)
    curses.init_pair(_CP_HEADER,   curses.COLOR_CYAN,  -1)
    curses.init_pair(_CP_DIM,      curses.COLOR_WHITE, -1)
    curses.init_pair(_CP_STATUS,   curses.COLOR_YELLOW, -1)


def _safe_addstr(stdscr, row: int, col: int, text: str, attr: int = 0) -> None:
    h, w = stdscr.getmaxyx()
    if row < 0 or row >= h:
        return
    text = text[:w - col - 1]
    try:
        stdscr.addstr(row, col, text, attr)
    except curses.error:
        pass


def _draw_list(
    stdscr,
    convos: list,
    selected: set[int],
    cursor: int,
    offset: int,
    mode: str,
    status: str = "",
) -> None:
    stdscr.erase()
    h, w = stdscr.getmaxyx()

    title = "stop2end — SMS Spam Unsubscriber"
    count_str = f"{len(selected)}/{len(convos)} selected"
    _safe_addstr(stdscr, 0, 2, title, curses.A_BOLD)
    _safe_addstr(stdscr, 0, w - len(count_str) - 2, count_str, curses.A_DIM)

    if mode == "pending":
        hdr = f"  {'#':>3}  {'':3}  {'Sender':<16}  {'Kwd':<12}  {'Msgs':>4}  Preview"
    else:
        hdr = f"  {'#':>3}  {'':3}  {'Sender':<16}  Confirmed message"
    _safe_addstr(stdscr, 1, 0, hdr, curses.color_pair(_CP_HEADER) | curses.A_BOLD)
    _safe_addstr(stdscr, 2, 2, "─" * (w - 4))

    list_h = h - 5
    for i in range(min(list_h, len(convos) - offset)):
        idx = offset + i
        row = 3 + i
        c = convos[idx]
        is_cursor = idx == cursor
        is_sel = idx in selected

        box = "[x]" if is_sel else "[ ]"
        num = f"{idx + 1:>3}."

        if mode == "pending":
            preview = c.sample[:38].replace("\n", " ")
            line = f"  {num} {box}  {c.phone:<16}  {c.keyword:<12}  {c.count:>4}  \"{preview}\""
        else:
            conf = c.confirm[:52].replace("\n", " ")
            line = f"  {num} {box}  {c.phone:<16}  \"{conf}\""

        if is_cursor:
            _safe_addstr(stdscr, row, 0, line.ljust(w - 1), curses.color_pair(_CP_CURSOR))
        elif is_sel:
            _safe_addstr(stdscr, row, 0, line, curses.color_pair(_CP_SELECTED))
        else:
            _safe_addstr(stdscr, row, 0, line)

    _safe_addstr(stdscr, h - 3, 2, "─" * (w - 4))

    if status:
        _safe_addstr(stdscr, h - 2, 2, status, curses.color_pair(_CP_STATUS))
    elif mode == "pending":
        keys = "↑↓:nav  SPC:select  a:all  n:none  v:view  u:unsub  d:unsub+del  i:ignore  r:refresh  q:quit"
    else:
        keys = "↑↓:nav  SPC:select  a:all  n:none  v:view  d:delete  i:ignore  r:refresh  q:quit"
    if not status:
        _safe_addstr(stdscr, h - 2, 2, keys, curses.A_DIM)

    scroll_pct = ""
    if len(convos) > list_h:
        pct = int(100 * (offset + list_h / 2) / len(convos))
        scroll_pct = f" {pct}% "
    _safe_addstr(stdscr, h - 1, w - len(scroll_pct) - 1, scroll_pct, curses.A_REVERSE)

    stdscr.refresh()


def _word_wrap(text: str, width: int) -> list[str]:
    lines: list[str] = []
    current = ""
    for word in text.split():
        candidate = (current + " " + word).strip()
        if len(candidate) <= width:
            current = candidate
        else:
            if current:
                lines.append(current)
            current = word
    if current:
        lines.append(current)
    return lines or [""]


def _draw_thread(stdscr, convo) -> None:
    """Full-screen scrollable thread view. Any non-scroll key returns."""
    h, w = stdscr.getmaxyx()
    prefix_w = 22  # "  Jun 15 09:23   them  "
    text_w = max(20, w - prefix_w - 2)

    # Pre-render into (line_str, from_me|None) pairs
    rendered: list[tuple[str, bool | None]] = []
    for msg in convo.messages:
        if not msg.text.strip():
            continue
        ts = msg.ts.strftime("%b %d %H:%M")
        who = "    me" if msg.from_me else "  them"
        prefix = f"  {ts}  {who}  "
        indent = " " * len(prefix)
        wrapped = _word_wrap(msg.text.replace("\n", " "), text_w)
        rendered.append((prefix + wrapped[0], msg.from_me))
        for extra in wrapped[1:]:
            rendered.append((indent + extra, msg.from_me))
        rendered.append(("", None))  # blank separator

    # Start scrolled to the bottom
    visible = h - 4
    scroll = max(0, len(rendered) - visible)

    while True:
        stdscr.erase()
        h, w = stdscr.getmaxyx()
        visible = h - 4

        _safe_addstr(stdscr, 0, 2, f"Thread: {convo.phone}", curses.A_BOLD)
        _safe_addstr(stdscr, 1, 2, "─" * (w - 4))

        for i, (line, from_me) in enumerate(rendered[scroll: scroll + visible]):
            row = 2 + i
            attr = curses.A_DIM if from_me else 0
            _safe_addstr(stdscr, row, 0, line, attr)

        _safe_addstr(stdscr, h - 2, 2, "─" * (w - 4))
        if len(rendered) > visible:
            pos = f"[{scroll + 1}–{min(scroll + visible, len(rendered))}/{len(rendered)}]  "
        else:
            pos = ""
        _safe_addstr(stdscr, h - 1, 2, f"{pos}↑↓:scroll   any other key: return", curses.A_DIM)
        stdscr.refresh()

        key = stdscr.getch()
        if key in (curses.KEY_UP, ord("k")) and scroll > 0:
            scroll -= 1
        elif key in (curses.KEY_DOWN, ord("j")) and scroll < len(rendered) - visible:
            scroll += 1
        else:
            return


def _read_line(stdscr, row: int, col: int) -> str | None:
    """Read text input at (row, col). Returns str on Enter, None on ESC."""
    curses.curs_set(1)
    buf: list[str] = []

    while True:
        key = stdscr.getch()
        if key in (10, 13):
            curses.curs_set(0)
            return "".join(buf)
        elif key == 27:
            curses.curs_set(0)
            return None
        elif key in (curses.KEY_BACKSPACE, 127, 8):
            if buf:
                buf.pop()
                cy, cx = stdscr.getyx()
                _safe_addstr(stdscr, cy, cx - 1, " ")
                stdscr.move(cy, cx - 1)
                stdscr.refresh()
        elif 32 <= key < 127:
            buf.append(chr(key))
            _safe_addstr(stdscr, row, col + len(buf) - 1, chr(key))
            stdscr.refresh()


def _confirm_delete_screen(stdscr, convos: list, targets: list[int], mode: str) -> bool:
    """Overlay asking the user to type 'delete'. Returns True only on exact match."""
    stdscr.erase()
    h, w = stdscr.getmaxyx()

    verb = "Unsubscribe + delete" if mode == "pending" else "Delete"
    _safe_addstr(stdscr, 1, 2, f"{verb} {len(targets)} conversation(s):", curses.A_BOLD)
    _safe_addstr(stdscr, 2, 2, "─" * 52)
    for i, idx in enumerate(targets[: h - 10]):
        _safe_addstr(stdscr, 3 + i, 4, f"• {convos[idx].phone}")
    row = 4 + min(len(targets), h - 10)
    _safe_addstr(stdscr, row,     2, 'Type "delete" to confirm (ESC to cancel):')
    _safe_addstr(stdscr, row + 1, 4, "> ")
    stdscr.refresh()

    text = _read_line(stdscr, row + 1, 6)
    return text == "delete"


def _show_results(stdscr, results: list[tuple[str, bool, str]]) -> None:
    """Brief results screen; any key continues."""
    stdscr.erase()
    h, _ = stdscr.getmaxyx()
    _safe_addstr(stdscr, 0, 2, "Results:", curses.A_BOLD)
    _safe_addstr(stdscr, 1, 2, "─" * 52)
    for i, (phone, ok, msg) in enumerate(results[: h - 4]):
        mark = "✓" if ok else "✗"
        attr = curses.color_pair(_CP_SELECTED) if ok else curses.color_pair(_CP_STATUS)
        _safe_addstr(stdscr, 2 + i, 4, f"{mark} {phone}  {msg}", attr)
    _safe_addstr(stdscr, h - 1, 2, "Press any key to continue…", curses.A_DIM)
    stdscr.refresh()
    stdscr.getch()


# ---------------------------------------------------------------------------
# Main TUI event loop
# ---------------------------------------------------------------------------

def _tui_run(
    stdscr,
    get_convos: Callable[[], list],
    mode: str,
    ignored: set[str],
) -> None:
    curses.curs_set(0)
    _init_colors()

    convos = get_convos()
    selected: set[int] = set()
    cursor = 0
    offset = 0
    status = ""

    while True:
        if not convos:
            _safe_addstr(stdscr, 0, 2, "No conversations remaining. Press any key to exit.", curses.A_DIM)
            stdscr.refresh()
            stdscr.getch()
            break

        h, w = stdscr.getmaxyx()
        list_h = h - 5
        cursor = max(0, min(cursor, len(convos) - 1))
        if cursor < offset:
            offset = cursor
        elif cursor >= offset + list_h:
            offset = cursor - list_h + 1

        _draw_list(stdscr, convos, selected, cursor, offset, mode, status)
        status = ""

        key = stdscr.getch()

        # ── navigation ──
        if key in (curses.KEY_UP, ord("k")):
            cursor = max(0, cursor - 1)
            continue
        if key in (curses.KEY_DOWN, ord("j")):
            cursor = min(len(convos) - 1, cursor + 1)
            continue

        # ── selection ──
        if key == ord(" "):
            selected.discard(cursor) if cursor in selected else selected.add(cursor)
            cursor = min(len(convos) - 1, cursor + 1)
            continue
        if key == ord("a"):
            selected = set(range(len(convos)))
            continue
        if key == ord("n"):
            selected.clear()
            continue

        # ── view thread ──
        if key == ord("v"):
            _draw_thread(stdscr, convos[cursor])
            continue

        # ── refresh ──
        if key == ord("r"):
            convos = get_convos()
            selected.clear()
            cursor = 0
            offset = 0
            status = f"Refreshed — {len(convos)} conversation(s) found."
            continue

        # ── quit ──
        if key == ord("q"):
            break

        # ── actions require a selection ──
        if not selected:
            status = "Select items first (SPACE / a)."
            continue

        targets = sorted(selected)

        # ── unsubscribe only (pending) ──
        if key == ord("u") and mode == "pending":
            results = []
            for idx in targets:
                c = convos[idx]
                ok = send_unsubscribe(c.phone, c.keyword)
                results.append((c.phone, ok, f"'{c.keyword}' {'sent' if ok else 'FAILED'}"))
            _show_results(stdscr, results)
            done = [t for t, (_, ok, _) in zip(targets, results) if ok]
            _remove_indices(convos, done, selected)
            cursor = min(cursor, max(0, len(convos) - 1))
            continue

        # ── unsubscribe + delete (pending) or delete (done) ──
        if key == ord("d"):
            if not _confirm_delete_screen(stdscr, convos, targets, mode):
                status = "Cancelled."
                continue
            results = []
            for idx in targets:
                c = convos[idx]
                parts = []
                ok_unsub = True
                if mode == "pending":
                    ok_unsub = send_unsubscribe(c.phone, c.keyword)
                    parts.append(f"unsub {'ok' if ok_unsub else 'FAILED'}")
                ok_del = delete_conversation(c.chat_identifier)
                parts.append(f"del {'ok' if ok_del else 'FAILED'}")
                results.append((c.phone, ok_unsub and ok_del, "  ".join(parts)))
            _show_results(stdscr, results)
            _remove_indices(convos, targets, selected)
            cursor = min(cursor, max(0, len(convos) - 1))
            continue

        # ── ignore forever ──
        if key == ord("i"):
            for idx in targets:
                ignored.add(convos[idx].phone)
            save_ignored(ignored)
            _remove_indices(convos, targets, selected)
            cursor = min(cursor, max(0, len(convos) - 1))
            status = f"Added {len(targets)} to ignore list → {IGNORE_FILE}"
            continue


# ---------------------------------------------------------------------------
# /dev/tty trick — lets curses own the terminal even when stdio is redirected
# ---------------------------------------------------------------------------

def _run_curses(func, *args) -> None:
    try:
        tty_fd = os.open("/dev/tty", os.O_RDWR)
        s0, s1 = os.dup(0), os.dup(1)
        os.dup2(tty_fd, 0)
        os.dup2(tty_fd, 1)
        os.close(tty_fd)
        try:
            curses.wrapper(func, *args)
        finally:
            os.dup2(s0, 0)
            os.dup2(s1, 1)
            os.close(s0)
            os.close(s1)
    except OSError:
        curses.wrapper(func, *args)


def run_menu(get_convos: Callable[[], list], mode: str, ignored: set[str]) -> None:
    _run_curses(_tui_run, get_convos, mode, ignored)


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> None:
    print("Scanning iMessage database…")
    ignored = load_ignored()
    spam_convos, done_convos = _read_db(ignored)

    if not spam_convos and not done_convos:
        print(f"No unsubscribe-pattern messages found in the last {LOOKBACK_DAYS} days.")
        return

    if spam_convos:
        print(f"Found {len(spam_convos)} conversation(s) with pending opt-out instructions.")
        # get_convos re-reads the DB so 'r' always sees fresh data
        run_menu(lambda: _read_db(ignored)[0], "pending", ignored)

    if done_convos:
        print(f"\nFound {len(done_convos)} already-unsubscribed conversation(s).")
        run_menu(lambda: _read_db(ignored)[1], "done", ignored)

    print("\nDone.")


if __name__ == "__main__":
    main()
