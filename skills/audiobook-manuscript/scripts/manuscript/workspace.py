"""Split blocks into sections and keep them on disk for the editing pass."""

import json
import re
import shutil
from dataclasses import dataclass
from pathlib import Path

from manuscript.structure import BULLETS, NOTE_ID, Block

MANIFEST_NAME = "manifest.json"
SECTIONS_DIR = "sections"
ORIGINAL_DIR = "original"
FRONT_MATTER_TITLE = "Front matter"
CONTENTS_TITLE = "Table of contents"
SLUG_MAX_CHARS = 50
NOT_SLUG_CHARS = re.compile(r"[^a-z0-9]+")
BULLET_PREFIX = re.compile(rf"^[{''.join(BULLETS)}]\s*")


@dataclass(frozen=True)
class Section:
    title: str
    detected: str
    blocks: list[Block]

    @property
    def pages(self) -> str:
        first, last = self.blocks[0].page, self.blocks[-1].page
        return str(first) if first == last else f"{first}-{last}"

    @property
    def words(self) -> int:
        return sum(len(block.text.split()) for block in self.blocks if block.kind != "footnote")

    @property
    def footnotes(self) -> int:
        return sum(block.kind == "footnote" for block in self.blocks)


def split_sections(blocks: list[Block]) -> list[Section]:
    groups: list[list[Block]] = []
    for block in blocks:
        if not groups or _starts_section(groups[-1], block):
            groups.append([])
        groups[-1].append(block)
    return [_make_section(group) for group in groups]


def _starts_section(current: list[Block], block: Block) -> bool:
    if block.kind == "contents":
        return current[-1].kind != "contents"
    if current[-1].kind == "contents":
        return True
    return block.kind == "heading" and block.level == 1


def _make_section(blocks: list[Block]) -> Section:
    first = blocks[0]
    if first.kind == "contents":
        return Section(title=CONTENTS_TITLE, detected="contents", blocks=blocks)
    if first.kind == "heading" and first.level == 1:
        title = NOTE_ID.sub("", first.text).strip()
        return Section(title=title, detected="text", blocks=blocks[1:] or blocks)
    return Section(title=FRONT_MATTER_TITLE, detected="text", blocks=blocks)


def render_section(section: Section) -> str:
    parts = [f"# {section.title}"]
    parts.extend(_render_block(block) for block in section.blocks)
    return "\n\n".join(parts) + "\n"


def _render_block(block: Block) -> str:
    if block.kind == "heading":
        return f"{'#' * max(block.level, 2)} {block.text}"
    if block.kind == "paragraph" and block.text.startswith(BULLETS):
        return BULLET_PREFIX.sub("- ", block.text)
    return block.text


def write_workspace(workdir: Path, sections: list[Section], source_name: str) -> Path:
    for folder in (SECTIONS_DIR, ORIGINAL_DIR):
        shutil.rmtree(workdir / folder, ignore_errors=True)
        (workdir / folder).mkdir(parents=True)
    entries = []
    for number, section in enumerate(sections, start=1):
        name = f"{number:02d}-{_slug(section.title)}.md"
        text = render_section(section)
        (workdir / SECTIONS_DIR / name).write_text(text, encoding="utf-8")
        (workdir / ORIGINAL_DIR / name).write_text(text, encoding="utf-8")
        entries.append(_manifest_entry(name, section))
    manifest = {"source": source_name, "title": "", "author": "", "sections": entries}
    manifest_path = workdir / MANIFEST_NAME
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    return manifest_path


def _manifest_entry(name: str, section: Section) -> dict:
    is_contents = section.detected == "contents"
    return {
        "file": name,
        "title": section.title,
        "pages": section.pages,
        "words": section.words,
        "footnotes": section.footnotes,
        "keep": False if is_contents else None,
        "reason": "table of contents" if is_contents else "",
    }


def _slug(title: str) -> str:
    slug = NOT_SLUG_CHARS.sub("-", title.lower()).strip("-")
    return slug[:SLUG_MAX_CHARS].rstrip("-") or "untitled"


def read_manifest(workdir: Path) -> dict:
    return json.loads((workdir / MANIFEST_NAME).read_text(encoding="utf-8"))
