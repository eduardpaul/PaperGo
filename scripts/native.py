#!/usr/bin/env python3
"""Install pinned native SDKs and write build/runtime environment files.

Requires Python 3.11+, a C compiler, CMake 3.26+ and pkg-config. Windows also
requires the Rust GNU toolchain to build Hugging Face's tokenizer static library.
No upstream source files are patched. Downloads are checked against native.json.
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


def build_webp(source, output, cmake):
    build = source / 'build'
    generator = ['-G', 'Ninja'] if os.name == 'nt' else []
    subprocess.run([cmake, *generator, '-S', str(source), '-B', str(build), '-DCMAKE_BUILD_TYPE=Release',
                    '-DBUILD_SHARED_LIBS=OFF', '-DCMAKE_POSITION_INDEPENDENT_CODE=ON',
                    '-DWEBP_BUILD_ANIM_UTILS=OFF', '-DWEBP_BUILD_CWEBP=OFF', '-DWEBP_BUILD_DWEBP=OFF',
                    '-DWEBP_BUILD_VWEBP=OFF', '-DWEBP_BUILD_WEBPINFO=OFF', '-DWEBP_BUILD_WEBPMUX=OFF',
                    '-DWEBP_BUILD_EXTRAS=OFF', '-DWEBP_BUILD_GIF2WEBP=OFF'], check=True)
    subprocess.run([cmake, '--build', str(build), '--target', 'webp', 'sharpyuv', '--parallel', '2'], check=True)
    shutil.copytree(source / 'src/webp', output / 'include/webp', dirs_exist_ok=True)
    for name in ['libwebp.a', 'libsharpyuv.a']:
        shutil.copy2(next(build.rglob(name)), output / 'lib' / name)


def environment(output, system):
    lib = (output / 'lib').as_posix()
    flags = [f'-L{lib}', '-lwebp', '-lsharpyuv', '-lm']
    if system == 'linux':
        flags += ['-Wl,-rpath,$ORIGIN/lib', '-Wl,-rpath,/usr/lib/papergo']
    elif system == 'darwin':
        flags += ['-Wl,-rpath,@loader_path/lib']
    elif system == 'windows':
        # The tokenizer's Rust standard library calls the Windows NT API.
        flags += ['-lntdll']
    env = {'CGO_ENABLED': '1', 'CGO_CFLAGS': shlex.join([f'-I{(output / "include").as_posix()}']),
           'CGO_LDFLAGS': shlex.join(flags), 'PKG_CONFIG_PATH': str(output / 'lib/pkgconfig'),
           'ONNXRUNTIME_LIB': str(output / 'lib' / {'linux': 'libonnxruntime.so', 'darwin': 'libonnxruntime.dylib', 'windows': 'onnxruntime.dll'}[system])}
    # The native loader uses this for local Go tests and development executables.
    loader = {'linux': 'LD_LIBRARY_PATH', 'darwin': 'DYLD_LIBRARY_PATH', 'windows': 'PATH'}[system]
    previous = os.environ.get(loader, '')
    env[loader] = str(output / 'lib') + (os.pathsep + previous if previous else '')
    return env


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', default=str(ROOT / '.tools/native'))
    parser.add_argument('--cmake', default='cmake')
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
        entries = dict(lock['platforms'][key], webp=lock['webp'], tokenizers_source=lock['tokenizers_source'])
        with concurrent.futures.ThreadPoolExecutor(max_workers=5) as pool:
            archives = dict(zip(entries, pool.map(lambda item: download(item, cache), entries.values())))
        if output.exists():
            shutil.rmtree(output)
        (output / 'lib').mkdir(parents=True)
        for name, archive in archives.items():
            extract(archive, output / 'sdk' / name)
        zvec = output / 'sdk/zvec'
        shutil.copytree(zvec / 'include', output / 'include')
        for native in (zvec / key.replace('-', '_')).iterdir():
            if native.is_file():
                shutil.copy2(native, output / 'lib' / native.name)
        pdfium = output / 'sdk/pdfium'
        shutil.copytree(pdfium / 'include', output / 'include', dirs_exist_ok=True)
        for native in list((pdfium / 'lib').glob('*')) + list((pdfium / 'bin').glob('*')):
            if native.is_file():
                shutil.copy2(native, output / 'lib' / native.name)
        ort = next((output / 'sdk/onnxruntime').iterdir())
        ort_name = {'linux': 'libonnxruntime.so', 'darwin': 'libonnxruntime.dylib', 'windows': 'onnxruntime.dll'}[system]
        ort_library = next(item for item in (ort / 'lib').iterdir() if item.name.startswith(ort_name) or (system == 'darwin' and item.name.startswith('libonnxruntime.') and item.suffix == '.dylib'))
        shutil.copy2(ort_library, output / 'lib' / ort_name)
        if system == 'darwin':
            # The published PDFium SDK uses a cwd-relative install name. Give
            # the linked executable an executable-relative runtime dependency.
            library = output / 'lib/libpdfium.dylib'
            subprocess.run(['install_name_tool', '-id', '@rpath/libpdfium.dylib', str(library)], check=True)
            subprocess.run(['codesign', '--force', '--sign', '-', str(library)], check=True)
        tokenizer_source = next((output / 'sdk/tokenizers_source').iterdir())
        if system == 'windows':
            subprocess.run(['cargo', 'build', '--locked', '--release', '-p', 'tokenizers-ffi',
                            '--target', 'x86_64-pc-windows-gnu'], cwd=tokenizer_source, check=True)
            tokenizer = tokenizer_source / 'target/x86_64-pc-windows-gnu/release/libtokenizers_ffi.a'
        else:
            tokenizer = next((output / 'sdk/tokenizers').rglob('libtokenizers.a'))
        shutil.copy2(tokenizer, output / 'lib/libtokenizers.a')
        build_webp(next((output / 'sdk/webp').iterdir()), output, args.cmake)
        notices = output / 'notices'
        notices.mkdir()
        shutil.copytree(pdfium / 'licenses', notices / 'pdfium')
        shutil.copy2(tokenizer_source / 'LICENSE', notices / 'tokenizers-LICENSE')
        for name in ['LICENSE', 'ThirdPartyNotices.txt']:
            shutil.copy2(ort / name, notices / ('onnxruntime-' + name))
        shutil.copy2(next((output / 'sdk/webp').iterdir()) / 'COPYING', notices / 'webp-COPYING')
        shutil.copytree(ROOT / 'scripts/native-notices/zvec', notices / 'zvec')
        pkg = output / 'lib/pkgconfig'
        pkg.mkdir()
        (pkg / 'pdfium.pc').write_text(f'Name: PDFium\nDescription: PDFium\nVersion: 8076\nLibs: -L{(output / "lib").as_posix()} -lpdfium\nCflags: -I{(output / "include").as_posix()}\n')
        (pkg / 'libwebp.pc').write_text(f'Name: libwebp\nDescription: libwebp\nVersion: 1.6.0\nLibs: -L{(output / "lib").as_posix()} -lwebp -lsharpyuv -lm\nCflags: -I{(output / "include").as_posix()}\n')
        stamp.write_text(json.dumps(fingerprint))
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
