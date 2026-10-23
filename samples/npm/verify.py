#!/usr/bin/env python3
"""Exercise the running sample through NPM with verified HTTPS."""
import argparse
import base64
import http.client
import json
import re
from pathlib import Path
import secrets
import socket
import ssl
import subprocess
import time
from urllib.parse import quote
from setup import verify_egress

ROOT = Path(__file__).resolve().parent


def load_deployment():
    generated = ROOT / "generated"
    try:
        deployment = json.loads((generated / "deployment.json").read_text())
        public_origin = "https://" + deployment["host"] + ("" if deployment["https_port"] == 443 else f":{deployment['https_port']}")
        if (generated / "ready").read_text().strip() != public_origin:
            raise RuntimeError("Setup completion does not match the recorded public origin; rerun setup.py.")
        login = json.loads((generated / "credentials.json").read_text())
        tls = ssl.create_default_context(cafile=str(generated / "ca.crt") if deployment["tls"] == "local" else None)
    except (OSError, ValueError, KeyError):
        raise RuntimeError("Sample setup is incomplete or its generated files are invalid. Resolve the setup.py error and rerun it successfully before running verify.py.") from None
    return deployment, login, public_origin, tls


class LocalHTTPS(http.client.HTTPSConnection):
    def connect(self):
        self.sock = context.wrap_socket(socket.create_connection(("127.0.0.1", self.port), self.timeout), server_hostname=self.host)


def request(method, path, body=None, *, authenticated=True, headers=None, expected=200, chunked=False):
    connection = (LocalHTTPS if settings["tls"] == "local" else http.client.HTTPSConnection)(hostname, port, timeout=30, context=context)
    fields = dict(headers or {})
    if authenticated:
        login = credentials["papergo_user"] + ":" + credentials["papergo_password"]
        fields["Authorization"] = "Basic " + base64.b64encode(login.encode()).decode()
    if isinstance(body, dict):
        body = json.dumps(body).encode()
        fields["Content-Type"] = "application/json"
    connection.request(method, path, body=body, headers=fields, encode_chunked=chunked)
    response = connection.getresponse()
    data = response.read()
    status = response.status
    connection.close()
    if status != expected:
        raise AssertionError(f"{method} {path}: expected {expected}, got {status}")
    return json.loads(data) if data and data.lstrip().startswith((b"{", b"[")) else data


def mcp(token, method, params, expected=200):
    """One stateless MCP request to /v1/mcp with a bearer token and no Basic login."""
    headers = {"Accept": "application/json, text/event-stream", "MCP-Protocol-Version": "2025-06-18"}
    if token:
        headers["Authorization"] = "Bearer " + token
    return request("POST", "/v1/mcp", {"jsonrpc": "2.0", "id": 1, "method": method, "params": params},
                   authenticated=False, headers=headers, expected=expected)


