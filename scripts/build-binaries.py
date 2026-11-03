#!/usr/bin/env python3
"""Build the executables and collect their platform-specific runtime files:
PaperGo's LICENSE and NOTICE, native libraries and the notices of everything
compiled into or shipped beside the executable. The Linux executable uses
the distribution's libstdc++ and libgcc_s. The Windows executable embeds
ONNX Runtime and links its compiler runtimes statically, so only notices
ship beside it."""
import argparse
import os
from pathlib import Path
import platform
import shlex
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent
LIBRARIES = {
    'linux': ['libonnxruntime.so'],
    'darwin': ['libonnxruntime.dylib'],
    # The Windows executable embeds ONNX Runtime with the Visual C++ runtime.
    'windows': [],
}
# PaperGo's own licence and notice, at the top of the installation.
PROJECT_FILES = ['LICENSE', 'NOTICE']
# The executable embeds PDFium as WebAssembly; these notices ship with it.
PDFIUM_NOTICES = ROOT / 'internal/doctext/pdfium/notices'
# The npm packages bundled into the embedded web app and workflow SDK, whose
# builds write these notices (scripts/npm-notices.mjs).
NPM_NOTICES = {
    'notices/web/THIRD-PARTY-NOTICES': ROOT / 'web/dist/THIRD-PARTY-NOTICES',
    'notices/sdk/THIRD-PARTY-NOTICES': ROOT / 'sdk/dist/THIRD-PARTY-NOTICES',
}
# The Go modules and standard library linked into the executable, generated
# for the platform being built (scripts/generate-go-notices.py).
GO_NOTICES = 'notices/go/THIRD-PARTY-NOTICES'
# MSYS2's licences of the MinGW runtimes the Windows executable links
# statically: libstdc++ and libgcc (GPL-3.0 with the GCC Runtime Library
# Exception) and winpthreads.
WINDOWS_RUNTIME_LICENSES = ['libstdc++', 'libgcc', 'winpthreads']


def compiler(env, system):
    command = shlex.split(env.get('CXX', 'g++'), posix=system != 'windows')
    # CI supplies Windows compiler paths containing spaces without shell quotes.
    if Path(env.get('CXX', '')).is_file():
        command = [env['CXX']]
    return command


def windows_compiler_notices(env):
    """The licences of the MinGW runtimes linked into the Windows executable."""
    command = compiler(env, 'windows')
    files = {}
    executable = shutil.which(command[0], path=env.get('PATH'))
    licenses = Path(executable).parent.parent / 'share/licenses' if executable else None
    for package in WINDOWS_RUNTIME_LICENSES:
        directory = licenses / package if licenses else None
        if not directory or not directory.is_dir():
            raise ValueError(f'Missing the MinGW {package} licence: install MSYS2 UCRT64 GCC, whose share/licenses/{package} holds it')
        for path in sorted(directory.rglob('*')):
            if path.is_file():
                files[f'notices/compiler/{package}/' + path.relative_to(directory).as_posix()] = path
    return files


def runtime_files(native, system, env):
    files = {name: ROOT / name for name in PROJECT_FILES}
    files.update({'lib/' + name: native / 'lib' / name for name in LIBRARIES[system]})
    if system == 'windows':
        files.update(windows_compiler_notices(env))
    for path in sorted((native / 'notices').rglob('*')):
        if path.is_file():
            files['notices/' + path.relative_to(native / 'notices').as_posix()] = path
    for path in sorted(PDFIUM_NOTICES.iterdir()):
        files['notices/pdfium/' + path.name] = path
    for name, path in NPM_NOTICES.items():
        if not path.is_file():
            project = path.parent.parent.name
            raise ValueError(f'Missing {path.relative_to(ROOT).as_posix()}: build it with npm --prefix {project} run build')
        files[name] = path
    return files


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output-dir', type=Path, default=ROOT / 'bin')
    parser.add_argument('--native-dir', type=Path, default=Path(os.environ.get('PAPERGO_NATIVE_DIR') or ROOT / '.tools/native'))
    parser.add_argument('--ldflags', default='-s -w')
    args = parser.parse_args()
    system = platform.system().lower()
    suffix = '.exe' if system == 'windows' else ''
    output = args.output_dir.resolve()
    native = args.native_dir.resolve()
    env = dict(os.environ)
    for line in (native / 'native.env').read_text().splitlines():
        key, value = line.split('=', 1)
        env[key] = value
    if system == 'windows':
        # Checks the SDKs and stages the runtime the executable embeds.
        subprocess.run([sys.executable, str(ROOT / 'scripts/native.py'), '--check', '--directory', str(native)],
                       cwd=ROOT, env=env, check=True)
    output.mkdir(parents=True, exist_ok=True)
    # Libraries and notices of an earlier build must not ship with this one.
    for directory in ('lib', 'notices'):
        shutil.rmtree(output / directory, ignore_errors=True)
    for name, source in runtime_files(native, system, env).items():
        target = output / name
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, target)
    # The same environment as the build, so the notices list exactly the
    # modules linked for this GOOS, GOARCH and cgo setting.
    subprocess.run([sys.executable, str(ROOT / 'scripts/generate-go-notices.py'), '--output', str(output / GO_NOTICES)],
                   cwd=ROOT, env=env, check=True)
    subprocess.run(['go', 'build', '-trimpath', '-ldflags=' + args.ldflags,
                    '-o', str(output / ('papergo' + suffix)), './cmd/api'],
                   cwd=ROOT, env=env, check=True)
    print(f'Executables, native libraries and notices: {output}')


if __name__ == '__main__':
    main()
