# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "babel",
# ]
# ///

# SPDX-FileCopyrightText: © 2023 Olivier Meunier <olivier@neokraft.net>
#
# SPDX-License-Identifier: AGPL-3.0-only

import re
import sys
from argparse import ArgumentParser
from pathlib import Path

from babel.messages.catalog import Catalog, Locale
from babel.messages.extract import extract_from_file
from babel.messages.mofile import write_mo
from babel.messages.pofile import read_po, write_po

# Percentage of translated content under which a translation won't be loaded.
COMPLETION_CUTOFF = 0.90

HERE = Path(__file__).parent
ROOT = (HERE / "..").resolve()

CATALOG_HEADER = """\
# Translations template for PROJECT.
# SPDX-FileCopyrightText: © YEAR Readeck <translate@readeck.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#"""

CATALOG_OPTIONS = {
    "header_comment": CATALOG_HEADER,
    "project": "Readeck",
    "version": "1.0.0",
    "copyright_holder": "Readeck",
    "msgid_bugs_address": "translate@readeck.com",
    "last_translator": "Readeck <translate@readeck.com>",
    "language_team": "Readeck <translate@readeck.com>",
}


def tok(name: str):
    def s(scanner, token):
        return (name, token, scanner.match)

    return s


class GoScanner:
    scanner = re.Scanner(
        [
            (r"//.*$", tok("COMMENT")),
            (r'"(?:[^"\\]|\\.)*"', tok("STRING")),
            (r"`", tok("RAW_DELIM")),
            (r"[0-9]+", tok("INT")),
            (r",", tok("COMMA")),
            (r"\+", tok("PLUS")),
            (r"\(", tok("OPEN")),
            (r"\)", tok("CLOSE")),
            (r"Gettext|Ngettext|Pgettext|Npgettext", tok("FUNC")),
            (r".", None),
        ],
        flags=re.DOTALL | re.I,
    )

    def __init__(self, fp) -> None:
        self.fp = fp
        self.line = "\n"
        self.lineno = 0
        self.tokens = []
        self.idx = -1

        self._next()

    def _nextline(self):
        self.line = self.fp.readline().decode("utf-8")
        self.lineno += 1

        self.tokens, _ = self.scanner.scan(self.line)
        self.idx = -1

    def _next(self):
        # Advance to next token
        if len(self.tokens) > 0 and self.idx + 1 < len(self.tokens):
            self.idx += 1
            return True

        self._nextline()
        while len(self.tokens) == 0 and not self.eof:
            self._nextline()

        if self.eof:
            self.idx = -1
            return None

        self.idx = 0
        return True

    @property
    def eof(self) -> bool:
        return self.line == ""

    @property
    def token(self) -> str:
        return self.tokens[self.idx][0] if self.idx >= 0 else None

    @property
    def value(self) -> str:
        return self.tokens[self.idx][1] if self.idx >= 0 else None

    @property
    def match(self):
        return self.tokens[self.idx][2] if self.idx >= 0 else None

    def extract(self):
        while not self.eof:
            if self.token == "FUNC":
                res = self.visit_function()
                if res is not None:
                    yield res
            self._next()

    def extract_strings(self):
        for lineno, func, args in self.extract():
            if len(args) == 0:
                continue
            if all(x is None for x in args):
                continue

            messages = []
            for m in args:
                if m is None:
                    break
                messages.append(m)
            yield (lineno, func, messages, [])

    def visit_function(self):
        name = self.value
        lineno = self.lineno
        self._next()

        args = []
        s = None
        while self.token:
            if self.token in ("STRING", "RAW_DELIM"):
                s = self.visit_string()
            if self.token in ("COMMA", "CLOSE"):
                args.append(s)
                s = None

            if self.token == "CLOSE":
                break

            self._next()

        return (lineno, name, args)

    def visit_string(self):
        res = []
        while self.token in ("STRING", "RAW_DELIM", "PLUS"):
            if self.token == "RAW_DELIM":
                res.append(self.visit_raw_string())
            if self.token == "STRING":
                res.append(self.value[1:-1].replace('\\"', '"'))
            elif self.token == "LITERAL":
                res.append(self.value[1:-1])

            self._next()

        return "".join(res)

    def visit_raw_string(self):
        res = []
        start = self.match.span()[1]
        while True:
            next = [
                i
                for i, x in enumerate(self.tokens[self.idx + 1 :])
                if x[0] == "RAW_DELIM"
            ]
            if len(next) > 0:
                self.idx = self.idx + next[0] + 1
                end = self.match.span()[0]
                res.append(self.line[start:end])
                break

            res.append(self.line[start:])
            start = 0
            self._nextline()

        return "".join(res)


def extract_go(fileobj, keywords, comment_tags, options):
    return GoScanner(fileobj).extract_strings()


METHODS = {
    "_test.go": None,
    "_templ.go": None,
    ".go": extract_go,
    ".templ": extract_go,
}

