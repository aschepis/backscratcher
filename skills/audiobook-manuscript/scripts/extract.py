# /// script
# requires-python = ">=3.10"
# dependencies = ["pypdfium2>=5"]
# ///
"""Extract a PDF into editable section files plus a manifest."""

import argparse
from pathlib import Path

from manuscript.pdf_lines import read_pages
from manuscript.structure import build_blocks
from manuscript.workspace import read_manifest, split_sections, write_workspace


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("pdf", type=Path)
    parser.add_argument("--workdir", type=Path, required=True)
    arguments = parser.parse_args()

    sections = split_sections(build_blocks(read_pages(arguments.pdf)))
    manifest_path = write_workspace(arguments.workdir, sections, arguments.pdf.name)

    print(f"Wrote {len(sections)} sections to {arguments.workdir}")
    print(f"{'file':<58} {'pages':>7} {'words':>6} {'notes':>5}  keep")
    for entry in read_manifest(arguments.workdir)["sections"]:
        keep = {None: "?", True: "yes", False: "no"}[entry["keep"]]
        print(
            f"{entry['file']:<58} {entry['pages']:>7} {entry['words']:>6} "
            f"{entry['footnotes']:>5}  {keep}"
        )
    print(f"Set keep/reason for each '?' section in {manifest_path}")


if __name__ == "__main__":
    main()
