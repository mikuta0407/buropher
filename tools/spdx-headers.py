#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-or-later
# Copyright (C) 2026 mikuta0407 and Buropher contributors
#
# Adds (or normalizes) the SPDX license header of every Go source file.
#   tools/spdx-headers.py          rewrite files
#   tools/spdx-headers.py --check  list files without the expected header (exit 1 if any)
#
# The header is placed before the package doc comment and separated from it by a
# blank line, so godoc output is unaffected. "Code generated ... DO NOT EDIT." lines
# stay valid (they only need to appear before the package clause).
# Templates (web/templates) get no comments because their output must stay
# byte-identical to Redmine; NOTICE covers them.
import os
import subprocess
import sys

COPYRIGHT = "// Copyright (C) 2026 mikuta0407 and Buropher contributors"
GPL = "// SPDX-License-Identifier: GPL-2.0-or-later"

# Files containing code ported from MIT-licensed projects (see THIRD_PARTY_NOTICES.md).
MIT_PORTS = [
    ("internal/textformat/highlight/rouge", [
        "// Portions ported from Rouge (https://github.com/rouge-ruby/rouge),",
        "// Copyright (c) 2012 Jeanine Adkisson and contributors, MIT License.",
    ]),
    ("internal/textformat/htmldom/", [
        "// Portions ported from libxml2 (https://gitlab.gnome.org/GNOME/libxml2),",
        "// Copyright (C) 1998-2012 Daniel Veillard, MIT License.",
    ]),
]

# Old header formats that are replaced by the standard one.
OLD_HEADERS = [
    ["// Copyright (C) 2026 buropher contributors", "// SPDX-License-Identifier: GPL-2.0-or-later"],
    ["// SPDX-License-Identifier: GPL-2.0-or-later"],
]


def header_for(path):
    for prefix, note in MIT_PORTS:
        if path.startswith(prefix) and not path.endswith("_test.go"):
            return ["// SPDX-License-Identifier: GPL-2.0-or-later AND MIT", COPYRIGHT] + note
    return [GPL, COPYRIGHT]


def strip_old(lines):
    for old in OLD_HEADERS:
        if lines[: len(old)] == old:
            rest = lines[len(old):]
            # drop a following empty comment line ("//") or blank line
            if rest and rest[0] in ("//", ""):
                rest = rest[1:]
            return rest
    return lines


def main():
    check = "--check" in sys.argv
    root = subprocess.run(["git", "rev-parse", "--show-toplevel"], capture_output=True, text=True, check=True).stdout.strip()
    os.chdir(root)
    files = subprocess.run(["git", "ls-files", "*.go"], capture_output=True, text=True, check=True).stdout.split()
    bad = []
    for f in files:
        with open(f, encoding="utf-8") as fh:
            src = fh.read()
        lines = src.split("\n")
        want = header_for(f)
        if lines[: len(want)] == want and len(lines) > len(want) and lines[len(want)] == "":
            continue
        if check:
            bad.append(f)
            continue
        body = strip_old(lines)
        while body and body[0] == "":
            body = body[1:]
        with open(f, "w", encoding="utf-8") as fh:
            fh.write("\n".join(want + [""] + body))
        bad.append(f)
    if check:
        for f in bad:
            print(f)
        sys.exit(1 if bad else 0)
    print("updated %d of %d files" % (len(bad), len(files)))


if __name__ == "__main__":
    main()
