#!/usr/bin/env python3
"""Build the executables and collect their platform-specific runtime files."""
import argparse
import os
from pathlib import Path
import platform
import shlex
import shutil
import subprocess

ROOT = Path(__file__).resolve().parent.parent
LIBRARIES = {
    'linux': ['libonnxruntime.so', 'libpdfium.so', 'libzvec_c_api.so'],
    'darwin': ['libonnxruntime.dylib', 'libpdfium.dylib', 'libzvec_c_api.dylib'],
    'windows': ['onnxruntime.dll', 'pdfium.dll', 'zvec_c_api.dll'],
}


def compiler_runtime(system, env):
    """Include the compiler runtimes that are not supplied by the target OS."""
    if system == 'darwin':
        return {}
    compiler = shlex.split(env.get('CXX', 'g++'), posix=system != 'windows')
    # CI supplies Windows compiler paths containing spaces without shell quotes.
    if Path(env.get('CXX', '')).is_file():
        compiler = [env['CXX']]
    names = ['libstdc++.so.6', 'libgcc_s.so.1'] if system == 'linux' else [
        'libstdc++-6.dll', 'libgcc_s_seh-1.dll', 'libwinpthread-1.dll', 'libgomp-1.dll',
    ]
    files = {}
    for name in names:
        located = subprocess.check_output([*compiler, '-print-file-name=' + name], env=env, text=True).strip()
        path = Path(located)
        if not path.is_file():
            executable = shutil.which(compiler[0], path=env.get('PATH'))
            path = Path(executable).parent / name if executable else path
        if path.is_file():
            files[('' if system == 'windows' else 'lib/') + name] = path
        elif system == 'linux':
            raise ValueError(f'Missing compiler runtime {name}')
    # Debian/Ubuntu's compiler copyright files include the runtime exception
    # and redistribution notices. MSYS2 supplies these in share/licenses.
    for package in ('libstdc++6', 'libgcc-s1'):
        notice = Path('/usr/share/doc') / package / 'copyright'
        if notice.is_file():
            files[f'notices/compiler/{package}-copyright'] = notice
    executable = shutil.which(compiler[0], path=env.get('PATH'))
    if executable:
        licenses = Path(executable).parent.parent / 'share/licenses/gcc-libs'
        if licenses.is_dir():
            for path in sorted(licenses.rglob('*')):
                if path.is_file():
                    files['notices/compiler/' + path.relative_to(licenses).as_posix()] = path
    return files


def runtime_files(native, system, env):
    files = {('' if system == 'windows' else 'lib/') + name: native / 'lib' / name
             for name in LIBRARIES[system]}
    files.update(compiler_runtime(system, env))
    for path in sorted((native / 'notices').rglob('*')):
        if path.is_file():
            files['notices/' + path.relative_to(native / 'notices').as_posix()] = path
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
    output.mkdir(parents=True, exist_ok=True)
    for name, source in runtime_files(native, system, env).items():
        target = output / name
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, target)
    for command, name in [('api', 'papergo'), ('admin', 'papergo-admin')]:
        subprocess.run(['go', 'build', '-trimpath', '-ldflags=' + args.ldflags,
                        '-o', str(output / (name + suffix)), './cmd/' + command],
                       cwd=ROOT, env=env, check=True)
    print(f'Executables and native libraries: {output}')


if __name__ == '__main__':
    main()
