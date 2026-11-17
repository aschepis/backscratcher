# /// script
# requires-python = ">=3.10"
# dependencies = ["pypdfium2>=5"]
# ///
"""Assemble edited sections into a manuscript, exports and production notes."""

import argparse
import sys
from pathlib import Path

from manuscript.assembly import ManifestIncomplete, assemble


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workdir", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    arguments = parser.parse_args()

    try:
        assembled = assemble(arguments.workdir, arguments.out)
    except ManifestIncomplete as problems:
        sys.exit(f"Cannot assemble yet:\n{problems}")

    for path in assembled.files:
        print(f"Wrote {path}")
    if assembled.warnings:
        print(f"\n{len(assembled.warnings)} passages a narrator would stumble on:")
        for warning in assembled.warnings:
            print(f"  {warning}")
        print("Fix each in its section file, or leave it if it reads well aloud, then run again.")


if __name__ == "__main__":
    main()
