"""Write the finished manuscript as plain text and as an EPUB."""

import re
import uuid
import zipfile
from dataclasses import dataclass
from datetime import datetime, timezone
from html import escape
from pathlib import Path

from manuscript.finalize import paragraphs

HEADING = re.compile(r"^(#+)\s+(.*)$")
LIST_ITEM = re.compile(r"^- ")
SENTENCE_END = (".", "!", "?", ":", "…", "”", '"')

CONTAINER_XML = """<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
"""

XHTML_PAGE = """<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="en">
<head><meta charset="utf-8"/><title>{title}</title></head>
<body>
{body}
</body>
</html>
"""


@dataclass(frozen=True)
class Chapter:
    title: str
    text: str


def to_plain_text(chapters: list[Chapter]) -> str:
    lines = []
    for chapter in chapters:
        for part in paragraphs(chapter.text):
            lines.append(_plain_paragraph(part))
    return "\n\n".join(lines) + "\n"


def _plain_paragraph(part: str) -> str:
    heading = HEADING.match(part)
    if heading:
        title = heading.group(2)
        return title if title.endswith(SENTENCE_END) else title + "."
    return LIST_ITEM.sub("", part)


def write_epub(path: Path, title: str, author: str, chapters: list[Chapter]) -> None:
    book_id = uuid.uuid5(uuid.NAMESPACE_URL, f"{title}|{author}")
    names = [f"chapter-{number:03d}.xhtml" for number in range(1, len(chapters) + 1)]
    with zipfile.ZipFile(path, "w") as epub:
        epub.writestr("mimetype", "application/epub+zip", compress_type=zipfile.ZIP_STORED)
        epub.writestr("META-INF/container.xml", CONTAINER_XML, zipfile.ZIP_DEFLATED)
        epub.writestr("OEBPS/content.opf", _package(title, author, book_id, names), zipfile.ZIP_DEFLATED)
        epub.writestr("OEBPS/nav.xhtml", _navigation(chapters, names), zipfile.ZIP_DEFLATED)
        for name, chapter in zip(names, chapters):
            page = XHTML_PAGE.format(title=escape(chapter.title), body=_chapter_body(chapter))
            epub.writestr(f"OEBPS/{name}", page, zipfile.ZIP_DEFLATED)


def _package(title: str, author: str, book_id: uuid.UUID, names: list[str]) -> str:
    items = "\n".join(
        f'    <item id="c{number}" href="{name}" media-type="application/xhtml+xml"/>'
        for number, name in enumerate(names, start=1)
    )
    spine = "\n".join(f'    <itemref idref="c{number}"/>' for number in range(1, len(names) + 1))
    return f"""<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="book-id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="book-id">urn:uuid:{book_id}</dc:identifier>
    <dc:title>{escape(title)}</dc:title>
    <dc:creator>{escape(author)}</dc:creator>
    <dc:language>en</dc:language>
    <meta property="dcterms:modified">{datetime.now(timezone.utc):%Y-%m-%dT%H:%M:%SZ}</meta>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
{items}
  </manifest>
  <spine>
{spine}
  </spine>
</package>
"""


def _navigation(chapters: list[Chapter], names: list[str]) -> str:
    links = "\n".join(
        f'    <li><a href="{name}">{escape(chapter.title)}</a></li>'
        for name, chapter in zip(names, chapters)
    )
    body = f'<nav epub:type="toc">\n  <h1>Chapters</h1>\n  <ol>\n{links}\n  </ol>\n</nav>'
    return XHTML_PAGE.format(title="Chapters", body=body)


def _chapter_body(chapter: Chapter) -> str:
    return "\n".join(_html_paragraph(part) for part in paragraphs(chapter.text))


def _html_paragraph(part: str) -> str:
    heading = HEADING.match(part)
    if heading:
        level = min(len(heading.group(1)), 6)
        return f"<h{level}>{escape(heading.group(2))}</h{level}>"
    return f"<p>{escape(LIST_ITEM.sub('', part))}</p>"
