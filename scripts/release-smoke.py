#!/usr/bin/env python3
"""Run packaged native executables against a fresh disposable database."""
import json
import os
from pathlib import Path
import socket
import sys
import sqlite3
import subprocess
import tempfile
import tarfile
import zipfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
TOKEN = "papergo-release-smoke-development-token"


def request(base, path, data=None):
    headers = {"Authorization": f"Bearer {TOKEN}"}
    if data is not None:
        headers["Content-Type"] = "application/json"
        data = json.dumps(data).encode()
    with urllib.request.urlopen(urllib.request.Request(base + path, data=data, headers=headers), timeout=5) as response:
        return response.read()


def exercise(binary, admin, directory, expected_version):
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    base = f"http://127.0.0.1:{port}"
    env = dict(os.environ, APP_ENV="test", AUTH_MODE="development", DEV_TOKEN=TOKEN,
               ADMIN_SUBJECT="smoke-admin", HTTP_ADDR=f"127.0.0.1:{port}",
               DATABASE_PATH=str(directory / "papergo.db"), BLOB_PATH=str(directory / "blobs"))
    with (directory / "server.log").open("w+") as log:
        process = subprocess.Popen([str(binary)], cwd=ROOT, env=env, stdout=log, stderr=log)
        try:
            for attempt in range(90):
                if process.poll() is not None:
                    break
                try:
                    request(base, "/health/ready")
                    break
                except (OSError, urllib.error.URLError):
                    time.sleep(1)
            else:
                raise RuntimeError("release server did not become ready")
            if process.poll() is not None:
                raise RuntimeError("release server exited")
            assert b"<html" in request(base, "/").lower()
            request(base, "/health/live")
            # Exercise writes after migration; SQLite functions, triggers and
            # foreign-key enforcement must work in the released executable.
            workspace = json.loads(request(base, "/v1/workspaces", {"name": "Release smoke"}))
            assert workspace.get("id"), workspace
            subprocess.run([str(admin), "backup", "--file", str(directory / "backup.tar.gz")],
                           env=env, check=True)
        except Exception:
            log.flush()
            print((directory / "server.log").read_text())
            raise
        finally:
            if process.poll() is None:
                process.terminate()
            try:
                process.wait(timeout=20)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=10)
    with sqlite3.connect(directory / "papergo.db") as db:
        assert db.execute("SELECT hash FROM atlas_schema_revisions").fetchone()[0] == expected_version
        assert db.execute("PRAGMA integrity_check").fetchone()[0] == "ok"
        assert not db.execute("PRAGMA foreign_key_check").fetchall()


def main():
    suffix = ".exe" if os.name == "nt" else ""
    archives = sorted((ROOT / 'dist').glob('papergo_*.zip' if os.name == 'nt' else 'papergo_*.tar.gz'))
    if len(archives) != 1:
        raise ValueError('Expected one packaged release archive')
    package = tempfile.TemporaryDirectory(prefix='papergo-release-package-')
    import atexit
    atexit.register(package.cleanup)
    directory = Path(package.name)
    if os.name == 'nt':
        with zipfile.ZipFile(archives[0]) as archive:
            archive.extractall(directory)
    else:
        with tarfile.open(archives[0]) as archive:
            archive.extractall(directory, filter='data')
    # Test executable-relative lookup with no development loader paths.
    os.environ.pop('LD_LIBRARY_PATH', None)
    os.environ.pop('DYLD_LIBRARY_PATH', None)
    excluded = {os.path.normcase(str(ROOT / '.tools/native/lib'))}
    compiler = Path(os.environ.get('CC', ''))
    if compiler.is_absolute():
        excluded.add(os.path.normcase(str(compiler.parent)))
    os.environ['PATH'] = os.pathsep.join(part for part in os.environ['PATH'].split(os.pathsep) if os.path.normcase(part) not in excluded)
    ort_name = 'onnxruntime.dll' if os.name == 'nt' else ('libonnxruntime.dylib' if sys.platform == 'darwin' else 'libonnxruntime.so')
    os.environ['ONNXRUNTIME_LIB'] = str(directory / ('' if os.name == 'nt' else 'lib') / ort_name)
    assert Path(os.environ['ONNXRUNTIME_LIB']).is_file()

    binary = directory / f"papergo{suffix}"
    admin = directory / f"papergo-admin{suffix}"
    bundle = json.loads((directory / "release-database.json").read_text())
    current = bundle["releases"][-1]
    assert current["version"] in subprocess.check_output([str(binary), "--version"], text=True)
    assert current["version"] in subprocess.check_output([str(admin), "--version"], text=True)
    with tempfile.TemporaryDirectory(prefix="papergo-release-smoke-") as temp:
        fresh = Path(temp) / "fresh"
        fresh.mkdir()
        exercise(binary, admin, fresh, current["schema_hash"])
    print("Packaged native libraries, versions, embedded UI, fresh startup, writes and online backup passed.")


if __name__ == "__main__":
    main()
