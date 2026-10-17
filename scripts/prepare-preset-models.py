#!/usr/bin/env python3
"""Prepare every offline preset used by the standalone executable."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

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
                        help='Pinned Laya directory/archive or HTTPS URL; otherwise export using Docker')
    args = parser.parse_args()
    destination = ROOT / '.tools/decision/model'
    if args.laya_bundle:
        subprocess.run([sys.executable, str(ROOT / 'scripts/decision-model/install.py'), args.laya_bundle,
                        '--destination', str(destination)], cwd=ROOT, check=True)
    elif not verify_laya(destination):
        if not shutil.which('docker'):
            raise ValueError('Preparing Laya requires Docker or PAPERGO_LAYA_BUNDLE pointing to a verified bundle; '
                             'see scripts/decision-model/README.md')
        (ROOT / '.tools').mkdir(exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='laya-export-', dir=ROOT / '.tools') as temp:
            subprocess.run(['docker', 'build', '--target=laya-model',
                            '--output', f'type=local,dest={temp}', '.'],
                           cwd=ROOT, check=True)
            subprocess.run([sys.executable, str(ROOT / 'scripts/decision-model/install.py'), temp,
                            '--destination', str(destination)], cwd=ROOT, check=True)
    subprocess.run([sys.executable, str(ROOT / 'scripts/fetch-embedding-models.py'), 'all',
                    '--directory', str(ROOT / '.tools/embedding/models')], cwd=ROOT, check=True)


if __name__ == '__main__':
    main()
