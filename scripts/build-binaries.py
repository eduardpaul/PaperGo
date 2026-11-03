#!/usr/bin/env python3
"""Build the executables and collect their platform-specific runtime files."""
import argparse
import contextlib
import os
from pathlib import Path
import platform
import shlex
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
WINDOWS = ROOT / 'scripts/windows'
LIBRARIES = {
    'linux': ['libonnxruntime.so', 'libpdfium.so', 'libzvec_c_api.so'],
    'darwin': ['libonnxruntime.dylib', 'libpdfium.dylib', 'libzvec_c_api.dylib'],
    'windows': ['onnxruntime.dll', 'pdfium.dll', 'zvec_c_api.dll'],
}


def compiler_runtime(system, env):
    """Include the compiler runtimes and notices that are not supplied by the target OS.

    Windows executables link the MinGW runtimes statically, so only their notices ship.
    """
    if system == 'darwin':
        return {}
    compiler = shlex.split(env.get('CXX', 'g++'), posix=system != 'windows')
    # CI supplies Windows compiler paths containing spaces without shell quotes.
    if Path(env.get('CXX', '')).is_file():
        compiler = [env['CXX']]
    files = {}
    # The shipped ONNX Runtime and PDFium libraries also load these.
    for name in ['libstdc++.so.6', 'libgcc_s.so.1'] if system == 'linux' else []:
        path = Path(subprocess.check_output([*compiler, '-print-file-name=' + name], env=env, text=True).strip())
        if not path.is_file():
            raise ValueError(f'Missing compiler runtime {name}')
        files['lib/' + name] = path
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
    files = {'lib/' + name: native / 'lib' / name for name in LIBRARIES[system]}
    if system == 'windows':
        files['lib/lib.manifest'] = WINDOWS / 'lib.manifest'
    files.update(compiler_runtime(system, env))
    for path in sorted((native / 'notices').rglob('*')):
        if path.is_file():
            files['notices/' + path.relative_to(native / 'notices').as_posix()] = path
    return files


@contextlib.contextmanager
def windows_manifest(env, packages):
    """Embed the application manifest, which resolves DLL imports from lib/, while building packages."""
    beside = Path(env['CC']).with_name('windres.exe') if env.get('CC') else None
    windres = str(beside) if beside and beside.is_file() else shutil.which('windres', path=env.get('PATH'))
    if not windres:
        raise ValueError('windres from the MinGW-w64 binutils is required to build Windows executables')
    objects = [ROOT / package / 'rsrc_windows_amd64.syso' for package in packages]
    try:
        with tempfile.TemporaryDirectory() as temp:
            script = Path(temp) / 'papergo.rc'
            script.write_text(f'1 24 "{(WINDOWS / "papergo.manifest").as_posix()}"\n')
            for target in objects:
                subprocess.run([windres, '-i', str(script), '-O', 'coff', '-o', str(target)], env=env, check=True)
        yield
    finally:
        for target in objects:
            target.unlink(missing_ok=True)


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
    # Windows executables carry the MinGW runtimes instead of shipping their DLLs.
    ldflags = args.ldflags + (' -extldflags=-static' if system == 'windows' else '')
    # papergo-mcp needs no native code and builds as pure Go.
    with windows_manifest(env, ['cmd/api', 'cmd/admin']) if system == 'windows' else contextlib.nullcontext():
        for command, name, cgo in [('api', 'papergo', None), ('admin', 'papergo-admin', None), ('mcp', 'papergo-mcp', '0')]:
            subprocess.run(['go', 'build', '-trimpath', '-ldflags=' + ldflags,
                            '-o', str(output / (name + suffix)), './cmd/' + command],
                           cwd=ROOT, env=env | ({'CGO_ENABLED': cgo} if cgo else {}), check=True)
    print(f'Executables and native libraries: {output}')


if __name__ == '__main__':
    main()
