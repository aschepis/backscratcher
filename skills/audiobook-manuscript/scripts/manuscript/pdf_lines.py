"""Read a PDF into positioned text lines with font size and style."""

import ctypes
import math
import re
from collections import Counter
from dataclasses import dataclass
from pathlib import Path

import pypdfium2 as pdfium
import pypdfium2.raw as raw

LINE_BREAKS = "\r\n"
# PDFium reports a hyphen that only exists because of a line wrap as U+0002.
WRAP_HYPHEN = "\x02"
SUPERSCRIPT_RATIO = 0.75
SAME_ROW_OVERLAP = 0.5
ROW_GAP_TOLERANCE = 2.0
# PDFium sometimes omits the line break before a footnote marker.
CARRIAGE_RETURN_DISTANCE = 20.0
FONT_NAME_BYTES = 128
SPACE_BEFORE_MARKER = re.compile(r"(?<=\S)\s+(?=\[\^)")
REPEATED_SPACE = re.compile(r"\s{2,}")


@dataclass(frozen=True)
class Char:
    text: str
    size: float
    font: str
    left: float
    right: float
    top: float
    bottom: float


@dataclass(frozen=True)
class Line:
    text: str
    size: float
    bold: bool
    italic: bool
    left: float
    right: float
    top: float
    ends_with_wrap_hyphen: bool


@dataclass(frozen=True)
class Page:
    index: int
    height: float
    lines: list[Line]


@dataclass
class Run:
    chars: list[Char]
    ends_with_wrap_hyphen: bool = False


def read_pages(path: Path) -> list[Page]:
    document = pdfium.PdfDocument(path)
    return [_read_page(document[index], index) for index in range(len(document))]


def _read_page(page, index: int) -> Page:
    height = page.get_height()
    textpage = page.get_textpage()
    chars = [
        _read_char(textpage, char_index, height)
        for char_index in range(textpage.count_chars())
    ]
    rows = _merge_runs_sharing_a_row(_split_into_runs(chars))
    lines = sorted((_build_line(row) for row in rows), key=lambda line: line.top)
    return Page(index=index, height=height, lines=lines)


def _read_char(textpage, char_index: int, page_height: float) -> Char:
    left, bottom, right, top = textpage.get_charbox(char_index)
    return Char(
        text=chr(raw.FPDFText_GetUnicode(textpage, char_index)),
        size=_font_size(textpage, char_index),
        font=_font_name(textpage, char_index),
        left=left,
        right=right,
        top=page_height - top,
        bottom=page_height - bottom,
    )


def _font_size(textpage, char_index: int) -> float:
    matrix = raw.FS_MATRIX()
    raw.FPDFText_GetMatrix(textpage, char_index, ctypes.byref(matrix))
    vertical_scale = math.hypot(matrix.c, matrix.d)
    nominal = raw.FPDFText_GetFontSize(textpage, char_index)
    return round(nominal * vertical_scale, 1)


def _font_name(textpage, char_index: int) -> str:
    buffer = ctypes.create_string_buffer(FONT_NAME_BYTES)
    flags = ctypes.c_int()
    raw.FPDFText_GetFontInfo(
        textpage, char_index, buffer, FONT_NAME_BYTES, ctypes.byref(flags)
    )
    return buffer.value.decode("utf-8", errors="replace")


def _split_into_runs(chars: list[Char]) -> list[Run]:
    runs = [Run(chars=[])]
    for char in chars:
        if char.text == WRAP_HYPHEN:
            runs[-1].ends_with_wrap_hyphen = True
            runs.append(Run(chars=[]))
        elif char.text in LINE_BREAKS or _returns_to_line_start(runs[-1], char):
            runs.append(Run(chars=[] if char.text in LINE_BREAKS else [char]))
        else:
            runs[-1].chars.append(char)
    return [run for run in runs if _visible(run.chars)]


def _returns_to_line_start(run: Run, char: Char) -> bool:
    visible = _visible(run.chars)
    if char.text.isspace() or not visible:
        return False
    return char.left < visible[-1].left - CARRIAGE_RETURN_DISTANCE


def _merge_runs_sharing_a_row(runs: list[Run]) -> list[Run]:
    rows: list[Run] = []
    for run in runs:
        previous = rows[-1] if rows else None
        if previous and not previous.ends_with_wrap_hyphen and _share_a_row(previous, run):
            previous.chars.extend([_gap_after(previous), *run.chars])
            previous.ends_with_wrap_hyphen = run.ends_with_wrap_hyphen
        else:
            rows.append(run)
    return rows


def _share_a_row(first: Run, second: Run) -> bool:
    continues_rightwards = second.chars[0].left >= first.chars[-1].right - ROW_GAP_TOLERANCE
    if not continues_rightwards:
        return False
    first_top, first_bottom = _vertical_extent(first.chars)
    second_top, second_bottom = _vertical_extent(second.chars)
    overlap = min(first_bottom, second_bottom) - max(first_top, second_top)
    shorter = min(first_bottom - first_top, second_bottom - second_top)
    return shorter > 0 and overlap >= shorter * SAME_ROW_OVERLAP


def _vertical_extent(chars: list[Char]) -> tuple[float, float]:
    visible = _visible(chars)
    return min(char.top for char in visible), max(char.bottom for char in visible)


def _gap_after(run: Run) -> Char:
    last = run.chars[-1]
    return Char(" ", last.size, last.font, last.right, last.right, last.top, last.bottom)


def _visible(chars: list[Char]) -> list[Char]:
    return [char for char in chars if not char.text.isspace()]


def _build_line(run: Run) -> Line:
    markers = _note_marker_positions(run.chars)
    visible = _visible(run.chars)
    full_size = [
        char for position, char in enumerate(run.chars)
        if position not in markers and not char.text.isspace()
    ] or visible
    font = Counter(char.font for char in full_size).most_common(1)[0][0]
    return Line(
        text=_render_text(run.chars, markers),
        size=max(char.size for char in full_size),
        bold="Bold" in font,
        italic="Italic" in font or "Oblique" in font,
        left=min(char.left for char in visible),
        right=max(char.right for char in visible),
        top=min(char.top for char in visible),
        ends_with_wrap_hyphen=run.ends_with_wrap_hyphen,
    )


def _note_marker_positions(chars: list[Char]) -> set[int]:
    return {
        position
        for position, char in enumerate(chars)
        if char.text.isdigit() and _is_superscript(chars, position)
    }


def _is_superscript(chars: list[Char], position: int) -> bool:
    neighbour = _nearest_letter(chars[:position][::-1]) or _nearest_letter(chars[position + 1:])
    return neighbour is not None and chars[position].size < neighbour.size * SUPERSCRIPT_RATIO


def _nearest_letter(chars: list[Char]) -> Char | None:
    for char in chars:
        if not char.text.isspace() and not char.text.isdigit():
            return char
    return None


def _render_text(chars: list[Char], markers: set[int]) -> str:
    pieces = []
    for position, char in enumerate(chars):
        if position not in markers:
            pieces.append(char.text)
            continue
        if position - 1 not in markers:
            pieces.append("[^")
        pieces.append(char.text)
        if position + 1 not in markers:
            pieces.append("]")
    text = "".join(pieces).strip()
    text = SPACE_BEFORE_MARKER.sub("", text)
    return REPEATED_SPACE.sub(" ", text)
