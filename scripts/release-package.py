#!/usr/bin/env python3
"""Package the executable, native libraries, licences, notices, configuration
and operator guide."""
import argparse
import json
from pathlib import Path
import re
import shutil
import subprocess
import zipfile

ROOT = Path(__file__).resolve().parent.parent
# Linux ZIPs ship ONNX Runtime, loaded through the executable's $ORIGIN/lib
# RPATH, and nothing else: the C and C++ runtimes are the distribution's.
LINUX_LIBRARIES = {'libonnxruntime.so'}
# The newest symbol versions the oldest supported Linux provides, as glibc,
# libstdc++ and libgcc_s. Ubuntu 24.04 has glibc 2.39 and GCC 14's libstdc++6
# and libgcc-s1 (14.2.0-4ubuntu2~24.04); Debian 13 has glibc 2.41 and the same
# GCC 14 runtimes (14.2.0-19). Both libstdc++.so.6 define GLIBCXX_3.4.33 and
# CXXABI_1.3.15 at most, and libgcc_s.so.1 GCC_14.0.0. The executable needs
# glibc 2.39: the Rust standard library in libtokenizers refers weakly to
# pidfd_spawnp, which the linker binds to GLIBC_2.39 on the Ubuntu 24.04
# runners.
SYSTEM_MAXIMUM = {'GLIBC': (2, 39), 'GLIBCXX': (3, 4, 33), 'CXXABI': (1, 3, 15), 'GCC': (14, 0, 0)}
SYSTEM_LIBRARY = {'GLIBC': 'glibc', 'GLIBCXX': 'libstdc++', 'CXXABI': 'libstdc++', 'GCC': 'libgcc_s'}
SYMBOL_VERSION = re.compile(r'\b(GLIBC|GLIBCXX|CXXABI|GCC)_(\d+(?:\.\d+)+)\b')


def symbol_requirements(symbols):
    """The highest GLIBC, GLIBCXX, CXXABI and GCC symbol versions in objdump -T
    or readelf --dyn-syms output, by prefix."""
    required = {}
    for prefix, version in SYMBOL_VERSION.findall(symbols):
        required[prefix] = max(required.get(prefix, ()), tuple(int(part) for part in version.split('.')))
    return required


def dynamic_symbols(path):
    for command in (['objdump', '-T'], ['readelf', '--dyn-syms', '--wide']):
        if shutil.which(command[0]):
            return subprocess.run([*command, str(path)], check=True, capture_output=True, text=True).stdout
    raise ValueError('Checking the system libraries that Linux files need requires objdump or readelf (binutils)')


def version(parts):
    return '.'.join(map(str, parts))


def check_linux(inputs):
    """Refuse unexpected libraries and any ELF file that needs a newer glibc,
    libstdc++ or libgcc_s than the oldest supported Linux provides."""
    extra = sorted(name for name in inputs if name.startswith('lib/') and name.removeprefix('lib/') not in LINUX_LIBRARIES)
    if extra:
        raise ValueError(f'Unexpected Linux libraries: {", ".join(extra)}')
    for name in sorted(name for name in inputs if name == 'papergo' or name.startswith('lib/')):
        for prefix, required in sorted(symbol_requirements(dynamic_symbols(inputs[name])).items()):
            if required > SYSTEM_MAXIMUM[prefix]:
                raise ValueError(f'{name} needs {prefix}_{version(required)}, newer than the {SYSTEM_LIBRARY[prefix]} '
                                 f'of the oldest supported Linux ({prefix}_{version(SYSTEM_MAXIMUM[prefix])})')


# Notices of what papergo.exe embeds or links statically: ONNX Runtime, the
# Visual C++ runtime and MinGW's GCC and thread runtimes.
VC_RUNTIME_NOTICE = 'notices/microsoft-visual-cpp-runtime.txt'
WINDOWS_NOTICES = ['notices/onnxruntime-LICENSE', 'notices/onnxruntime-ThirdPartyNotices.txt',
                   VC_RUNTIME_NOTICE, 'notices/compiler/libstdc++/COPYING.RUNTIME',
                   'notices/compiler/libgcc/COPYING.RUNTIME', 'notices/compiler/winpthreads/COPYING']
# The Visual C++ runtime notice must keep Microsoft's files out of PaperGo's
# licence and bind users and redistributors to Microsoft's terms.
VC_RUNTIME_TERMS = ["are not licensed under PaperGo's Apache License",
                    "By installing, copying or using PaperGo's Windows build, you agree to these terms"]
# Every platform: PaperGo's licence and notice, and the notices of PDFium and
# of the Go modules and npm packages compiled into the executable.
NOTICES = ['LICENSE', 'NOTICE', 'notices/pdfium/pdfium.txt', 'notices/go/THIRD-PARTY-NOTICES',
           'notices/web/THIRD-PARTY-NOTICES', 'notices/sdk/THIRD-PARTY-NOTICES']


def check_windows_notice(path):
    text = path.read_text(encoding='utf-8')
    lock = json.loads((ROOT / 'scripts/native.json').read_text())
    pinned = lock['platforms']['windows-amd64']['vcruntime']['version']
    missing = [phrase for phrase in [f'Visual C++ Runtime {pinned} ', *VC_RUNTIME_TERMS] if phrase not in ' '.join(text.split())]
    if missing:
        raise ValueError(f'{VC_RUNTIME_NOTICE} must state {missing}')


def package(version, system, arch, bin_dir, dist):
    suffix = '.exe' if system == 'windows' else ''
    inputs = {
        f'papergo{suffix}': bin_dir / f'papergo{suffix}',
        'RELEASES.md': ROOT / 'docs/releases.md',
        'config.yaml': ROOT / 'deploy/config.yaml',
        'README.md': ROOT / 'deploy/README.md',
        'LICENSE': bin_dir / 'LICENSE',
        'NOTICE': bin_dir / 'NOTICE',
    }
    for directory in ('notices',) if system == 'windows' else ('lib', 'notices'):
        for path in sorted((bin_dir / directory).rglob('*')):
            if path.is_file():
                inputs[path.relative_to(bin_dir).as_posix()] = path
    required = list(NOTICES)
    if system == 'windows':
        # papergo.exe embeds ONNX Runtime with the Visual C++ runtime and links
        # its compiler runtimes statically: no DLL ships beside it.
        required += WINDOWS_NOTICES
        unexpected = [name for name in inputs if name.lower().endswith('.dll') or not (
            name in ('papergo.exe', 'RELEASES.md', 'config.yaml', 'README.md', 'LICENSE', 'NOTICE') or name.startswith('notices/'))]
        if unexpected:
            raise ValueError(f'The Windows package holds only papergo.exe, its documents, config.yaml, LICENSE, NOTICE and notices, not {unexpected}')
    else:
        required.append('lib/libonnxruntime.' + ('so' if system == 'linux' else 'dylib'))
    missing = [name for name in required if name not in inputs or not inputs[name].is_file() or not inputs[name].stat().st_size]
    if missing:
        raise ValueError(f'Build the native libraries, licences and notices alongside the executable before packaging; missing {missing}')
    platform = inputs['notices/go/THIRD-PARTY-NOTICES'].read_text(encoding='utf-8').partition('\n')[0]
    if not platform.endswith(f'({system}/{arch})'):
        raise ValueError(f'notices/go/THIRD-PARTY-NOTICES is not for {system}/{arch}: {platform}')
    if system == 'windows':
        check_windows_notice(inputs[VC_RUNTIME_NOTICE])
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
