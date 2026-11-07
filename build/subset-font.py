"""Subset JetBrainsLxgwNerdMono to the characters the Yousage interface can show.

The interface text is fixed: English source strings, the Chinese translations and a
few symbols. Dynamic values such as model names, accounts and paths are ASCII. Only
those characters are kept, so the bundled WOFF2 files stay small; anything else falls
back to a system font.

Run from the repository root after changing interface text:

    uv run --no-project --with fonttools --with brotli python build/subset-font.py

`charset.txt` lists the characters the subset carries and `fallback.txt` the interface
characters the font itself lacks, which render in a system font. `fonts.test.tsx` fails
when interface text uses a character in neither file.
"""

import argparse
import os
import pathlib

from fontTools import subset

ROOT = pathlib.Path(__file__).resolve().parents[1]
FRONTEND = ROOT / "frontend"
OUTPUT = FRONTEND / "src" / "fonts"
FAMILY = "JetBrainsLxgwNerdMono"
WEIGHTS = ("Regular", "Bold")
SOURCE_SUFFIXES = (".tsx", ".ts", ".css", ".html")

# Printable ASCII and the punctuation, arrows, math and fullwidth forms that dynamic
# values and future copy most often need, independently of current source text.
FIXED_RANGES = (
    (0x0020, 0x007E),
    (0x00A0, 0x00FF),
    (0x2010, 0x2027),
    (0x2030, 0x205E),
    (0x20A0, 0x20BF),
    (0x2190, 0x21FF),
    (0x2200, 0x22FF),
    (0x25A0, 0x25FF),
    (0x3000, 0x303F),
    (0xFF01, 0xFF5E),
)


def is_interface_source(path: pathlib.Path) -> bool:
    """Tell whether a file holds interface text rather than tests or fixtures.

    Args:
        path: A file under the frontend.

    Returns:
        True for shipped source, styles and pages.
    """
    return (
        path.suffix in SOURCE_SUFFIXES
        and ".test." not in path.name
        and path.name != "review.tsx"
        and "fonts" not in path.relative_to(FRONTEND).parts[:-1]
    )


def interface_characters() -> set[int]:
    """Collect every non-ASCII code point used by shipped frontend text.

    Returns:
        The code points, excluding whitespace and controls.
    """
    paths = [*FRONTEND.glob("*.html")]
    for directory in ("src", "vendor"):
        paths += [path for path in (FRONTEND / directory).rglob("*") if path.is_file()]
    code_points: set[int] = set()
    for path in paths:
        if is_interface_source(path):
            text = path.read_text(encoding="utf-8")
            code_points |= {ord(character) for character in text if ord(character) > 0x7E and not character.isspace()}
    return code_points


def requested_characters() -> set[int]:
    """Combine the fixed ranges with the characters the interface uses.

    Returns:
        Every code point the subset should keep when the font has it.
    """
    code_points = interface_characters()
    for first, last in FIXED_RANGES:
        code_points.update(range(first, last + 1))
    return code_points


def subset_weight(source: pathlib.Path, destination: pathlib.Path, code_points: set[int]) -> set[int]:
    """Write one WOFF2 subset with its names, licenses and layout features.

    Args:
        source: The full TTF of one weight.
        destination: The WOFF2 file to write.
        code_points: The characters to keep.

    Returns:
        The requested characters the font actually covers.
    """
    options = subset.Options()
    options.flavor = "woff2"
    options.name_IDs = ["*"]
    options.name_languages = ["*"]
    options.layout_features = ["*"]
    font = subset.load_font(str(source), options)
    covered = code_points & set(font.getBestCmap())
    subsetter = subset.Subsetter(options)
    subsetter.populate(unicodes=sorted(covered))
    subsetter.subset(font)
    subset.save_font(font, str(destination), options)
    return covered


def main() -> None:
    """Subset every bundled weight and record the covered characters."""
    default_source = pathlib.Path(os.environ.get("LOCALAPPDATA", "")) / "Microsoft" / "Windows" / "Fonts"
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--source-dir", type=pathlib.Path, default=default_source, help="Directory with the full TTF files")
    arguments = parser.parse_args()
    code_points = requested_characters()
    covered: set[int] | None = None
    for weight in WEIGHTS:
        destination = OUTPUT / f"{FAMILY}-{weight}.woff2"
        weight_covered = subset_weight(arguments.source_dir / f"{FAMILY}-{weight}.ttf", destination, code_points)
        covered = weight_covered if covered is None else covered & weight_covered
        print(f"{destination.relative_to(ROOT)}: {destination.stat().st_size} bytes")
    assert covered is not None
    (OUTPUT / "charset.txt").write_text("".join(chr(code_point) for code_point in sorted(covered)) + "\n", encoding="utf-8")
    fallback = sorted(interface_characters() - covered)
    (OUTPUT / "fallback.txt").write_text("".join(chr(code_point) for code_point in fallback) + "\n", encoding="utf-8")
    print(f"{len(covered)} characters kept; {len(fallback)} interface characters fall back: {''.join(map(chr, fallback))}")


if __name__ == "__main__":
    main()
