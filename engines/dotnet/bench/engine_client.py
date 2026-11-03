"""A stdlib client for the engine protocol over a Unix socket."""

import http.client
import json
import os
import socket
import subprocess
import time


class UnixConnection(http.client.HTTPConnection):
    def __init__(self, path, timeout=3600):
        super().__init__("localhost", timeout=timeout)
        self.path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.path)


class EngineError(Exception):
    def __init__(self, status, body):
        super().__init__(f"{status}: {body}")
        self.status = status
        self.body = body


class Engine:
    """A running papergo-engine process."""

    def __init__(self, exe, sock, args, env=None):
        self.sock = sock
        if os.path.exists(sock):
            os.unlink(sock)
        self.started = time.monotonic()
        self.proc = subprocess.Popen([exe, "--listen", "unix:" + sock, "--exit-on-stdin-eof", *args],
                                     stdin=subprocess.PIPE, env=env)
        self.ready_seconds = None

    def wait_ready(self, timeout=60):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if self.proc.poll() is not None:
                raise RuntimeError(f"engine exited with {self.proc.returncode}")
            if os.path.exists(self.sock):
                try:
                    if self.request("GET", "/v1/health")[0] == 200:
                        self.ready_seconds = time.monotonic() - self.started
                        return self.ready_seconds
                except OSError:
                    pass
            time.sleep(0.005)
        raise TimeoutError("engine did not become healthy")

    def request(self, method, path, body=None, headers=None):
        conn = UnixConnection(self.sock)
        try:
            conn.request(method, path, body=body, headers=headers or {})
            r = conn.getresponse()
            data = r.read()
            return r.status, data, dict(r.getheaders())
        finally:
            conn.close()

    def json(self, method, path, payload=None):
        body = None if payload is None else json.dumps(payload, ensure_ascii=False).encode()
        status, data, _ = self.request(method, path, body, {"Content-Type": "application/json"} if body else {})
        if status != 200:
            raise EngineError(status, data.decode(errors="replace"))
        return json.loads(data)

    def ocr(self, kind, pixels, width, height, fmt, query=""):
        status, data, _ = self.request("POST", f"/v1/ocr/{kind}{query}", pixels, {
            "Content-Type": "application/octet-stream",
            "X-Image-Width": str(width), "X-Image-Height": str(height), "X-Pixel-Format": fmt,
        })
        if status != 200:
            raise EngineError(status, data.decode(errors="replace"))
        return json.loads(data)

    def rss(self):
        """VmRSS and VmHWM in MiB."""
        out = {}
        with open(f"/proc/{self.proc.pid}/status") as f:
            for line in f:
                if line.startswith(("VmRSS:", "VmHWM:")):
                    k, v = line.split(":")
                    out[k] = int(v.split()[0]) / 1024
        return out["VmRSS"], out["VmHWM"]

    def reset_peak(self):
        """Resets VmHWM (Linux clear_refs 5)."""
        with open(f"/proc/{self.proc.pid}/clear_refs", "w") as f:
            f.write("5")

    def close(self):
        if self.proc.poll() is None:
            self.proc.stdin.close()
            try:
                self.proc.wait(timeout=30)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait()
