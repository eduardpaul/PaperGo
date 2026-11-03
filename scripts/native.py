#!/usr/bin/env python3
"""Install pinned native SDKs and write build/runtime environment files.

Requires Python 3.11+. Linux and macOS only download pinned binaries. Windows
builds the tokenizers static library from its pinned source, which needs the
Rust GNU toolchain (target x86_64-pc-windows-gnu) and MinGW-w64 GCC with g++
for the C and C++ code its crates compile, and Go to stage the runtime the
Windows executable embeds (internal/onnx). No upstream source files are
patched. Downloads are checked against native.json.
"""
import argparse
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import platform
import shlex
import shutil
import subprocess
import tarfile
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parent.parent
# The Visual C++ runtime ONNX Runtime imports, in load order; the Windows
# executable embeds it with ONNX Runtime (internal/onnx).
VC_RUNTIME = ['vcruntime140.dll', 'vcruntime140_1.dll', 'msvcp140.dll', 'msvcp140_1.dll']
VC_RUNTIME_NOTICE = ROOT / 'scripts/native-notices/microsoft-visual-cpp-runtime.txt'
WINDOWS_RUNTIME = ROOT / 'internal/onnx/windows-runtime'


def download(entry, cache):
    target = cache / entry['sha256']
    if not target.exists():
        temporary = target.with_suffix('.download')
        for attempt in range(3):
            try:
                with urllib.request.urlopen(entry['url'], timeout=120) as source, temporary.open('wb') as out:
                    shutil.copyfileobj(source, out)
                temporary.replace(target)
                break
            except OSError:
                temporary.unlink(missing_ok=True)
                if attempt == 2:
                    raise RuntimeError(f"download failed: {entry['url']}") from None
    if hashlib.sha256(target.read_bytes()).hexdigest() != entry['sha256']:
        raise ValueError(f"checksum mismatch: {entry['url']}")
    return target


def extract(archive, directory):
    """Extract only regular files and directories into the SDK's own directory."""
    directory.mkdir(parents=True, exist_ok=True)
    if zipfile.is_zipfile(archive):
        with zipfile.ZipFile(archive) as source:
            for info in source.infolist():
                target = directory / info.filename
                if not target.resolve().is_relative_to(directory.resolve()):
                    raise ValueError(f'unsafe archive path: {info.filename}')
                if not info.is_dir():
                    target.parent.mkdir(parents=True, exist_ok=True)
                    with source.open(info) as data, target.open('wb') as out:
                        shutil.copyfileobj(data, out)
    else:
        with tarfile.open(archive) as source:
            for info in source:
                target = directory / info.name
                if not target.resolve().is_relative_to(directory.resolve()):
                    raise ValueError(f'unsafe archive path: {info.name}')
                if info.isfile():
                    target.parent.mkdir(parents=True, exist_ok=True)
                    with source.extractfile(info) as data, target.open('wb') as out:
                        shutil.copyfileobj(data, out)
                    target.chmod(info.mode & 0o777)


def environment(output, system):
    lib = (output / 'lib').as_posix()
    flags = [f'-L{lib}', '-lm']
    if system == 'linux':
        # RPATH also resolves dependencies of the shipped shared libraries.
        flags += ['-Wl,--disable-new-dtags', '-Wl,-rpath,$ORIGIN/lib']
    elif system == 'darwin':
        flags += ['-Wl,-rpath,@loader_path/lib']
    elif system == 'windows':
        # The tokenizer's Rust standard library calls the Windows NT API.
        # MinGW's C++, GCC and thread runtimes link statically, so the
        # executable imports only Windows system DLLs and the Universal CRT.
        flags += ['-lntdll', '-static']
    env = {'CGO_ENABLED': '1', 'CGO_LDFLAGS': shlex.join(flags),
           'ONNXRUNTIME_LIB': str(output / 'lib' / {'linux': 'libonnxruntime.so', 'darwin': 'libonnxruntime.dylib', 'windows': 'onnxruntime.dll'}[system]),
           'OCR_MODELS': str(ROOT / 'models/paddleocr')}
    # The native loader uses this for local Go tests and development executables.
    loader = {'linux': 'LD_LIBRARY_PATH', 'darwin': 'DYLD_LIBRARY_PATH', 'windows': 'PATH'}[system]
    previous = os.environ.get(loader, '')
    env[loader] = str(output / 'lib') + (os.pathsep + previous if previous else '')
    return env


