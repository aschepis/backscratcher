"""Clean edited sections for narration and describe what changed."""

import difflib
import re

NOTE_MARKER_WITH_LEADING_SPACE = re.compile(r"[ \t]*\[\^[^\]\s]+\]")
NOTE_DEFINITION = re.compile(r"^\[\^[^\]\s]+\]:")
EDIT_CONTEXT_WORDS = 4
WORDS_PER_MINUTE = 150

UNSPEAKABLE = {
    "web address": re.compile(r"https?://|www\.", re.IGNORECASE),
    "email address": re.compile(r"\b[\w.+-]+@[\w-]+\.\w+"),
    "table or number debris": re.compile(r"^[\d\s.,%$-]{6,}$"),
    "undecoded PDF glyph": re.compile(r"\(cid:\d+\)"),
    "bracketed tag": re.compile(r"\[[a-z][a-z ]+\]"),
    "editorial bracket": re.compile(r"\[[A-Za-z]\]\w"),
    "spaced ellipsis": re.compile(r"\. \. \."),
    "citation shorthand": re.compile(r"\b(?:Ibid|op\. cit|pp?\.\s?\d)", re.IGNORECASE),
}


def paragraphs(text: str) -> list[str]:
    return [part.strip() for part in text.split("\n\n") if part.strip()]


def drop_footnotes(text: str) -> tuple[str, int]:
    kept = [part for part in paragraphs(text) if not NOTE_DEFINITION.match(part)]
    dropped = len(paragraphs(text)) - len(kept)
    cleaned = [NOTE_MARKER_WITH_LEADING_SPACE.sub("", part) for part in kept]
    return "\n\n".join(cleaned) + "\n", dropped


def find_unspeakable(text: str) -> list[str]:
    warnings = []
    for part in paragraphs(text):
        for problem, pattern in UNSPEAKABLE.items():
            if pattern.search(part):
                warnings.append(f"{problem}: {_shorten(part)}")
    return warnings


def _shorten(text: str, limit: int = 90) -> str:
    return text if len(text) <= limit else text[:limit].rstrip() + "…"


def describe_edits(original: str, edited: str) -> list[str]:
    before = drop_footnotes(original)[0].split()
    after = drop_footnotes(edited)[0].split()
    matcher = difflib.SequenceMatcher(None, before, after, autojunk=False)
    edits = []
    for operation, before_start, before_end, after_start, after_end in matcher.get_opcodes():
        if operation == "equal":
            continue
        lead = " ".join(before[max(before_start - EDIT_CONTEXT_WORDS, 0):before_start])
        tail = " ".join(before[before_end:before_end + EDIT_CONTEXT_WORDS])
        removed = " ".join(before[before_start:before_end])
        added = " ".join(after[after_start:after_end])
        change = " ".join(
            piece for piece in (
                f"[-{removed}-]" if removed else "",
                f"{{+{added}+}}" if added else "",
            ) if piece
        )
        edits.append(f"…{lead} {change} {tail}…")
    return edits


def listening_minutes(text: str) -> int:
    return round(len(text.split()) / WORDS_PER_MINUTE)
