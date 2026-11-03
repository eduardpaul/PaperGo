#!/usr/bin/env python3
"""Regenerate scripts/native-notices/tokenizers from the pinned tokenizers source.

The tokenizers static library (daulet/tokenizers, pinned in native.json as
tokenizers_source) links Hugging Face tokenizers, its Rust crate
dependencies, C and C++ code those crates compile (Oniguruma, esaxx), the
OpenAI tiktoken vocabularies tiktoken-rs embeds, and the Rust standard
library. This lists the crates cargo links into the release build of the
tokenizers-ffi static library on every shipped target (normal dependencies
only: no build, dev or proc-macro crates) and writes their licence texts to
THIRD-PARTY-NOTICES.

Requires Python 3.11+, cargo and network access to crates.io and GitHub. Run
it after changing tokenizers_source and commit the result.
"""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import shutil
import subprocess
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
OUTPUT = ROOT / 'scripts/native-notices/tokenizers'
# The Rust targets of the released tokenizers libraries (native.json platforms).
TARGETS = ['x86_64-unknown-linux-gnu', 'aarch64-unknown-linux-gnu', 'aarch64-apple-darwin', 'x86_64-pc-windows-gnu']
LICENSE_FILE = re.compile(r'^(licen[cs]e|copying|copyright|notice|unlicense)', re.IGNORECASE)
SOURCE_SUFFIXES = {'.c', '.cpp', '.h', '.hxx'}
# Code a crate compiles or embeds from its own sources whose notices are not
# in its top-level licence files: crate name -> paths relative to the crate.
# Only the leading licence comment of a source file is kept.
BUNDLED = {
    'onig_sys': ['oniguruma/COPYING'],
    'esaxx-rs': ['src/esa.hxx', 'src/sais.hxx'],
    # The OpenAI vocabularies in tiktoken-rs/assets come from tiktoken.
    'tiktoken-rs': ['../vendor/tiktoken/LICENSE'],
}
# Crates that package no licence file: their repository's, pinned by hash.
UPSTREAM = {
    'number_prefix': {
        'url': 'https://raw.githubusercontent.com/ogham/rust-number-prefix/v0.4.0/LICENCE',
        'sha256': 'df8d11b64ecce43b1229cd72745c59513b54ba39f05e3ec7c06780617b7b1fcc',
    },
}
RUST_STD = ('The Rust standard library (std, core, alloc and their dependencies) is\n'
            'also statically linked. It is dual-licensed MIT OR Apache-2.0; see\n'
            'https://github.com/rust-lang/rust/blob/master/COPYRIGHT.\n')
SEPARATOR = '\n' + '=' * 78 + '\n\n'


