"""Turn positioned PDF lines into headings, paragraphs, footnotes and contents."""

import re
import statistics
from collections import Counter
from dataclasses import dataclass

from manuscript.pdf_lines import Line, Page

HEADING_SIZE_RATIO = 1.1
SMALL_SIZE_RATIO = 0.9
SUBHEADING_MAX_CHARS = 100
INDENT_TOLERANCE = 5.0
PARAGRAPH_GAP_RATIO = 1.5
SHORT_LINE_RATIO = 0.85
FURNITURE_MIN_PAGES = 3
CONTENTS_MIN_ENTRIES = 5
CONTENTS_MIN_SHARE = 0.4
BULLETS = ("•", "◦", "▪", "–")

PAGE_NUMBER = r"(?:\d+|[ivxlcdm]+)"
PAGE_NUMBER_TOKEN = re.compile(rf"\b{PAGE_NUMBER}\b", re.IGNORECASE)
ENDS_WITH_PAGE_NUMBER = re.compile(rf"\s{PAGE_NUMBER}$", re.IGNORECASE)
NUMBERED_ITEM = re.compile(r"\d{1,3}\.\s")
NOTE_MARKER = re.compile(r"\[\^(\d+)\]")
NOTE_ID = re.compile(r"\[\^p\d+n\d+\]")
COMPOUND_WORD = re.compile(r"\b\w+-\w+\b")
TRAILING_WORD = re.compile(r"\w+$")
LEADING_WORD = re.compile(r"^\w+")


@dataclass(frozen=True)
class Block:
    kind: str
    text: str
    page: int
    level: int = 0


@dataclass(frozen=True)
class Layout:
    body_size: float
    margin: float
    text_right: float
    leading: float
    compounds: frozenset[str]

    def is_small(self, line: Line) -> bool:
        return line.size < self.body_size * SMALL_SIZE_RATIO

    def heading_level(self, line: Line) -> int:
        if line.size > self.body_size * HEADING_SIZE_RATIO:
            return 1
        if line.bold and not self.is_small(line) and len(line.text) < SUBHEADING_MAX_CHARS:
            return 2
        return 0

    def is_indented(self, line: Line) -> bool:
        return line.left > self.margin + INDENT_TOLERANCE

    def is_short(self, line: Line) -> bool:
        width = self.text_right - self.margin
        return line.right < self.margin + width * SHORT_LINE_RATIO

    def starts_paragraph(self, previous: Line, line: Line) -> bool:
        if line.text.startswith(BULLETS) or NUMBERED_ITEM.match(line.text):
            return True
        if line.top - previous.top > self.leading * PARAGRAPH_GAP_RATIO:
            return True
        if not self.is_indented(line):
            return False
        return not self.is_indented(previous) or self.is_short(previous)

    def continues_across_pages(self, previous: Line, line: Line) -> bool:
        if line.text.startswith(BULLETS):
            return False
        return previous.ends_with_wrap_hyphen or not self.is_indented(line)


def build_blocks(pages: list[Page]) -> list[Block]:
    pages = [_without_furniture(page, _furniture_keys(pages)) for page in pages]
    pages = [_with_page_scoped_note_ids(page) for page in pages]
    layout = _measure_layout(pages)
    builder = _BlockBuilder(layout)
    for page in pages:
        builder.add_page(page)
    return _place_footnotes_after_citations(builder.finish())


def _furniture_key(line: Line) -> str:
    return PAGE_NUMBER_TOKEN.sub("", line.text).strip()


def _furniture_keys(pages: list[Page]) -> set[str]:
    counts = Counter()
    for page in pages:
        edge_lines = page.lines[:1] + page.lines[-1:]
        counts.update({_furniture_key(line) for line in edge_lines})
    repeated = {key for key, count in counts.items() if count >= FURNITURE_MIN_PAGES}
    return repeated | {""}


def _without_furniture(page: Page, furniture: set[str]) -> Page:
    lines = list(page.lines)
    while lines and _furniture_key(lines[0]) in furniture:
        lines.pop(0)
    while lines and _furniture_key(lines[-1]) in furniture:
        lines.pop()
    return Page(index=page.index, height=page.height, lines=lines)


def _with_page_scoped_note_ids(page: Page) -> Page:
    scoped = rf"[^p{page.index + 1}n\1]"
    lines = [
        Line(**{**line.__dict__, "text": NOTE_MARKER.sub(scoped, line.text)})
        for line in page.lines
    ]
    return Page(index=page.index, height=page.height, lines=lines)


