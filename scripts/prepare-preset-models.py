#!/usr/bin/env python3
"""Prepare every offline preset for the shared assets directory."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent


def verify_laya(directory):
    manifest = json.loads((ROOT / 'internal/decision/manifest.json').read_bytes())
    try:
        if json.loads((directory / 'manifest.json').read_bytes()) != manifest:
            return False
        for name, expected in manifest['files'].items():
            with (directory / name).open('rb') as source:
                if hashlib.file_digest(source, 'sha256').hexdigest() != expected:
                    return False
    except (OSError, ValueError):
        return False
    return True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--laya-bundle', default=os.environ.get('PAPERGO_LAYA_BUNDLE'),
                        help='Laya directory, archive or HTTPS URL; otherwise the published bundle in '
                             'scripts/decision-model/bundle.json')
    args = parser.parse_args()
    destination = ROOT / '.tools/decision/model'
    bundle = args.laya_bundle
    if not bundle and not verify_laya(destination):
        bundle = json.loads((ROOT / 'scripts/decision-model/bundle.json').read_bytes())['url']
    if bundle:
        subprocess.run([sys.executable, str(ROOT / 'scripts/decision-model/install.py'), bundle,
                        '--destination', str(destination)], cwd=ROOT, check=True)
    subprocess.run([sys.executable, str(ROOT / 'scripts/fetch-embedding-models.py'), 'all',
                    '--directory', str(ROOT / '.tools/embedding/models')], cwd=ROOT, check=True)


if __name__ == '__main__':
    main()
