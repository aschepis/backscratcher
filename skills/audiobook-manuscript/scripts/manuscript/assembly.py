"""Build the finished manuscript, its exports and the production notes."""

from dataclasses import dataclass
from pathlib import Path

from manuscript.exports import HEADING, Chapter, to_plain_text, write_epub
from manuscript.finalize import (
    describe_edits,
    drop_footnotes,
    find_unspeakable,
    listening_minutes,
)
from manuscript.workspace import ORIGINAL_DIR, SECTIONS_DIR, read_manifest

BRIEF_NAME = "brief.md"
MANUSCRIPT_STEM = "manuscript"
NOTES_NAME = "production-notes.md"


class ManifestIncomplete(Exception):
    pass


@dataclass(frozen=True)
class KeptSection:
    chapter: Chapter
    footnotes_dropped: int
    edits: list[str]
    warnings: list[str]


@dataclass(frozen=True)
class Assembled:
    files: list[Path]
    warnings: list[str]


def assemble(workdir: Path, out_dir: Path) -> Assembled:
    manifest = read_manifest(workdir)
    _require_decisions(manifest)
    kept = [_load_kept_section(workdir, entry) for entry in manifest["sections"] if entry["keep"]]
    chapters = [section.chapter for section in kept]
    manuscript = "\n".join(chapter.text for chapter in chapters)

    out_dir.mkdir(parents=True, exist_ok=True)
    markdown_path = out_dir / f"{MANUSCRIPT_STEM}.md"
    text_path = out_dir / f"{MANUSCRIPT_STEM}.txt"
    epub_path = out_dir / f"{MANUSCRIPT_STEM}.epub"
    notes_path = out_dir / NOTES_NAME
    markdown_path.write_text(manuscript, encoding="utf-8")
    text_path.write_text(to_plain_text(chapters), encoding="utf-8")
    write_epub(epub_path, manifest["title"], manifest["author"], chapters)
    notes_path.write_text(_production_notes(workdir, manifest, kept, manuscript), encoding="utf-8")

    warnings = [warning for section in kept for warning in section.warnings]
    return Assembled(files=[markdown_path, text_path, epub_path, notes_path], warnings=warnings)


def _require_decisions(manifest: dict) -> None:
    problems = [
        f"section {entry['file']} needs keep set to true or false"
        for entry in manifest["sections"] if entry["keep"] is None
    ]
    problems += [
        f"section {entry['file']} is cut but has no reason"
        for entry in manifest["sections"] if entry["keep"] is False and not entry["reason"]
    ]
    problems += [f"manifest {field} is empty" for field in ("title", "author") if not manifest[field]]
    if problems:
        raise ManifestIncomplete("\n".join(problems))


def _load_kept_section(workdir: Path, entry: dict) -> KeptSection:
    edited = (workdir / SECTIONS_DIR / entry["file"]).read_text(encoding="utf-8")
    original = (workdir / ORIGINAL_DIR / entry["file"]).read_text(encoding="utf-8")
    text, footnotes_dropped = drop_footnotes(edited)
    heading = HEADING.match(text.splitlines()[0])
    title = heading.group(2) if heading else entry["title"]
    return KeptSection(
        chapter=Chapter(title=title, text=text),
        footnotes_dropped=footnotes_dropped,
        edits=describe_edits(original, edited),
        warnings=[f"{entry['file']}: {warning}" for warning in find_unspeakable(text)],
    )


def _production_notes(workdir: Path, manifest: dict, kept: list[KeptSection], manuscript: str) -> str:
    brief_path = workdir / BRIEF_NAME
    brief = brief_path.read_text(encoding="utf-8").strip() if brief_path.exists() else "No brief written."
    parts = [
        f"# Production notes: {manifest['title']}",
        "## Narration brief",
        brief,
        "## Length",
        _length_summary(manuscript),
        "## Sections cut",
        _cut_table(manifest),
        "## Footnotes",
        _footnote_summary(kept),
        "## Edits to the source text",
        _edit_list(kept),
        "## Warnings",
        _warning_list(kept),
    ]
    return "\n\n".join(parts) + "\n"


def _length_summary(manuscript: str) -> str:
    minutes = listening_minutes(manuscript)
    return (
        f"- {len(manuscript.split()):,} words, {len(manuscript):,} characters\n"
        f"- About {minutes // 60} h {minutes % 60} min at a typical narration pace"
    )


def _cut_table(manifest: dict) -> str:
    cut = [entry for entry in manifest["sections"] if not entry["keep"]]
    if not cut:
        return "Nothing was cut."
    rows = [
        f"| {entry['title']} | {entry['pages']} | {entry['words']:,} | {entry['reason']} |"
        for entry in cut
    ]
    table = ["| Section | PDF pages | Words | Why it was cut |", "|---|---|---|---|", *rows]
    restore = 'To restore a section, set its "keep" to true in manifest.json and run assemble again.'
    return "\n".join(table) + "\n\n" + restore


def _footnote_summary(kept: list[KeptSection]) -> str:
    dropped = sum(section.footnotes_dropped for section in kept)
    return (
        f"{dropped} footnotes in the kept sections were dropped. "
        "Any footnote reworded into the text as a spoken aside is listed under the edits below."
    )


def _edit_list(kept: list[KeptSection]) -> str:
    groups = [
        f"### {section.chapter.title}\n\n" + "\n".join(f"- {edit}" for edit in section.edits)
        for section in kept if section.edits
    ]
    return "\n\n".join(groups) if groups else "The kept sections match the source word for word."


def _warning_list(kept: list[KeptSection]) -> str:
    warnings = [warning for section in kept for warning in section.warnings]
    return "\n".join(f"- {warning}" for warning in warnings) if warnings else "None."
