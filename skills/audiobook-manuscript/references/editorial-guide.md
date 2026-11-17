# Editorial guide

The test for every decision: would a good human narrator, recording this book for a
publisher, say this aloud? If a listener would be confused, bored or jolted by it,
change how it is presented. Never change what the author says.

## The fidelity rule

The author's sentences are not yours to improve. Do not paraphrase, condense,
modernise spelling, fix grammar or soften anything. You may:

- cut whole sections that are not part of the listening experience;
- remove or reword things that only work on a page (below);
- add a small amount of spoken scaffolding, such as chapter openings and credits.

Every changed word appears in the production notes, so the person can audit you. Make
changes you would be comfortable explaining one by one.

## What to keep and what to cut

Keep the work itself: preface, foreword, introduction, chapters, conclusion, epilogue.
A preface by the author is nearly always worth hearing.

Cut by default, with a reason in the manifest:

- title page, copyright page, edition history, publisher address, ISBN;
- table of contents, lists of figures;
- acknowledgements, unless they are short and personal enough to be part of the book;
- appendices, endnotes, bibliography, further reading, index;
- notes about translation, reprinting or the publisher.

Use judgment at the edges. An appendix that the chapters lean on repeatedly, or a
short afterword, may deserve to stay. When unsure, cut it and say so: restoring a
section is one line in the manifest.

## Opening credits

A cut title page still leaves the listener needing to know what they are hearing.
Keep the front-matter section and replace its contents with opening credits: the
title, the subtitle if there is one, and the author. For example:

```
# From Dictatorship to Democracy

A Conceptual Framework for Liberation. By Gene Sharp.
```

Do not invent a narrator credit, a publisher line or a closing sign-off.

## Chapter and section headings

Give each chapter a spoken opening: `# Chapter One. Facing Dictatorships
Realistically`. The extractor joins a chapter's number and title into one line, so
this is usually a small edit. Match the book's own numbering style.

Subheadings inside a chapter stay as they are. They act as signposts in audio too.

If two headings were merged into one line, or a table's header row was mistaken for a
heading, fix it.

## Footnotes

Each footnote sits directly below the paragraph that cites it, as `[^p9n1]: ...`, with
a matching `[^p9n1]` marker in the text. Anything you leave in this form is removed
automatically at assembly, marker and all. So you only need to act on the ones worth
keeping.

Most footnotes are citations. Leave those alone and they disappear.

A footnote is substantive when it tells the listener something they need or would
enjoy: a definition of a term the book goes on to use, a caveat that changes the
meaning, a short story. Fold these into the text as an aside:

- place it where a narrator would naturally pause, usually the end of the sentence or
  paragraph;
- keep the author's own wording, trimmed to what works aloud, typically one to three
  sentences;
- delete the marker and the footnote's definition paragraph.

A long substantive footnote can be trimmed to its core. Trimming a footnote is the one
place where condensing is allowed, because the alternative is losing it entirely.

## Tables, figures and lists

A table arrives as a run of numbers, sometimes with its header row turned into a
heading. Rewrite it as sentences a listener can follow, saying each figure once. Check
the PDF page if the columns are ambiguous; the manifest gives page numbers.

Describe a figure only if the text depends on it. Otherwise remove the reference
("see Figure 3") or leave the sentence if it still makes sense.

Short bulleted lists read fine. A very long list, such as a catalogue running to
pages, belongs with the appendices: cut it and note it.

## Things that only work on a page

- Web and email addresses: remove, or say the name of the site if it matters.
- "Ibid.", "op. cit.", "pp. 12-14", "see page 43": remove or reword ("as discussed
  earlier").
- Editorial brackets: "[H]elp yourselves" becomes "Help yourselves".
- Spaced ellipses ". . ." become "…".
- Symbols a voice may mangle: write "percent" for "%" and "and" for "&" when the
  result is natural. Leave ordinary numbers and years alone; voices read them well.
- Stray fragments left by extraction, such as a page number or a broken word.

## Reading guidance

Modern voices take their cues from the text itself, so guidance means shaping the text
and nothing else. Do not insert bracketed directions such as "[pause]": most reader
apps will speak them. The exception is when the person says they will render with a
model that supports such tags.

What does work everywhere:

- A paragraph break gives a longer pause. Use one before a turn in the argument.
- A dash or an ellipsis slows a sentence at that point.
- A single capitalised word is stressed. Emphasis works by contrast, so use it for
  the one word a narrator would lean on, never two in a sentence, and only a handful
  of times in a chapter.

Add guidance only where you have a strong conviction that a flat reading would lose
the meaning, for instance a contrast the whole chapter turns on. Most paragraphs need
none. Guidance edits appear in the production notes like any other change.

## The narration brief

Write `work/brief.md` for whoever picks the voice. Keep it to about a page:

- **The book in a sentence**: what it is and how it argues.
- **Narrator**: the kind of voice that suits it (age, register, warmth, authority) and
  why. Describe the voice; do not name a specific product voice, since catalogues
  change.
- **Tone and pace**: for example measured and serious, slightly slower than
  conversational. Suggest a speed setting if the app offers one.
- **Pronunciations**: names and terms a voice may get wrong, with a plain respelling.
- **Anything unusual**: long quotations, lists, passages in another language.

If the person stated narrator preferences, follow them and note any tension with the
material.
