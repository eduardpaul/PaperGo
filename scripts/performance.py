#!/usr/bin/env python3
"""Measure a disposable PaperGo server on Linux; never opens the deployment DB.

Requires built current-source API/admin binaries and the embedded frontend.
CPU percentages use one logical core = 100%; RSS excludes OS file cache.
The stdlib HTTP client runs on the same host, so throughput is a lower bound
when client CPU is saturated. This is a closed-loop load test, not an SLO test.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
from collections import Counter
from contextlib import closing
from datetime import datetime, timezone
import http.client
import json
import math
import os
from pathlib import Path
import platform
import socket
import sqlite3
import statistics
import subprocess
import tempfile
import threading
import time

ROOT = Path(__file__).resolve().parent.parent
TOKEN = "papergo-disposable-performance-token"


def process_stats(pid):
    proc = Path(f"/proc/{pid}")
    fields = (proc / "stat").read_text().rsplit(")", 1)[1].split()
    status = dict(line.split(":", 1) for line in (proc / "status").read_text().splitlines())
    io_path = proc / "io"
    io = dict(line.split(":", 1) for line in io_path.read_text().splitlines()) if io_path.exists() else {}
    return {
        "cpu_s": (int(fields[11]) + int(fields[12])) / os.sysconf("SC_CLK_TCK"),
        "rss_mib": int(status["VmRSS"].split()[0]) / 1024,
        "hwm_mib": int(status["VmHWM"].split()[0]) / 1024,
        "threads": int(status["Threads"]),
        "read_bytes": int(io["read_bytes"]) if io else None,
        "write_bytes": int(io["write_bytes"]) if io else None,
    }


def percentile(values, fraction):
    ordered = sorted(values)
    return ordered[max(0, math.ceil(len(ordered) * fraction) - 1)] if ordered else None


class Server:
    def __init__(self, port, pid):
        self.port, self.pid = port, pid
        self.results = []

    def connection(self):
        return http.client.HTTPConnection("127.0.0.1", self.port, timeout=60)

    def request(self, conn, method, path, data=None, headers=None, stream=False):
        h = {"Authorization": f"Bearer {TOKEN}"}
        if data is not None and not isinstance(data, bytes):
            data = json.dumps(data).encode()
            h["Content-Type"] = "application/json"
        h.update(headers or {})
        conn.request(method, path, body=data, headers=h)
        response = conn.getresponse()
        if stream:
            size = 0
            while block := response.read(64 * 1024):
                size += len(block)
            body = b""
        else:
            body = response.read()
            size = len(body)
        return response.status, body, size

    def json(self, method, path, data=None):
        with closing(self.connection()) as conn:
            status, body, _ = self.request(conn, method, path, data)
        if not 200 <= status < 300:
            raise RuntimeError(f"{method} {path}: {status} {body[:1000]!r}")
        return json.loads(body)

    def measure(self, name, action=None, concurrency=1, seconds=10, count=None):
        samples, latencies, statuses, byte_counts = [], [], [], []
        stop_sampling = threading.Event()

        def sample():
            while not stop_sampling.is_set():
                samples.append(process_stats(self.pid))
                stop_sampling.wait(0.05)

        sampler = threading.Thread(target=sample, daemon=True)
        before = process_stats(self.pid)
        client_before = time.process_time()
        sampler.start()
        start = time.monotonic()
        deadline = start + seconds

        def worker(worker_id):
            rows = []
            with closing(self.connection()) as conn:
                iteration = 0
                while (iteration < count if count is not None else time.monotonic() < deadline):
                    tick = time.monotonic()
                    try:
                        status, _, size = action(conn, worker_id, iteration)
                    except (OSError, http.client.HTTPException) as error:
                        status, size = type(error).__name__, 0
                        conn.close()
                    rows.append(((time.monotonic() - tick) * 1000, status, size))
                    iteration += 1
            return rows

        try:
            if action:
                with ThreadPoolExecutor(max_workers=concurrency) as executor:
                    for rows in executor.map(worker, range(concurrency)):
                        for latency, status, size in rows:
                            latencies.append(latency)
                            statuses.append(status)
                            byte_counts.append(size)
            else:
                time.sleep(seconds)
        finally:
            elapsed = time.monotonic() - start
            stop_sampling.set()
            sampler.join()
        after = process_stats(self.pid)
        samples.append(after)
        successes = sum(isinstance(s, int) and 200 <= s < 300 for s in statuses)
        by_status = {}
        for status in set(statuses):
            values = [latency for latency, s in zip(latencies, statuses) if s == status]
            by_status[str(status)] = {"count": len(values), "p50": percentile(values, .5), "p95": percentile(values, .95)}
        result = {
            "name": name, "concurrency": concurrency if action else 0,
            "elapsed_s": elapsed, "requests": len(statuses), "successes": successes,
            "statuses": dict(Counter(str(s) for s in statuses)),
            "successful_requests_per_s": successes / elapsed,
            "latency_ms_all": {"p50": percentile(latencies, .5), "p95": percentile(latencies, .95),
                               "p99": percentile(latencies, .99), "max": max(latencies, default=None)},
            "latency_ms_by_status": by_status,
            "cpu_pct_one_core": 100 * (after["cpu_s"] - before["cpu_s"]) / elapsed,
            "cpu_ms_per_success": 1000 * (after["cpu_s"] - before["cpu_s"]) / successes if successes else None,
            "client_cpu_pct_one_core": 100 * (time.process_time() - client_before) / elapsed,
            "rss_mean_mib": statistics.mean(s["rss_mib"] for s in samples),
            "rss_peak_sampled_mib": max(s["rss_mib"] for s in samples),
            "rss_process_hwm_mib": after["hwm_mib"], "threads_max": max(s["threads"] for s in samples),
            "disk_read_mib": (after["read_bytes"] - before["read_bytes"]) / 2**20 if after["read_bytes"] is not None else None,
            "disk_write_mib": (after["write_bytes"] - before["write_bytes"]) / 2**20 if after["write_bytes"] is not None else None,
            "response_mib_per_s": sum(byte_counts) / 2**20 / elapsed,
        }
        self.results.append(result)
        print(json.dumps(result), flush=True)
        return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, default=ROOT / "bin/papergo")
    parser.add_argument("--admin", type=Path, default=ROOT / "bin/papergo-admin")
    parser.add_argument("--items", type=int, default=2000)
    parser.add_argument("--seconds", type=float, default=10)
    parser.add_argument("--idle-seconds", type=float, default=30)
    parser.add_argument("--concurrency", type=int, nargs="+", default=[1, 8, 32])
    parser.add_argument("--extended", action="store_true", help="also measure 500-row pages and repeated edits")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if platform.system() != "Linux":
        parser.error("Linux /proc is required for process resource measurements")
    if min(args.items, args.seconds, args.idle_seconds, *args.concurrency) <= 0:
        parser.error("items, durations and concurrency must be positive")
    args.binary, args.admin = args.binary.resolve(), args.admin.resolve()
    # Explicitly clear deployment configuration; the benchmark must be isolated.
    env = {k: v for k, v in os.environ.items() if k in ("PATH", "HOME", "TMPDIR", "LANG", "TZ", "SYSTEMROOT")}
    env.update(APP_ENV="test", AUTH_MODE="development", DEV_TOKEN=TOKEN,
               DEV_SUBJECT="performance-admin", ADMIN_SUBJECT="performance-admin", NODE_ID="performance")
    with tempfile.TemporaryDirectory(prefix="papergo-performance-") as temp:
        directory = Path(temp)
        dbpath = directory / "papergo.db"
        subprocess.run([str(args.admin), "initialize", "--database", str(dbpath), "--schema", str(ROOT / "migrations")],
                       cwd=ROOT, env=env, check=True, stdout=subprocess.DEVNULL)
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        env.update(HTTP_ADDR=f"127.0.0.1:{port}", DATABASE_PATH=str(dbpath), BLOB_PATH=str(directory / "blobs"))
        with (directory / "server.log").open("w+") as log:
            started = time.monotonic()
            process = subprocess.Popen([str(args.binary)], env=env, cwd=ROOT, stdout=log, stderr=log)
            server = Server(port, process.pid)
            try:
                for _ in range(300):
                    if process.poll() is not None:
                        raise RuntimeError("server exited before readiness")
                    try:
                        server.json("GET", "/health/ready")
                        break
                    except (OSError, RuntimeError, http.client.HTTPException):
                        time.sleep(.1)
                else:
                    raise RuntimeError("server did not become ready")
                ready_s = time.monotonic() - started
                server.measure("idle-empty", seconds=args.idle_seconds)
                workspace = server.json("POST", "/v1/workspaces", {"name": "Performance"})["id"]
                collection = server.json("POST", f"/v1/resources/{workspace}/children", {"kind": "list", "name": "Invoices"})["id"]
                for key, fieldtype in [("n", "integer"), ("status", "text")]:
                    server.json("POST", f"/v1/resources/{collection}/fields", {"key": key, "label": key, "type": fieldtype, "indexed": True})

                def seed(conn, _, iteration):
                    operations = [{"action": "create", "create": {
                        "name": f"Invoice {i:06d} Acme", "tags": ["finance"],
                        "values": {"n": i, "status": ["open", "closed", "held"][i % 3]}}}
                        for i in range(iteration * 100, min(args.items, (iteration + 1) * 100))]
                    return server.request(conn, "POST", f"/v1/resources/{collection}/bulk", {"operations": operations})

                seeded = server.measure("seed-bulk-100", seed, count=math.ceil(args.items / 100))
                if seeded["successes"] != math.ceil(args.items / 100):
                    raise RuntimeError("fixture seeding failed")
                # Drain domain events before attributing resource use to idle/reads.
                drain_deadline = time.monotonic() + 60
                while True:
                    with sqlite3.connect(dbpath) as db:
                        pending = db.execute("SELECT count(*) FROM domain_events WHERE dispatched_at IS NULL").fetchone()[0]
                    if pending == 0:
                        break
                    if time.monotonic() > drain_deadline:
                        raise RuntimeError("event dispatcher did not drain fixture events")
                    time.sleep(.2)
                server.measure("idle-seeded", seconds=args.idle_seconds)
                with sqlite3.connect(dbpath) as db:
                    fixture = {"page_count": db.execute("PRAGMA page_count").fetchone()[0],
                               "page_size": db.execute("PRAGMA page_size").fetchone()[0],
                               "database_mib": dbpath.stat().st_size / 2**20,
                               "wal_mib": Path(str(dbpath) + "-wal").stat().st_size / 2**20,
                               "rows": {table: db.execute(f"SELECT count(*) FROM {table}").fetchone()[0]
                                        for table in ["resources", "item_revisions", "audit_events", "domain_events", "workflow_runs"]}}
                streams = [server.connection() for _ in range(4)]
                try:
                    for stream in streams:
                        stream.request("GET", "/v1/events", headers={"Authorization": f"Bearer {TOKEN}"})
                        response = stream.getresponse()
                        if response.status != 200:
                            raise RuntimeError("SSE stream failed")
                        response.readline()
                    server.measure("idle-four-live-streams", seconds=args.idle_seconds)
                finally:
                    for stream in streams:
                        stream.close()

                browse = f"/v1/resources/{collection}/children?$top=50"
                query = f"/v1/resources/{collection}/query?$top=50"
                for concurrency in args.concurrency:
                    server.measure(f"browse-c{concurrency}", lambda c, w, i: server.request(c, "GET", browse),
                                   concurrency, args.seconds)
                for name, body in [
                    ("indexed-sort", {"query": {"sort": [{"field": "n"}]}}),
                    ("selective-filter", {"query": {"sort": [{"field": "$modified_at"}], "filter": {"field": "n", "value": 77}}}),
                    ("fts-search", {"query": {"search": "Invoice", "sort": [{"field": "$name"}]}}),
                    ("include-total", {"query": {}, "include_total": True}),
                ]:
                    server.json("POST", query, body)  # Fail clearly on an invalid workload.
                    server.measure(name, lambda c, w, i, b=body: server.request(c, "POST", query, b), seconds=args.seconds)
                if args.extended:
                    for concurrency in [1, 8]:
                        server.measure(f"browse-500-c{concurrency}",
                                       lambda c, w, i: server.request(c, "GET", browse.replace("$top=50", "$top=500")),
                                       concurrency, args.seconds)
                        editable = [server.json("POST", f"/v1/resources/{collection}/children", {
                            "kind": "item", "name": f"Edit target {concurrency} {i}", "values": {"n": i, "status": "open"}})
                            for i in range(concurrency)]
                        def edit(conn, worker, iteration):
                            item = editable[worker]
                            result = server.request(conn, "PATCH", f"/v1/resources/{item['id']}",
                                                    {"values": {"n": iteration}}, {"If-Match": f'"{item["version"]}"'})
                            if 200 <= result[0] < 300:
                                item["version"] = json.loads(result[1])["version"]
                            return result
                        server.measure(f"edit-c{concurrency}", edit, concurrency, args.seconds)
                for concurrency in [c for c in args.concurrency if c <= 32]:
                    def mixed(conn, worker, iteration):
                        if iteration % 5:
                            return server.request(conn, "GET", browse)
                        return server.request(conn, "POST", f"/v1/resources/{collection}/children", {
                            "kind": "item", "name": f"Mixed c{concurrency} w{worker} i{iteration}",
                            "values": {"n": iteration, "status": "open"}})
                    server.measure(f"mixed-80read-20create-c{concurrency}", mixed, concurrency, args.seconds)
                library = server.json("POST", f"/v1/resources/{workspace}/children", {"kind": "library", "name": "Files"})["id"]
                documents = [server.json("POST", f"/v1/resources/{library}/children", {"kind": "item", "name": f"File {i}"}) for i in range(4)]
                for size_mib, concurrency in [(16, 1), (64, 1), (16, 4)]:
                    payload = b"%PDF-1.7\n" + b"0" * (size_mib * 2**20 - 9)
                    def upload(conn, worker, iteration):
                        item = documents[worker]
                        result = server.request(conn, "PUT", f"/v1/items/{item['id']}/content", payload,
                                                {"Content-Type": "application/pdf", "If-Match": f'"{item["version"]}"',
                                                 "X-Filename": f"sample-{worker}.pdf"})
                        if 200 <= result[0] < 300:
                            item["version"] = json.loads(result[1])["resource_version"]
                        return result
                    server.measure(f"upload-{size_mib}MiB-c{concurrency}", upload, concurrency, count=1)
                for concurrency in [1, 4]:
                    server.measure(f"download-16MiB-c{concurrency}",
                                   lambda c, w, i: server.request(c, "GET", f"/v1/items/{documents[w]['id']}/content", stream=True),
                                   concurrency, args.seconds)
                server.measure("idle-after-load", seconds=args.idle_seconds)
                with sqlite3.connect(dbpath) as db:
                    counts = {table: db.execute(f"SELECT count(*) FROM {table}").fetchone()[0]
                              for table in ["resources", "item_revisions", "audit_events", "domain_events", "workflow_runs", "blobs"]}
                result = {
                    "measured_at_utc": datetime.now(timezone.utc).isoformat(),
                    "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
                    "platform": platform.platform(), "logical_cpus": os.cpu_count(),
                    "go_version": subprocess.check_output(["go", "version"], text=True).strip(),
                    "binary_bytes": args.binary.stat().st_size, "initial_items": args.items,
                    "configuration": {"auth_mode": "development", "max_in_flight": 32, "request_timeout": "30s",
                                      "max_upload_mib": 64, "extended": args.extended},
                    "fixture_storage": fixture,
                    "startup_ready_s": ready_s, "stages": server.results, "final_table_rows": counts,
                    "database_mib": dbpath.stat().st_size / 2**20,
                    "wal_mib": Path(str(dbpath) + "-wal").stat().st_size / 2**20 if Path(str(dbpath) + "-wal").exists() else 0,
                    "blob_mib": sum(f.stat().st_size for f in (directory / "blobs").iterdir() if f.is_file()) / 2**20,
                    "server_log_mib": (directory / "server.log").stat().st_size / 2**20,
                }
                args.output.parent.mkdir(parents=True, exist_ok=True)
                args.output.write_text(json.dumps(result, indent=2) + "\n")
            except Exception:
                log.flush()
                print((directory / "server.log").read_text()[-6000:])
                raise
            finally:
                process.terminate()
                try:
                    process.wait(timeout=30)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()


if __name__ == "__main__":
    main()
