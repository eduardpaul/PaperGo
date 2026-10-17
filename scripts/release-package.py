#!/usr/bin/env python3
"""Package the executables, native libraries, configuration and operator guide."""
import argparse
import json
from pathlib import Path
import zipfile

ROOT = Path(__file__).resolve().parent.parent


def package(version, system, arch, bin_dir, release_database, dist):
    suffix = '.exe' if system == 'windows' else ''
    inputs = {
        f'papergo{suffix}': bin_dir / f'papergo{suffix}',
        f'papergo-admin{suffix}': bin_dir / f'papergo-admin{suffix}',
        'release-database.json': release_database,
        'RELEASES.md': ROOT / 'docs/releases.md',
        'config.yaml': ROOT / 'deploy/config.yaml',
        'README.md': ROOT / 'deploy/README.md',
    }
    for directory in ('lib', 'notices'):
        for path in sorted((bin_dir / directory).rglob('*')):
            if path.is_file():
                inputs[path.relative_to(bin_dir).as_posix()] = path
    if system == 'windows':
        inputs.update((path.name, path) for path in sorted(bin_dir.glob('*.dll')))
    required = ['onnxruntime.dll', 'pdfium.dll', 'zvec_c_api.dll'] if system == 'windows' else [
        'lib/libonnxruntime.so', 'lib/libpdfium.so', 'lib/libzvec_c_api.so',
    ] if system == 'linux' else [
        'lib/libonnxruntime.dylib', 'lib/libpdfium.dylib', 'lib/libzvec_c_api.dylib',
    ]
    if any(name not in inputs for name in required):
        raise ValueError('Build the native libraries alongside both executables before packaging')
    bundle = json.loads(release_database.read_text())
    if bundle['releases'][-1]['version'] != version:
        raise ValueError('Packaged bundle does not match release tag')
    dist.mkdir(parents=True, exist_ok=True)
    target = dist / f'papergo_{version}_{system}_{arch}.zip'
    with zipfile.ZipFile(target, 'w', zipfile.ZIP_DEFLATED) as archive:
        for name, source in sorted(inputs.items()):
            archive.write(source, name)
    return target


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', required=True)
    parser.add_argument('--os', required=True, choices=['linux', 'darwin', 'windows'])
    parser.add_argument('--arch', required=True, choices=['amd64', 'arm64'])
    parser.add_argument('--bin-dir', type=Path, default=ROOT / 'bin')
    parser.add_argument('--release-database', type=Path, default=ROOT / 'migrations/release.json')
    args = parser.parse_args()
    print(package(args.version, args.os, args.arch, args.bin_dir, args.release_database, ROOT / 'dist'))


if __name__ == '__main__':
    main()
