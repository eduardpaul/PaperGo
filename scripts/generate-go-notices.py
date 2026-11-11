#!/usr/bin/env python3
"""Write the third-party notices of the Go code linked into papergo.

Lists every module `go list -deps ./cmd/api` reports for the platform being
built, in this environment (GOOS, GOARCH, CGO_ENABLED, GOFLAGS), but the
main module, and writes each one's path, version and every licence, copying
and notice file at its module root, plus the Go standard library's. A
replaced module's files come from its replacement. scripts/build-binaries.py
runs this with the environment it builds with; run it directly to inspect
the notices of the current platform.

It fails on a module without a licence file and on a licence outside those
docs/dependencies.md allows, unless REVIEWED records the module.
"""
import argparse
import hashlib
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent
PACKAGE = './cmd/api'
# LICENSE, LICENCE.md, LICENSE-GO, COPYING, NOTICE.txt, PATENTS and the like,
# but not Go sources such as notice.go.
LICENSE_FILE = re.compile(r'^((un)?licen[cs]e|copying|notice|patents)([-_.][\w.-]*)?$(?<!\.go)', re.IGNORECASE)
# Recognised licence texts, matched on whitespace-collapsed text. BSD-3-Clause
# precedes BSD-2-Clause, whose clauses it contains.
ALLOWED = [
    ('Apache-2.0', r'Apache License,? Version 2\.0'),
    ('MIT', r'Permission is hereby granted, free of charge, to any person obtaining a copy'),
    ('BSD-3-Clause', r'Redistribution and use in source and binary forms.*Neither the name'),
    ('BSD-2-Clause', r'Redistribution and use in source and binary forms, with or without modification, are permitted'),
    ('ISC', r'Permission to use, copy, modify, and(/or)? distribute this software for any purpose with or without fee'),
    ('Zlib', r"This software is provided 'as-is', without any express or implied warranty"),
    ('BSL-1.0', r'Boost Software License'),
    ('PostgreSQL', r'Permission to use, copy, modify, and distribute this software and its documentation for any purpose, without fee, and without a written agreement'),
]
REFUSED = [
    ('GPL', r'GNU (Affero |Lesser |Library )?General Public License'),
    ('MPL', r'Mozilla Public License'),
    ('SSPL', r'Server Side Public License'),
    ('BUSL', r'Business Source License'),
    ('Creative Commons', r'Creative Commons'),
    ('Unlicense', r'This is free and unencumbered software released into the public domain'),
]
# Modules whose licence files these heuristics refuse or cannot decide,
# reviewed by hand: module path -> (licence, SHA-256 of the reviewed licence
# files as licence_digest computes it). Changed files need a new review.
REVIEWED = {
    # Code under Apache-2.0, older contributions MIT. The same LICENSE puts
    # the project's documentation, which is not linked, under CC-BY-4.0.
    'github.com/modelcontextprotocol/go-sdk': (
        'Apache-2.0 AND MIT', 'af679003d933f045393a6a029f43da113f9ae364eac651d9ae268392985580f5'),
    # BSD-3-Clause, with SQLite's public-domain dedication and sqlite-vec's
    # MIT licence. LICENSE-3RD-PARTY.md also quotes the MPL-2.0 of
    # github.com/hashicorp/golang-lru, a module it lists as in the module graph
    # but compiled into nothing; go list -deps confirms it is not linked.
    'modernc.org/sqlite': (
        'BSD-3-Clause AND MIT AND public domain (SQLite)', 'aea23a14fe643249b695c6507d06fe6fa6ee28691f6b568e51e7171b85c164cd'),
}
SEPARATOR = '\n' + '=' * 78 + '\n\n'


def parse_modules(listing):
    """Modules of `go list -deps -f MODULE_FORMAT` output, but the main module:
    {path: (version, directory)}, with a replacement's version and directory."""
    modules = {}
    for line in listing.splitlines():
        if not line:
            continue
        path, version, main, directory, replacement, replaced_version, replaced_directory = line.split('\t')
        if main == 'true':
            continue
        if replacement:
            version = f'{version} => {replacement}' + (f' {replaced_version}' if replaced_version else '')
            directory = replaced_directory
        modules[path] = (version, directory)
    return modules