KEYWORDS = {
    "Gettext": None,
    "Ngettext": (1, 2),
    "Pgettext": ((1, "c"), 2),
    "Npgettext": ((1, "c"), 2, 3),
    "gettext": None,
    "ngettext": (1, 2),
    "pgettext": ((1, "c"), 2),
    "npgettext": ((1, "c"), 2, 3),
}


def extract(_):
    template = Catalog(**CATALOG_OPTIONS)

    for f in ROOT.rglob("*"):
        method = None
        for suffix, m in METHODS.items():
            if f.name.endswith(suffix):
                method = m
                break

        if method is None:
            continue

        for lineno, message, comments, context in extract_from_file(
            method, f, keywords=KEYWORDS
        ):
            template.add(
                message,
                None,
                [(str(f.relative_to(ROOT)), lineno)],
                auto_comments=comments,
                context=context,
            )

    translations = HERE / "translations"
    dest = translations / "messages.pot"
    with dest.open("wb") as fp:
        write_po(
            fp,
            template,
            width=None,
            sort_by_file=True,
            include_lineno=True,
            ignore_obsolete=True,
        )
        print(f"{dest} written")


def update(_):
    translations = HERE / "translations"
    with (translations / "messages.pot").open("rb") as fp:
        template = read_po(fp)

    dirs = [x for x in translations.iterdir() if x.is_dir()]
    for p in sorted(dirs):
        locale = Locale.parse(p.name, sep="-")
        po_file = p / "messages.po"
        if po_file.exists():
            with po_file.open("rb") as fp:
                catalog = read_po(fp, locale=locale, domain=po_file.name)
        else:
            catalog = Catalog(
                **CATALOG_OPTIONS,
                locale=locale,
                domain=po_file.name,
            )

        catalog.update(template)

        # Remove previous id in fuzzy matches because of a bug with
        # weblate.
        for k, v in catalog._messages.items():
            if not v.fuzzy:
                continue
            catalog._messages[k].previous_id = []

        with po_file.open("wb") as fp:
            write_po(
                fp,
                catalog,
                width=None,
                sort_by_file=True,
                include_lineno=True,
                include_previous=True,
            )
            print(f"{po_file} written")


def compile(_):
    translations = HERE / "translations"
    po_files = translations.glob("*/messages.po")

    with (translations / "messages.pot").open("rb") as fp:
        template = read_po(fp)
    total_strings = len(template)

    for po_file in sorted(po_files):
        code = po_file.parent.name

        # Ignore en, it's always empty
        if code == "en":
            continue

        with po_file.open("rb") as fp:
            catalog = read_po(fp)

        # no need to compile en
        if code == "en":
            continue

        # Count translated strings
        nb_translated = 0
        for k, m in catalog._messages.items():
            tm = template._messages[k]
            if m.fuzzy:
                continue
            if tm.string == m.string:
                continue
            if isinstance(m.string, str) and m.string.strip() == "":
                continue
            if isinstance(m.string, tuple) and any([x.strip() == "" for x in m.string]):
                continue
            nb_translated += 1

        pct = float(nb_translated / total_strings)
        count_info = "{:>4}/{:<4} {:>4}%".format(
            nb_translated, total_strings, round(pct * 100)
        )
        if round(pct, 2) < COMPLETION_CUTOFF:
            print("[-] {:8} {}".format(code, count_info))
            continue

        dest = po_file.with_suffix(".mo")
        with dest.open("wb") as fp:
            write_mo(fp, catalog, use_fuzzy=False)
            print("[+] {:8} {} -- {}".format(code, count_info, dest.relative_to(HERE)))


def check(_):
    translations = HERE / "translations"
    po_files = translations.glob("*/messages.po")

    def check(catalog: Catalog):
        for message in catalog._messages.values():
            if message.fuzzy:
                continue
            errors = message.check(catalog=catalog)
            if errors:
                yield message, errors

    has_errors = False
    for filename in po_files:
        code = filename.parent.name
        if code == "en":
            continue

        with filename.open("rb") as fp:
            catalog = read_po(fp)

        errors = list(check(catalog))
        if len(errors) == 0:
            print(f"[OK] {code}")
        else:
            has_errors = True
            print(f"[ERRORS] {code}")
            for [m, e] in errors:
                print(f"  - #{m.lineno} - {m.id}")
                for x in e:
                    print(f"    - {str(x)}")

    sys.exit(has_errors and 1 or 0)


def main():
    parser = ArgumentParser()
    subparsers = parser.add_subparsers(required=True)

    p_extract = subparsers.add_parser("extract", help="Extract messages")
    p_extract.set_defaults(func=extract)

    p_update = subparsers.add_parser("update", help="Update strings")
    p_update.set_defaults(func=update)

    p_compile = subparsers.add_parser("compile", help="Compile gettext .mo files")
    p_compile.set_defaults(func=compile)

    p_check = subparsers.add_parser("check", help="Check translation files")
    p_check.set_defaults(func=check)

    args = parser.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
