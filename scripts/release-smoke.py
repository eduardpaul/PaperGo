#!/usr/bin/env python3
"""Run the actual native release binaries against fresh and published databases."""
import json
import os
from pathlib import Path
import socket
import sqlite3
import subprocess
import tempfile
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
    binary = ROOT / "bin" / f"papergo{suffix}"
    admin = ROOT / "bin" / f"papergo-admin{suffix}"
    bundle = json.loads((ROOT / "migrations" / "release.json").read_text())
    current = bundle["releases"][-1]
    assert current["version"] in subprocess.check_output([str(binary), "--version"], text=True)
    assert current["version"] in subprocess.check_output([str(admin), "--version"], text=True)
    with tempfile.TemporaryDirectory(prefix="papergo-release-smoke-") as temp:
        fresh = Path(temp) / "fresh"
        fresh.mkdir()
        exercise(binary, admin, fresh, current["schema_hash"])
        upgrade = Path(temp) / "upgrade"
        upgrade.mkdir()
        old = bundle["releases"][0]
        with sqlite3.connect(upgrade / "papergo.db") as db:
            db.create_function("unicode_lower", 1, lambda value: value.lower() if value else value, deterministic=True)
            db.executescript(old["schema"])
            db.executescript("CREATE TABLE atlas_schema_revisions(version TEXT PRIMARY KEY,applied INTEGER NOT NULL,total INTEGER NOT NULL,error TEXT NOT NULL,hash TEXT NOT NULL);")
            db.execute("INSERT INTO atlas_schema_revisions VALUES('20261007000100',1,1,'',?)", (old["schema_hash"],))
            db.execute("INSERT INTO identity_users(id,created_at,display_name) VALUES('release-preserved',CURRENT_TIMESTAMP,'Keep me')")
        exercise(binary, admin, upgrade, current["schema_hash"])
        with sqlite3.connect(upgrade / "papergo.db") as db:
            assert db.execute("SELECT display_name FROM identity_users WHERE id='release-preserved'").fetchone()[0] == "Keep me"
        if old["schema_hash"] != current["schema_hash"]:
            assert list(upgrade.glob("papergo-pre-upgrade-*/database.sqlite"))
    print("Native release versions, embedded UI, fresh startup, published database upgrade, writes and online backup passed.")


if __name__ == "__main__":
    main()
