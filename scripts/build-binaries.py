#!/usr/bin/env python3
"""Build the executables and collect their platform-specific runtime files.
The Windows executable embeds ONNX Runtime and links its compiler runtimes
statically, so only notices ship beside it."""
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
# The executable embeds PDFium as WebAssembly; these notices ship with it.
PDFIUM_NOTICES = ROOT / 'internal/doctext/pdfium/notices'
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


def compiler_runtime(system, env):
    """Include the compiler runtimes that are not supplied by the target OS,
    and the licences of those linked into the executable."""
    if system == 'darwin':
        return {}
    command = compiler(env, system)
    files = {}
    if system == 'windows':
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
    for name in ['libstdc++.so.6', 'libgcc_s.so.1']:
        located = subprocess.check_output([*command, '-print-file-name=' + name], env=env, text=True).strip()
        path = Path(located)
        if not path.is_file():
            executable = shutil.which(command[0], path=env.get('PATH'))
            path = Path(executable).parent / name if executable else path
        if not path.is_file():
            raise ValueError(f'Missing compiler runtime {name}')
        files['lib/' + name] = path
    # Debian/Ubuntu's compiler copyright files include the runtime exception
    # and redistribution notices.
    for package in ('libstdc++6', 'libgcc-s1'):
        notice = Path('/usr/share/doc') / package / 'copyright'
        if notice.is_file():
            files[f'notices/compiler/{package}-copyright'] = notice
    return files


def runtime_files(native, system, env):
    files = {'lib/' + name: native / 'lib' / name for name in LIBRARIES[system]}
    files.update(compiler_runtime(system, env))
    for path in sorted((native / 'notices').rglob('*')):
        if path.is_file():
            files['notices/' + path.relative_to(native / 'notices').as_posix()] = path
    for path in sorted(PDFIUM_NOTICES.iterdir()):
        files['notices/pdfium/' + path.name] = path
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
    for name, source in runtime_files(native, system, env).items():
        target = output / name
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, target)
    subprocess.run(['go', 'build', '-trimpath', '-ldflags=' + args.ldflags,
                    '-o', str(output / ('papergo' + suffix)), './cmd/api'],
                   cwd=ROOT, env=env, check=True)
    print(f'Executables and native libraries: {output}')


if __name__ == '__main__':
    main()
