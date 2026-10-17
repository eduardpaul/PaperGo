#!/usr/bin/env python3
"""Collect the shared model assets and optionally create their release ZIP."""
import argparse
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import sys
import zipfile

ROOT = Path(__file__).resolve().parent.parent


def asset_files():
    files = {}
    for path in sorted((ROOT / 'models/paddleocr').iterdir()):
        if path.is_file():
            files['models/paddleocr/' + path.name] = path
    decision = json.loads((ROOT / 'internal/decision/manifest.json').read_bytes())
    for name in ['manifest.json', *decision['files']]:
        files['models/laya/' + name] = ROOT / '.tools/decision/model' / name
    spec = importlib.util.spec_from_file_location('presets', ROOT / 'scripts/fetch-embedding-models.py')
    presets = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(presets)
    for model, preset in presets.MODELS.items():
        for name in preset['files']:
            files[f'models/embedding/{model}/{name}'] = ROOT / '.tools/embedding/models' / model / name
    for path in sorted((ROOT / 'scripts/model-notices').iterdir()):
        if path.is_file():
            files['notices/' + path.name] = path
    return files


def collect(output, files):
    for name, source in sorted(files.items()):
        target = output / name
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, target)
        target.chmod(0o644)


def archive_assets(path, output, files):
    path.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(path, 'w', zipfile.ZIP_DEFLATED, compresslevel=6) as archive:
        for name in sorted(files):
            # ZipFile.write streams large graphs and enables ZIP64 as needed.
            archive.write(output / name, 'assets/' + name)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output-dir', type=Path, default=ROOT / 'bin/assets')
    parser.add_argument('--archive', type=Path, help='Also write the shared assets ZIP')
    args = parser.parse_args()
    subprocess.run([sys.executable, str(ROOT / 'scripts/prepare-preset-models.py')], cwd=ROOT, check=True)
    files = asset_files()
    collect(args.output_dir, files)
    if args.archive:
        archive_assets(args.archive, args.output_dir, files)
    print(f'Shared assets: {args.output_dir}')


if __name__ == '__main__':
    main()