def stage_windows_runtime(native):
    """Stage ONNX Runtime and the Visual C++ runtime in internal/onnx, which
    the Windows executable embeds, unless they are staged already."""
    files = [native / 'lib' / name for name in [*VC_RUNTIME, 'onnxruntime.dll']]
    manifest = WINDOWS_RUNTIME / 'manifest.json'
    if manifest.is_file():
        staged = [(entry['name'], entry['sha256']) for entry in json.loads(manifest.read_text())]
        if staged == [(path.name, hashlib.sha256(path.read_bytes()).hexdigest()) for path in files] and \
                all((WINDOWS_RUNTIME / (path.name + '.zst')).is_file() for path in files):
            return
    subprocess.run(['go', 'run', './internal/onnx/stage', str(WINDOWS_RUNTIME), *map(str, files)], cwd=ROOT, check=True)
    print(f'Staged the embedded Windows runtime: {WINDOWS_RUNTIME}')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    # The toolchain image installs the SDKs once, outside the checkout.
    parser.add_argument('--directory', default=os.environ.get('PAPERGO_NATIVE_DIR') or str(ROOT / '.tools/native'))
    parser.add_argument('--github-env', action='store_true')
    parser.add_argument('--check', action='store_true', help='require current installed SDKs without downloading or building')
    args = parser.parse_args()
    system = platform.system().lower()
    machine = {'x86_64': 'amd64', 'AMD64': 'amd64', 'aarch64': 'arm64', 'arm64': 'arm64'}[platform.machine()]
    key = f'{system}-{machine}'
    lock = json.loads((ROOT / 'scripts/native.json').read_text())
    if key not in lock['platforms']:
        parser.error(f'no pinned native SDKs for {key}')
    output = Path(args.directory).resolve()
    cache = ROOT / '.tools/native-downloads'
    cache.mkdir(parents=True, exist_ok=True)
    stamp = output / 'installed.json'
    fingerprint = {'platform': key, 'lock': hashlib.sha256((ROOT / 'scripts/native.json').read_bytes()).hexdigest(),
                   'installer': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                   'notices': hashlib.sha256(b''.join(path.read_bytes() for path in sorted((ROOT / 'scripts/native-notices').rglob('*')) if path.is_file())).hexdigest()}
    if not stamp.exists() or json.loads(stamp.read_text()) != fingerprint:
        if args.check:
            parser.error('native SDKs are missing or stale; run python scripts/native.py')
        entries = dict(lock['platforms'][key], tokenizers_source=lock['tokenizers_source'])
        with concurrent.futures.ThreadPoolExecutor(max_workers=5) as pool:
            archives = dict(zip(entries, pool.map(lambda item: download(item, cache), entries.values())))
        if output.exists():
            shutil.rmtree(output)
        (output / 'lib').mkdir(parents=True)
        for name, archive in archives.items():
            extract(archive, output / 'sdk' / name)
        ort = next((output / 'sdk/onnxruntime').iterdir())
        ort_name = {'linux': 'libonnxruntime.so', 'darwin': 'libonnxruntime.dylib', 'windows': 'onnxruntime.dll'}[system]
        ort_library = next(item for item in (ort / 'lib').iterdir() if item.name.startswith(ort_name) or (system == 'darwin' and item.name.startswith('libonnxruntime.') and item.suffix == '.dylib'))
        shutil.copy2(ort_library, output / 'lib' / ort_name)
        if system == 'windows':
            # Beside onnxruntime.dll, and on PATH for development, as Windows
            # lacks them until the Visual C++ Redistributable is installed.
            redist = output / 'sdk/vcruntime' / lock['platforms'][key]['vcruntime']['directory']
            for name in VC_RUNTIME:
                shutil.copy2(redist / name, output / 'lib' / name)
        tokenizer_source = next((output / 'sdk/tokenizers_source').iterdir())
        if system == 'windows':
            subprocess.run(['cargo', 'build', '--locked', '--release', '-p', 'tokenizers-ffi',
                            '--target', 'x86_64-pc-windows-gnu'], cwd=tokenizer_source, check=True)
            tokenizer = tokenizer_source / 'target/x86_64-pc-windows-gnu/release/libtokenizers_ffi.a'
        else:
            tokenizer = next((output / 'sdk/tokenizers').rglob('libtokenizers.a'))
        shutil.copy2(tokenizer, output / 'lib/libtokenizers.a')
        notices = output / 'notices'
        # The tokenizers library's own and its linked crates' notices,
        # generated by scripts/generate-tokenizers-notices.py.
        shutil.copytree(ROOT / 'scripts/native-notices/tokenizers', notices / 'tokenizers')
        for name in ['LICENSE', 'ThirdPartyNotices.txt']:
            shutil.copy2(ort / name, notices / ('onnxruntime-' + name))
        if system == 'windows':
            version = lock['platforms'][key]['vcruntime']['version']
            if f'Visual C++ Runtime {version} ' not in VC_RUNTIME_NOTICE.read_text():
                raise ValueError(f'{VC_RUNTIME_NOTICE.relative_to(ROOT)} must name the pinned runtime version {version}')
            shutil.copy2(VC_RUNTIME_NOTICE, notices / VC_RUNTIME_NOTICE.name)
        stamp.write_text(json.dumps(fingerprint))
    if system == 'windows':
        stage_windows_runtime(output)
    if args.check:
        print(f'Native SDKs are current: {output}')
        return
    env = environment(output, system)
    (output / 'native.env').write_text(''.join(f'{name}={value}\n' for name, value in env.items()))
    (output / 'env.sh').write_text(''.join(f'export {name}={shlex.quote(value)}\n' for name, value in env.items()))
    (output / 'env.ps1').write_text(''.join(f"$env:{name} = '" + value.replace("'", "''") + "'\n" for name, value in env.items()))
    if args.github_env:
        with open(os.environ['GITHUB_ENV'], 'a') as target:
            for name, value in env.items():
                if name == 'PATH':
                    continue
                target.write(f'{name}={value}\n')
            if system == 'windows':
                # PATH is handled by the runner's dedicated command file.
                with open(os.environ['GITHUB_PATH'], 'a') as paths:
                    paths.write(str(output / 'lib') + '\n')
    print(f'Native SDKs ready: {output}')


if __name__ == '__main__':
    main()
