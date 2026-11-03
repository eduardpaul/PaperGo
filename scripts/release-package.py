#!/usr/bin/env python3
"""Package the executable, native libraries, configuration and operator guide."""
import argparse
from pathlib import Path
import re
import shutil
import subprocess
import zipfile

ROOT = Path(__file__).resolve().parent.parent
# Linux ZIPs ship ONNX Runtime and the build runner's C++ runtime, loaded
# through the executable's $ORIGIN/lib RPATH, and nothing else.
LINUX_LIBRARIES = {'libonnxruntime.so', 'libstdc++.so.6', 'libgcc_s.so.1'}
# The documented minimum glibc. The executable needs 2.39: the Rust standard
# library in libtokenizers refers weakly to pidfd_spawnp, which the linker
# binds to GLIBC_2.39 on the Ubuntu 24.04 runners. Their libstdc++ needs 2.38.
GLIBC_MAXIMUM = (2, 39)
GLIBC_VERSION = re.compile(r'\bGLIBC_(\d+(?:\.\d+)+)\b')


def glibc_requirement(symbols):
    """The highest GLIBC symbol version in objdump -T or readelf --dyn-syms output."""
    return max((tuple(int(part) for part in match.split('.')) for match in GLIBC_VERSION.findall(symbols)), default=None)


def dynamic_symbols(path):
    for command in (['objdump', '-T'], ['readelf', '--dyn-syms', '--wide']):
        if shutil.which(command[0]):
            return subprocess.run([*command, str(path)], check=True, capture_output=True, text=True).stdout
    raise ValueError('Checking the glibc that Linux files need requires objdump or readelf (binutils)')


def check_linux(inputs):
    """Refuse unexpected libraries and any ELF file that needs a newer glibc."""
    extra = sorted(name for name in inputs if name.startswith('lib/') and name.removeprefix('lib/') not in LINUX_LIBRARIES)
    if extra:
        raise ValueError(f'Unexpected Linux libraries: {", ".join(extra)}')
    for name in sorted(name for name in inputs if name == 'papergo' or name.startswith('lib/')):
        required = glibc_requirement(dynamic_symbols(inputs[name]))
        if required and required > GLIBC_MAXIMUM:
            raise ValueError(f'{name} needs GLIBC_{".".join(map(str, required))}, '
                             f'newer than the documented glibc {".".join(map(str, GLIBC_MAXIMUM))}')


def package(version, system, arch, bin_dir, dist):
    suffix = '.exe' if system == 'windows' else ''
    inputs = {
        f'papergo{suffix}': bin_dir / f'papergo{suffix}',
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
    required = ['onnxruntime.dll'] if system == 'windows' else [
        'lib/libonnxruntime.so',
    ] if system == 'linux' else [
        'lib/libonnxruntime.dylib',
    ]
    # The executable embeds PDFium, whose notices ship beside it.
    required.append('notices/pdfium/pdfium.txt')
    if any(name not in inputs for name in required):
        raise ValueError('Build the native libraries alongside the executable before packaging')
    if system == 'linux':
        check_linux(inputs)
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
    args = parser.parse_args()
    print(package(args.version, args.os, args.arch, args.bin_dir, ROOT / 'dist'))


if __name__ == '__main__':
    main()