def native():
    spec = importlib.util.spec_from_file_location('native', ROOT / 'scripts/native.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def cargo(source, *args):
    return subprocess.run(['cargo', *args], cwd=source, check=True, text=True, stdout=subprocess.PIPE).stdout


def linked(source):
    """(name, version) of every crate linked into tokenizers-ffi on any target."""
    crates = set()
    for target in TARGETS:
        tree = cargo(source, 'tree', '--locked', '-p', 'tokenizers-ffi', '-e', 'normal,no-proc-macro',
                     '--target', target, '--prefix', 'none', '--no-dedupe', '--format', '{p}')
        for line in tree.splitlines():
            name, version = line.split()[:2]
            crates.add((name, version.removeprefix('v')))
    return crates


def leading_comment(text):
    match = re.match(r'\s*/\*(.*?)\*/', text, re.DOTALL)
    if not match:
        raise ValueError('no leading licence comment')
    lines = match.group(1).strip('\n').splitlines()
    return '\n'.join(line.strip().removeprefix('*').removeprefix(' ') for line in lines) + '\n'


def fetch(entry):
    with urllib.request.urlopen(entry['url'], timeout=60) as response:
        data = response.read()
    if hashlib.sha256(data).hexdigest() != entry['sha256']:
        raise SystemExit(f"checksum mismatch: {entry['url']}")
    return data.decode()


def notices(package, source):
    name = package['name']
    if name == 'tokenizers-ffi':
        # daulet/tokenizers: the repository's licence, outside the crate.
        return [('LICENSE', (source / 'LICENSE').read_text())]
    directory = Path(package['manifest_path']).parent
    files = [(path.name, path.read_text(errors='replace'))
             for path in sorted(directory.iterdir()) if path.is_file() and LICENSE_FILE.match(path.name)]
    if not files and package['source'] and package['source'].startswith('git+'):
        # A crate in a Git workspace: the repository root's licence.
        files = [(path.name, path.read_text(errors='replace'))
                 for path in sorted(directory.parent.iterdir()) if path.is_file() and LICENSE_FILE.match(path.name)]
    if name in UPSTREAM:
        files.append((UPSTREAM[name]['url'], fetch(UPSTREAM[name])))
    for path in BUNDLED.get(name, []):
        text = (directory / path).read_text(errors='replace')
        files.append((path.removeprefix('../'), leading_comment(text) if Path(path).suffix in SOURCE_SUFFIXES else text))
    if not files:
        raise SystemExit(f"{name} {package['version']} ships no licence file; add it to UPSTREAM")
    return files


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--work-dir', type=Path, default=ROOT / '.tools/tokenizers-notices')
    args = parser.parse_args()
    helpers = native()
    lock = json.loads((ROOT / 'scripts/native.json').read_text())
    cache = ROOT / '.tools/native-downloads'
    cache.mkdir(parents=True, exist_ok=True)
    archive = helpers.download(lock['tokenizers_source'], cache)
    work = args.work_dir.resolve()
    if work.exists():
        shutil.rmtree(work)
    helpers.extract(archive, work)
    source = next(work.iterdir())
    cargo(source, 'fetch', '--locked')
    metadata = json.loads(cargo(source, 'metadata', '--locked', '--format-version', '1'))
    packages = {(package['name'], package['version']): package for package in metadata['packages']}
    crates = sorted(linked(source))
    names = {name for name, _ in crates}
    stale = sorted(name for name in [*BUNDLED, *UPSTREAM] if name not in names)
    if stale:
        raise SystemExit(f'no longer linked, update BUNDLED or UPSTREAM: {stale}')
    sections = []
    for name, version in crates:
        package = packages[(name, version)]
        body = '\n'.join(f'--- {file} ---\n\n{text.rstrip()}\n' for file, text in notices(package, source))
        expression = package['license'] or 'MIT'
        sections.append(f"{name} {version} ({expression})\n{package['repository'] or 'https://github.com/daulet/tokenizers'}\n\n{body}")
    tag = lock['tokenizers_source']['url'].rsplit('/', 1)[-1]
    hugging_face = Path(packages[next((name, version) for name, version in crates if name == 'tokenizers')]['manifest_path']).parent
    header = (f'Third-party notices for libtokenizers, the static library of daulet/tokenizers {tag}\n\n'
              'Generated by scripts/generate-tokenizers-notices.py from the pinned source\'s\n'
              f'Cargo.lock: the {len(crates)} crates linked into the release build of the\n'
              'tokenizers-ffi static library on one or more of\n'
              f'{", ".join(TARGETS)}.\n'
              'They include Hugging Face tokenizers, the Oniguruma C library (onig_sys),\n'
              'the esaxx C++ code (esaxx-rs) and the OpenAI tiktoken vocabularies\n'
              '(tiktoken-rs).\n\n'
              f'{RUST_STD}\n'
              'Crates (licence as declared in the crate manifest):\n'
              + ''.join(f"  {name} {version} ({packages[(name, version)]['license'] or 'MIT'})\n" for name, version in crates))
    if OUTPUT.exists():
        shutil.rmtree(OUTPUT)
    OUTPUT.mkdir(parents=True)
    (OUTPUT / 'THIRD-PARTY-NOTICES').write_text(header + SEPARATOR + SEPARATOR.join(sections))
    shutil.copy2(source / 'LICENSE', OUTPUT / 'LICENSE')
    shutil.copy2(hugging_face / 'LICENSE', OUTPUT / 'huggingface-tokenizers-LICENSE')
    print(f'{len(crates)} crates: {OUTPUT}')


if __name__ == '__main__':
    main()