def _measure_layout(pages: list[Page]) -> Layout:
    lines = [line for page in pages for line in page.lines]
    size_weights = Counter()
    for line in lines:
        size_weights[line.size] += len(line.text)
    body_size = size_weights.most_common(1)[0][0]
    body_lines = [line for line in lines if line.size == body_size]
    gaps = [
        after.top - before.top
        for page in pages
        for before, after in zip(page.lines, page.lines[1:])
        if before.size == after.size == body_size
    ]
    compounds = {
        word.lower() for line in lines for word in COMPOUND_WORD.findall(line.text)
    }
    return Layout(
        body_size=body_size,
        margin=Counter(round(line.left) for line in body_lines).most_common(1)[0][0],
        text_right=max(line.right for line in body_lines),
        leading=statistics.median(gaps) if gaps else body_size * 1.3,
        compounds=frozenset(compounds),
    )


def _is_contents_page(page: Page) -> bool:
    entries = [line for line in page.lines if ENDS_WITH_PAGE_NUMBER.search(line.text)]
    enough = len(entries) >= CONTENTS_MIN_ENTRIES
    return enough and len(entries) >= len(page.lines) * CONTENTS_MIN_SHARE


class _BlockBuilder:
    def __init__(self, layout: Layout):
        self.layout = layout
        self.blocks: list[Block] = []
        self.open_lines: list[Line] = []
        self.open_level = 0
        self.open_page = 0

    def add_page(self, page: Page) -> None:
        page_number = page.index + 1
        if _is_contents_page(page):
            self._close()
            text = "\n".join(line.text for line in page.lines)
            self.blocks.append(Block(kind="contents", text=text, page=page_number))
            return
        body_lines, note_lines = self._split_off_footnotes(page.lines)
        if body_lines and self._continues_open_paragraph(body_lines[0]):
            self.open_lines.append(body_lines.pop(0))
        else:
            self._close()
        for line in body_lines:
            self._add_line(line, page_number)
        self.blocks.extend(_footnote_blocks(note_lines, page_number, self.layout.compounds))

    def finish(self) -> list[Block]:
        self._close()
        return self.blocks

    def _split_off_footnotes(self, lines: list[Line]) -> tuple[list[Line], list[Line]]:
        for position, line in enumerate(lines):
            if self.layout.is_small(line) and NOTE_ID.match(line.text):
                return lines[:position], lines[position:]
        return list(lines), []

    def _continues_open_paragraph(self, first_line: Line) -> bool:
        paragraph_is_open = bool(self.open_lines) and self.open_level == 0
        if not paragraph_is_open or self.layout.heading_level(first_line):
            return False
        return self.layout.continues_across_pages(self.open_lines[-1], first_line)

    def _add_line(self, line: Line, page_number: int) -> None:
        level = self.layout.heading_level(line)
        if self._starts_new_block(level, line):
            self._close()
            self.open_level, self.open_page = level, page_number
        self.open_lines.append(line)

    def _starts_new_block(self, level: int, line: Line) -> bool:
        if not self.open_lines or level != self.open_level:
            return True
        is_paragraph = level == 0
        return is_paragraph and self.layout.starts_paragraph(self.open_lines[-1], line)

    def _close(self) -> None:
        if self.open_lines:
            kind = "heading" if self.open_level else "paragraph"
            text = _join_lines(self.open_lines, self.layout.compounds)
            self.blocks.append(
                Block(kind=kind, text=text, page=self.open_page, level=self.open_level)
            )
        self.open_lines, self.open_level = [], 0


def _footnote_blocks(lines: list[Line], page_number: int, compounds: frozenset[str]) -> list[Block]:
    notes: list[list[Line]] = []
    for line in lines:
        if NOTE_ID.match(line.text) or not notes:
            notes.append([])
        notes[-1].append(line)
    blocks = []
    for note_lines in notes:
        text = _join_lines(note_lines, compounds)
        text = NOTE_ID.sub(lambda match: match.group() + ":", text, count=1)
        blocks.append(Block(kind="footnote", text=text, page=page_number))
    return blocks


def _join_lines(lines: list[Line], compounds: frozenset[str]) -> str:
    text = lines[0].text
    for previous, line in zip(lines, lines[1:]):
        if previous.ends_with_wrap_hyphen:
            text += _wrap_joiner(text, line.text, compounds) + line.text
        else:
            text += " " + line.text
    return text


def _wrap_joiner(before: str, after: str, compounds: frozenset[str]) -> str:
    left, right = TRAILING_WORD.search(before), LEADING_WORD.search(after)
    if left and right and f"{left.group()}-{right.group()}".lower() in compounds:
        return "-"
    return ""


def _place_footnotes_after_citations(blocks: list[Block]) -> list[Block]:
    footnotes = {
        NOTE_ID.match(block.text).group(): block
        for block in blocks if block.kind == "footnote"
    }
    placed: list[Block] = []
    for block in blocks:
        if block.kind == "footnote":
            continue
        placed.append(block)
        for note_id in NOTE_ID.findall(block.text):
            if note_id in footnotes:
                placed.append(footnotes.pop(note_id))
    return placed + list(footnotes.values())
