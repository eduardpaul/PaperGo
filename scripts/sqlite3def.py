#!/usr/bin/env python3
"""Install the pinned sqlite3def release planner into a directory, .tools/bin
by default, where scripts/check.py finds it. The toolchain image, CI's
native jobs and setup use this one pin."""
import argparse
import hashlib
from pathlib import Path
import platform
import shutil
import subprocess
import tarfile
import tempfile
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parent.parent
VERSION = '3.11.26'
ARCHIVES = {
    'linux-amd64': ('sqlite3def_linux_amd64.tar.gz', '7ef1cadbbaca6f12543c00edd3abe6b607ec69f30239517c47e36f86b580e378'),
    'linux-arm64': ('sqlite3def_linux_arm64.tar.gz', '40c75c6ddb38cbf080bd6766f1e39afa9e6b08785a55fbcf20207e6622bb1873'),
    'windows-amd64': ('sqlite3def_windows_amd64.zip', '235d8a40f74dd56cc9997a6293f5f36241017a4450b698d6b3b7edc9bacaeb70'),
}


def installed(executable):
    if not executable.is_file():
        return False
    version = subprocess.run([str(executable), '--version'], capture_output=True, text=True)
    return version.returncode == 0 and version.stdout.startswith(VERSION + ' ')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', type=Path, default=ROOT / '.tools/bin')
    args = parser.parse_args()
    system = platform.system().lower()
    machine = {'x86_64': 'amd64', 'AMD64': 'amd64', 'aarch64': 'arm64', 'arm64': 'arm64'}.get(platform.machine(), platform.machine())
    if f'{system}-{machine}' not in ARCHIVES:
        parser.error(f'sqlite3def {VERSION} has no pinned build for {system}-{machine}; install it on PATH or set SQLITE3DEF')
    archive, checksum = ARCHIVES[f'{system}-{machine}']
    executable = args.directory / ('sqlite3def.exe' if system == 'windows' else 'sqlite3def')
    if installed(executable):
        print(f'sqlite3def {VERSION} is installed: {executable}')
        return
    with tempfile.TemporaryDirectory() as temporary:
        download = Path(temporary) / archive
        url = f'https://github.com/sqldef/sqldef/releases/download/v{VERSION}/{archive}'
        with urllib.request.urlopen(url, timeout=120) as source, download.open('wb') as out:
            shutil.copyfileobj(source, out)
        if hashlib.sha256(download.read_bytes()).hexdigest() != checksum:
            raise SystemExit(f'checksum mismatch: {url}')
        # Only the executable is read from the archive, by name.
        if archive.endswith('.zip'):
            with zipfile.ZipFile(download) as files:
                content = files.read(executable.name)
        else:
            with tarfile.open(download) as files:
                content = files.extractfile(executable.name).read()
        args.directory.mkdir(parents=True, exist_ok=True)
        executable.write_bytes(content)
        executable.chmod(0o755)
    if not installed(executable):
        raise SystemExit(f'{executable} does not report sqlite3def {VERSION}')
    print(f'sqlite3def {VERSION} installed: {executable}')


if __name__ == '__main__':
    main()