def verify_mcp(workspace, library):
    """A person signed in through NPM connects an assistant limited to one
    library; the assistant reaches papergo-mcp past the Basic sign-in with
    that token alone, and sees only that library."""
    connection = request("POST", "/v1/identity/me/tokens", {"name": "NPM verification assistant", "scopes": ["workspace.read", "list.read"],
                                                            "resources": [library["id"]], "expires_in_days": 1}, expected=201)
    try:
        mcp(None, "tools/list", {}, expected=401)
        request("GET", "/v1/identity/me", authenticated=False, headers={"Authorization": "Bearer " + connection["secret"]}, expected=401)
        initialized = mcp(connection["secret"], "initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                                               "clientInfo": {"name": "verify", "version": "1"}})
        assert initialized["result"]["serverInfo"]["name"], initialized
        listed = mcp(connection["secret"], "tools/call", {"name": "list_collections", "arguments": {"workspace_id": workspace["id"]}})
        collections = listed["result"]["structuredContent"]["value"]
        assert [c["id"] for c in collections] == [library["id"]], listed
        workspaces = mcp(connection["secret"], "tools/call", {"name": "list_workspaces", "arguments": {}})
        assert [w["id"] for w in workspaces["result"]["structuredContent"]["value"]] == [workspace["id"]], workspaces
    finally:
        request("DELETE", f"/v1/identity/me/tokens/{connection['token']['id']}", expected=204)


def main():
    global settings, credentials, hostname, port, origin, context
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--large-upload", action="store_true", help="Also verify a 64 MiB upload and download at the sample's configured limit")
    args = parser.parse_args()
    settings, credentials, origin, context = load_deployment()
    verify_egress()
    hostname, port = settings["host"], settings["https_port"]
    plain = http.client.HTTPConnection("127.0.0.1" if settings["tls"] == "local" else hostname, settings["http_port"], timeout=30)
    plain.request("GET", "/v1/identity/me", headers={"Host": hostname})
    redirect = plain.getresponse()
    assert redirect.status in (301, 308) and redirect.getheader("Location") == origin + "/v1/identity/me"
    redirect.read()
    plain.close()
    request("GET", "/", authenticated=False, expected=401)
    request("GET", "/auth/config", authenticated=False, expected=401)
    request("GET", "/v1/identity/me", authenticated=False, expected=401)
    assert request("GET", "/auth/config") == {"mode": "proxy"}
    index = request("GET", "/")
    assert b'<div id="root">' in index and request("GET", "/admin/users") == index
    assets = re.findall(rb'(?:src|href)="(/assets/[^" ]+)"', index)
    assert assets, "frontend entry assets are missing"
    for asset in assets:
        assert request("GET", asset.decode()), "frontend asset is empty"
    request("GET", assets[0].decode(), authenticated=False, expected=401)
    request("GET", "/assets/missing.js", expected=404)
    me = request("GET", "/v1/identity/me")
    assert me["subject"] == credentials["papergo_user"] and "workspace.manage" in me["scopes"]
    assert request("GET", "/v1/search/status")["store"] == "zvec", "NPM sample must use native zvec search"
    spoofed = request("GET", "/v1/identity/me", headers={"X-Papergo-Subject": "forged-admin", "X-Papergo-Proxy-Secret": "forged"})
    assert spoofed["subject"] == me["subject"]
    assert isinstance(request("GET", "/v1/identity/me/tokens"), list)
    batch = request("POST", "/v1/$batch", {"requests": [{"id": "me", "method": "GET", "url": "identity/me"}]})
    assert batch["responses"][0]["status"] == 200 and batch["responses"][0]["body"]["subject"] == me["subject"]
    request("POST", "/v1/$batch", {"requests": [{"id": "me", "method": "GET", "url": "identity/me", "headers": {"X-Papergo-Subject": "forged"}}]}, expected=400)
    request("POST", "/v1/workspaces", {"name": "Cross-origin attempt"}, headers={"Origin": "https://attacker.example"}, expected=401)
    workspace = request("POST", "/v1/workspaces", {"name": "NPM verification " + secrets.token_hex(4)}, headers={"Origin": origin, "Sec-Fetch-Site": "same-origin"}, expected=201)
    library = request("POST", f"/v1/resources/{workspace['id']}/children", {"kind": "library", "name": "Documents", "webdav_enabled": True}, expected=201)
    searchable = "Zvecverify" + secrets.token_hex(6)
    item = request("POST", f"/v1/resources/{library['id']}/children", {"kind": "item", "name": searchable}, expected=201)
    deadline = time.monotonic() + 60
    while True:
        result = request("GET", f"/v1/search?q={searchable}&workspace_id={workspace['id']}")
        if any(hit["id"] == item["id"] for hit in result["value"]):
            break
        if time.monotonic() >= deadline:
            raise AssertionError("Native zvec keyword search did not index the verification item")
        time.sleep(0.25)
    folder = f"/webdav/{library['id']}/" + quote("Sample Ω")
    request("MKCOL", folder, expected=201)
    content = b"%PDF-1.7\nPaperGo NPM verification\n%%EOF\n"
    request("PUT", folder + "/example.pdf", content, headers={"Content-Type": "application/pdf"}, expected=201)
    assert request("GET", folder + "/example.pdf") == content
    request("MOVE", folder + "/example.pdf", headers={"Destination": origin + folder + "/moved.pdf"}, expected=201)
    assert request("GET", folder + "/moved.pdf") == content
    request("PUT", folder + "/chunked.pdf", iter([content[:9], content[9:]]), headers={"Content-Type": "application/pdf"}, expected=201, chunked=True)
    assert request("GET", folder + "/chunked.pdf") == content
    request("COPY", folder + "/chunked.pdf", headers={"Destination": origin + folder + "/copied.pdf"}, expected=201)
    assert request("GET", folder + "/copied.pdf") == content
    verify_mcp(workspace, library)
    if args.large_upload:
        large = content + b"\0" * (64 * 1024 * 1024 - len(content))
        request("PUT", folder + "/large.pdf", large, headers={"Content-Type": "application/pdf"}, expected=201)
        assert request("GET", folder + "/large.pdf") == large
        print("Verified a 64 MiB upload and download at MAX_UPLOAD_BYTES.")
    command = ["docker", "compose", "--env-file", str(ROOT / ".env"), "-f", str(ROOT / "compose.yaml")]
    for service in ("papergo", "papergo-mcp"):
        cid = subprocess.check_output(command + ["ps", "-q", service], text=True).strip()
        ports = json.loads(subprocess.check_output(["docker", "inspect", "--format", "{{json .NetworkSettings.Ports}}", cid], text=True))
        assert not any(ports.values()), f"{service} was published on the host"
    print("Verified embedded UI, deep links, frontend assets, proxy auth configuration, HTTP-to-HTTPS redirect, HTTPS, NPM login, header-spoofing rejection, batch identity, CSRF rejection, private backends, REST writes, Unicode WebDAV PUT/GET/MOVE/COPY with chunked uploads, and an AI assistant connection limited to one library through /v1/mcp.")
    print(f"Test workspace: {workspace['id']}; WebDAV library: {library['id']}")


if __name__ == "__main__":
    try:
        main()
    except ssl.SSLCertVerificationError:
        raise SystemExit("NPM's served TLS certificate does not verify with the configured trust roots. Rerun setup.py --no-build to install the configured certificate and wait for verified HTTPS. TLS verification remains enabled.")
    except RuntimeError as error:
        raise SystemExit(str(error))
