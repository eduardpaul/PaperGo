#!/usr/bin/env python3
"""Measure Linux idle RSS on a disposable server, including a seeded restart.

Build the API/admin binaries and source .tools/native/env.sh before running.
Local AI models are disabled so this measures the server and search store.
"""
import argparse
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import platform
import socket
import sqlite3
import subprocess
import tempfile
import time

from performance import ROOT, Server, TOKEN, process_stats


@contextmanager
def running(binary, env, directory):
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        port = listener.getsockname()[1]
    with (directory / 'server.log').open('w+') as log:
        process = subprocess.Popen([str(binary)], cwd=ROOT,
                                   env=dict(env, HTTP_ADDR=f'127.0.0.1:{port}'), stdout=log, stderr=log)
        server = Server(port, process.pid)
        try:
            for _ in range(300):
                if process.poll() is not None:
                    raise RuntimeError('server exited before readiness')
                try:
                    server.json('GET', '/health/ready')
                    break
                except (OSError, RuntimeError):
                    time.sleep(.1)
            else:
                raise RuntimeError('server did not become ready')
            yield server
        except Exception:
            log.flush()
            print((directory / 'server.log').read_text()[-6000:])
            raise
        finally:
            process.terminate()
            try:
                process.wait(timeout=30)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()


def snapshot(server, name, seconds, poll_status=False):
    start = process_stats(server.pid)
    if poll_status:
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            server.json('GET', '/v1/search/status')
            time.sleep(min(5, max(0, deadline - time.monotonic())))
    else:
        time.sleep(seconds)
    end = process_stats(server.pid)
    smaps = {}
    for line in Path(f'/proc/{server.pid}/smaps_rollup').read_text().splitlines()[1:]:
        key, value = line.split(':', 1)
        smaps[key] = int(value.split()[0])
    result = dict(stage=name, idle_seconds=seconds, **end, smaps_kib=smaps,
                  idle_cpu_percent=(end['cpu_s'] - start['cpu_s']) / seconds * 100)
    print(f'{name}: {end["rss_mib"]:.1f} MiB RSS, {result["idle_cpu_percent"]:.2f}% CPU', flush=True)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, default=ROOT / 'bin/papergo')
    parser.add_argument('--admin', type=Path, default=ROOT / 'bin/papergo-admin')
    parser.add_argument('--search-store', choices=['sqlite', 'zvec'], default='sqlite')
    parser.add_argument('--items', type=int, default=200)
    parser.add_argument('--idle-seconds', type=float, default=65)
    parser.add_argument('--unload-seconds', type=float, default=0,
                        help='also measure automatic native unloading, polling status, then a wake-up search (use 335+)')
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    if platform.system() != 'Linux' or args.items < 1 or args.idle_seconds <= 0 or args.unload_seconds < 0:
        parser.error('requires Linux, positive item count and positive idle duration')
    if args.unload_seconds and args.search_store != 'zvec':
        parser.error('--unload-seconds requires --search-store zvec')
    if 0 < args.unload_seconds < 335:
        parser.error('--unload-seconds must be at least 335 to cover five minutes plus the maintenance interval')
    binary, admin = args.binary.resolve(), args.admin.resolve()
    # Deployment auth, paths, model settings and allocator overrides stay out.
    env = {k: v for k, v in os.environ.items() if k in ('PATH', 'HOME', 'LANG', 'TMPDIR', 'LD_LIBRARY_PATH')}
    env.update(APP_ENV='test', AUTH_MODE='development', DEV_TOKEN=TOKEN,
               DEV_SUBJECT='memory-admin', ADMIN_SUBJECT='memory-admin',
               SEARCH_STORE=args.search_store)
    with tempfile.TemporaryDirectory(prefix='papergo-memory-') as temp:
        directory = Path(temp)
        dbpath = directory / 'papergo.db'
        env.update(DATABASE_PATH=str(dbpath), BLOB_PATH=str(directory / 'blobs'))
        subprocess.run([str(admin), 'initialize', '--database', str(dbpath)],
                       cwd=ROOT, env=env, check=True, stdout=subprocess.DEVNULL)
        with running(binary, env, directory) as server:
            stages = [snapshot(server, 'idle-empty', args.idle_seconds)]
            workspace = server.json('POST', '/v1/workspaces', {'name': 'Memory'})['id']
            collection = server.json('POST', f'/v1/resources/{workspace}/children', {'kind': 'list', 'name': 'Invoices'})['id']
            for start in range(0, args.items, 100):
                operations = [{'action': 'create', 'create': {'name': f'Invoice {i:06d} Acme', 'tags': ['finance']}}
                              for i in range(start, min(start + 100, args.items))]
                server.json('POST', f'/v1/resources/{collection}/bulk', {'operations': operations})
            deadline = time.monotonic() + 600
            while True:
                with sqlite3.connect(dbpath) as db:
                    pending = db.execute('SELECT count(*) FROM domain_events WHERE dispatched_at IS NULL').fetchone()[0]
                    pending += db.execute("SELECT count(*) FROM workflow_runs WHERE live_status IN ('queued', 'running', 'waiting')").fetchone()[0]
                if not pending:
                    break
                if time.monotonic() > deadline:
                    raise RuntimeError('fixture events and workflows did not drain')
                time.sleep(.2)
            stages.append(snapshot(server, 'idle-seeded', args.idle_seconds))
            for _ in range(20):
                result = server.json('POST', f'/v1/resources/{collection}/query?$top=50', {'query': {'search': 'Invoice'}})
                if len(result.get('value', [])) != min(50, args.items):
                    raise RuntimeError('seeded full-text query returned the wrong number of items')
            stages.append(snapshot(server, 'idle-after-search', args.idle_seconds))
            if args.unload_seconds:
                stages.append(snapshot(server, 'idle-native-unloaded', args.unload_seconds, poll_status=True))
                started = time.monotonic()
                result = server.json('POST', f'/v1/resources/{collection}/query?$top=50', {'query': {'search': 'Invoice'}})
                wake_ms = (time.monotonic() - started) * 1000
                if len(result.get('value', [])) != min(50, args.items):
                    raise RuntimeError('native wake-up search lost indexed items')
                wake = snapshot(server, 'native-search-reopened', args.idle_seconds)
                wake['first_query_ms'] = wake_ms
                stages.append(wake)
        with running(binary, env, directory) as server:
            stages.append(snapshot(server, 'idle-restarted', args.idle_seconds))
        result = dict(platform=platform.platform(), search_store=args.search_store, items=args.items,
                      idle_seconds=args.idle_seconds, unload_seconds=args.unload_seconds,
                      binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),
                      local_models='disabled', stages=stages)
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(result, indent=2) + '\n')


if __name__ == '__main__':
    main()