MODULE_FORMAT = ('{{with .Module}}{{.Path}}\t{{.Version}}\t{{.Main}}\t{{.Dir}}\t'
                 '{{with .Replace}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{else}}\t\t{{end}}{{end}}')


def licence_files(directory):
    directory = Path(directory)
    return [(path.name, path.read_text(errors='replace'))
            for path in sorted(directory.iterdir()) if path.is_file() and LICENSE_FILE.match(path.name)]


def licence_digest(files):
    return hashlib.sha256('\0'.join(body for _, body in files).encode()).hexdigest()


def classify(name, files):
    """The allowed licences the files grant; raises on a refused or an
    unrecognised licence, unless REVIEWED records the module."""
    if not files:
        raise ValueError(f'{name} has no LICENSE, COPYING or NOTICE file at its module root; '
                         'add its upstream licence under scripts/native-notices/go and document it')
    if name in REVIEWED:
        licence, digest = REVIEWED[name]
        if licence_digest(files) != digest:
            raise ValueError(f'the licence files of {name} changed since they were reviewed; '
                             'review them and update REVIEWED in scripts/generate-go-notices.py')
        return [licence]
    text = ' '.join(' '.join(body for _, body in files).split())
    refused = [label for label, pattern in REFUSED if re.search(pattern, text, re.IGNORECASE)]
    if refused:
        raise ValueError(f'{name} is under {", ".join(refused)}, which docs/dependencies.md does not allow; '
                         'review it and record it in REVIEWED in scripts/generate-go-notices.py if it is acceptable')
    found = []
    for label, pattern in ALLOWED:
        if re.search(pattern, text, re.IGNORECASE) and not (label == 'BSD-2-Clause' and 'BSD-3-Clause' in found):
            found.append(label)
    if not found:
        raise ValueError(f'{name} has a licence these notices do not recognise; '
                         'review it and record it in REVIEWED in scripts/generate-go-notices.py')
    return found


def go(env, *args):
    return subprocess.run(['go', *args], cwd=ROOT, env=env, check=True, text=True, stdout=subprocess.PIPE).stdout


def render(platform, components):
    """The notices file: components are (name, version, directory) with the
    Go standard library first."""
    entries = []
    for name, version, directory in components:
        files = licence_files(directory)
        entries.append((f'{name} {version}', ' AND '.join(classify(name, files)), files))
    header = (f'Third-party notices for the Go code linked into papergo ({platform})\n\n'
              f'Generated by scripts/generate-go-notices.py from go list -deps {PACKAGE}:\n'
              f'the Go standard library and {len(entries) - 1} modules.\n\n'
              + ''.join(f'  {title} ({licence})\n' for title, licence, _ in entries))
    sections = [f'{title}\nLicence: {licence}\n\n' + '\n'.join(f'--- {file} ---\n\n{text.rstrip()}\n' for file, text in files)
                for title, licence, files in entries]
    return header + SEPARATOR + SEPARATOR.join(sections)


def notices(env=None):
    goos, goarch, goroot, version = go(env, 'env', 'GOOS', 'GOARCH', 'GOROOT', 'GOVERSION').splitlines()
    modules = parse_modules(go(env, 'list', '-deps', '-f', MODULE_FORMAT, PACKAGE))
    components = [('Go standard library', version, goroot)]
    components += [(path, module_version, directory) for path, (module_version, directory) in sorted(modules.items())]
    return render(f'{goos}/{goarch}', components), len(modules)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--output', type=Path, help='file to write; standard output when omitted')
    args = parser.parse_args()
    try:
        text, count = notices()
    except ValueError as error:
        raise SystemExit(f'Go notices: {error}')
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(text, encoding='utf-8', newline='\n')
        print(f'Go notices for the standard library and {count} modules: {args.output}', file=sys.stderr)
    else:
        sys.stdout.write(text)


if __name__ == '__main__':
    main()
