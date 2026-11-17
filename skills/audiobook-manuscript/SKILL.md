---
name: audiobook-manuscript
description: Turn a PDF of a book or article into a narration-ready audiobook manuscript for ElevenReader or ElevenLabs Studio. Use when someone wants to listen to a PDF, asks for an audiobook version, or complains that a read-aloud app reads the table of contents, footnotes, page headers or publisher boilerplate.
---

# Audiobook manuscript

Read-aloud apps narrate whatever is on the page: the copyright notice, the table of
contents, every running header, every footnote. A professional audiobook is an edited
performance text. Someone decided what the listener hears, turned tables into
sentences, and gave each chapter a spoken opening. Your job is to be that editor.

The narration itself happens elsewhere (ElevenReader, ElevenLabs Studio). You produce
the document they read from, plus notes on what you changed.

The work splits in two. Scripts do the mechanical part, which they do more reliably
than you would: pulling text out of the PDF, removing page furniture, rejoining
paragraphs, pairing footnotes with the sentences that cite them. You do the part that
needs judgment: what to cut, which footnotes matter, how a table should sound.

## Workflow

Paths below are relative to this skill's folder. Put the working files in a folder
next to the PDF, named after it, for example `sharp-audiobook/`.

### 1. Extract

```
uv run scripts/extract.py BOOK.pdf --workdir BOOK-audiobook/work
```

Without `uv`: `pip install pypdfium2`, then run the script with `python`.

This writes one Markdown file per section into `work/sections/`, an untouched copy of
each into `work/original/`, and `work/manifest.json`. It prints an outline with page
ranges, word counts and footnote counts. Leave `work/original/` alone: it is what your
edits are measured against.

### 2. Decide what the listener hears

Read `references/editorial-guide.md` before editing. Then:

- In `manifest.json`, fill in `title` and `author`, and set `keep` to `true` or
  `false` for every section, with a short `reason` for each cut. The table of
  contents is already marked as cut.
- Edit the kept files in `work/sections/` in place, with targeted edits. Do not
  retype or regenerate a section: the author's words must survive exactly, and a
  rewrite is where words silently go missing.
- Write `work/brief.md`, the narration brief described in the guide.

Skim every kept section in full. Extraction is good but not perfect, and the
problems (a table flattened into a run of numbers, two headings merged into one) are
only visible when you read the text.

### 3. Assemble

```
uv run scripts/assemble.py --workdir BOOK-audiobook/work --out BOOK-audiobook
```

This refuses to run until every section has a decision. It removes any footnotes you
left in place, then writes:

- `manuscript.md`, `manuscript.txt` and `manuscript.epub`: the same text in three
  formats. ElevenReader imports EPUB (which keeps chapter navigation) and TXT.
- `production-notes.md`: your brief, the length, every section cut and why, and every
  word that differs from the source.

It also prints passages a narrator would stumble on, such as web addresses or leftover
table debris. Fix each one in its section file and run assemble again. A warning can
stay if the passage genuinely reads well aloud.

### 4. Check and hand over

Read the edits list in `production-notes.md`. Every entry should be a change you meant
to make. Anything else is an accident: restore the original wording.

Then tell the person where the files are, what was cut and how to restore it (set
`keep` to `true` and assemble again), which footnotes became spoken asides, and the
listening time. Recommend the EPUB for ElevenReader.

## What the person can steer

Keep the controls few. These are the ones worth offering, and none needs asking up
front; use the defaults unless they have said otherwise.

| Choice | Default |
|---|---|
| Footnotes | Dropped, except substantive ones reworded as short asides |
| Appendices and back matter | Cut, and listed so they can be restored |
| Reading guidance in the text | Light: punctuation and rare emphasis only |
| Narrator preferences for the brief | Your recommendation |

If they ask for something outside these, such as abridging chapters or modernising the
language, say that this skill preserves the author's text and offer the closest
supported choice.

## Limits

- PDFs with a text layer only. A scanned PDF needs OCR first; extraction will produce
  little or nothing, which is the sign.
- Single-column layouts. Two-column pages come out interleaved.
