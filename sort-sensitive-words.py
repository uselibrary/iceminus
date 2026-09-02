from __future__ import annotations

import argparse
import sys
import unicodedata
from pathlib import Path

from pypinyin import Style, lazy_pinyin


DEFAULT_WORDS_FILE = Path("sensitive_words.txt")


def is_chinese_char(char: str) -> bool:
    return unicodedata.name(char, "").startswith("CJK UNIFIED IDEOGRAPH-")


def load_words(path: Path) -> list[str]:
    errors: list[str] = []
    words: list[str] = []

    for line_number, raw_line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        word = raw_line.strip()
        if not word:
            continue

        invalid_chars = [char for char in word if not is_chinese_char(char)]
        if invalid_chars:
            shown = " ".join(f"{char!r}" for char in invalid_chars)
            errors.append(f"line {line_number}: {word} contains non-Chinese character(s): {shown}")

        words.append(word)

    if errors:
        message = "\n".join(errors)
        raise ValueError(f"found non-Chinese content in {path}:\n{message}")

    return words


def pinyin_sort_key(word: str) -> tuple[str, tuple[str, ...], str]:
    initials = "".join(lazy_pinyin(word, style=Style.FIRST_LETTER))
    full_pinyin = tuple(lazy_pinyin(word))
    return initials, full_pinyin, word


def sort_words(words: list[str]) -> list[str]:
    unique_words = list(dict.fromkeys(words))
    return sorted(unique_words, key=pinyin_sort_key)


def write_words(path: Path, words: list[str]) -> None:
    path.write_text("\n".join(words) + "\n", encoding="utf-8")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Deduplicate and sort sensitive_words.txt by pinyin initials, then full pinyin.",
    )
    parser.add_argument(
        "path",
        nargs="?",
        type=Path,
        default=DEFAULT_WORDS_FILE,
        help="Path to the sensitive words file. Defaults to sensitive_words.txt.",
    )
    parser.add_argument(
        "--check",
        action="store_true",
        help="Validate and report whether the file is already sorted without writing changes.",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()

    try:
        words = load_words(args.path)
        sorted_words = sort_words(words)
    except FileNotFoundError:
        print(f"error: file not found: {args.path}", file=sys.stderr)
        return 1
    except ValueError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1

    if args.check:
        if words == sorted_words:
            print(f"{args.path} is already sorted")
            return 0
        print(f"{args.path} is not sorted")
        return 1

    write_words(args.path, sorted_words)
    removed_count = len(words) - len(sorted_words)
    print(f"sorted {len(sorted_words)} word(s) in {args.path}; removed {removed_count} duplicate(s)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
