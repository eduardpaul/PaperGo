#!/usr/bin/env python3
"""Provision NPM access-list authentication and TLS for PaperGo."""
import argparse
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parent
GENERATED = ROOT / "generated"


def private_write(path, content):
    with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as f:
        f.write(content)
    os.chmod(path, 0o600)


def compose(*args):
    subprocess.run(["docker", "compose", "--env-file", str(ROOT / ".env"),
                    "-f", str(ROOT / "compose.yaml"), *args], check=True)


class API:
    def __init__(self, url):
        self.url, self.token = url, None

    def call(self, method, path, data=None):
        headers = {"Content-Type": "application/json"}
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        request = urllib.request.Request(self.url + path, method=method, headers=headers,
                                         data=None if data is None else json.dumps(data).encode())
        with urllib.request.urlopen(request, timeout=180) as response:
            return json.load(response)

    def upload(self, path, files):
        boundary = secrets.token_hex(24)
        chunks = []
        for name, file in files.items():
            chunks.extend([f"--{boundary}\r\nContent-Disposition: form-data; name=\"{name}\"; filename=\"{file.name}\"\r\nContent-Type: application/octet-stream\r\n\r\n".encode(), file.read_bytes(), b"\r\n"])
        chunks.append(f"--{boundary}--\r\n".encode())
        request = urllib.request.Request(self.url + path, data=b"".join(chunks),
                                         headers={"Authorization": "Bearer " + self.token,
                                                  "Content-Type": "multipart/form-data; boundary=" + boundary})
        with urllib.request.urlopen(request, timeout=60) as response:
            return json.load(response)


def local_certificate(host):
    ca_key, ca_cert = GENERATED / "ca.key", GENERATED / "ca.crt"
    key, csr, cert = GENERATED / "server.key", GENERATED / "server.csr", GENERATED / "server.crt"
    run = lambda *args: subprocess.run(["openssl", *args], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if not ca_cert.exists():
        run("req", "-x509", "-newkey", "rsa:3072", "-nodes", "-sha256", "-days", "3650", "-subj", "/CN=PaperGo local sample CA", "-addext", "basicConstraints=critical,CA:TRUE", "-addext", "keyUsage=critical,keyCertSign,cRLSign", "-keyout", str(ca_key), "-out", str(ca_cert))
    if not cert.exists():
        run("req", "-new", "-newkey", "rsa:3072", "-nodes", "-subj", "/CN=" + host, "-keyout", str(key), "-out", str(csr))
        extensions = GENERATED / "certificate.ext"
        private_write(extensions, f"subjectAltName=DNS:{host}\nbasicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectKeyIdentifier=hash\nauthorityKeyIdentifier=keyid,issuer\n")
        run("x509", "-req", "-in", str(csr), "-CA", str(ca_cert), "-CAkey", str(ca_key), "-CAcreateserial", "-out", str(cert), "-days", "365", "-sha256", "-extfile", str(extensions))
    return cert, key, ca_cert


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", required=True, help="Public PaperGo hostname")
    parser.add_argument("--email", required=True, help="NPM administrator email")
    parser.add_argument("--tls", choices=["letsencrypt", "local"], default="letsencrypt")
    parser.add_argument("--admin-user", default="papergo-admin")
    parser.add_argument("--http-port", type=int, default=80)
    parser.add_argument("--https-port", type=int, default=443)
    parser.add_argument("--admin-port", type=int, default=8181)
    parser.add_argument("--no-build", action="store_true", help="Use the already built PaperGo image when reconfiguring this stack")
    parser.add_argument("--subnet", default="172.29.242.0/24")
    args = parser.parse_args()
    if not re.fullmatch(r"[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?", args.host) or ".." in args.host:
        parser.error("host must be a DNS hostname without a scheme, port or path")
    if not re.fullmatch(r"[a-zA-Z0-9._@-]{1,200}", args.admin_user):
        parser.error("admin username must use letters, digits, dots, underscores, @ or hyphens")
    if not re.fullmatch(r"[^\s@]+@[^\s@]+", args.email):
        parser.error("email must be an email address")
    for port in [args.http_port, args.https_port, args.admin_port]:
        if not 1 <= port <= 65535:
            parser.error("ports must be between 1 and 65535")
    if args.tls == "letsencrypt" and (args.http_port != 80 or args.host.endswith(".localhost")):
        parser.error("Let's Encrypt requires a public DNS hostname and HTTP port 80")
    network = ipaddress.ip_network(args.subnet)
    if network.version != 4 or not network.is_private or network.prefixlen < 24 or network.prefixlen > 28:
        parser.error("subnet must be a dedicated private IPv4 /24 to /28 network")
    os.umask(0o077)
    GENERATED.mkdir(mode=0o700, exist_ok=True)
    settings = {key: value for key, value in vars(args).items() if key != "no_build"}
    settings_file = GENERATED / "deployment.json"
    if settings_file.exists() and json.loads(settings_file.read_text()) != settings:
        parser.error("existing deployment settings differ; use the recorded settings for this stack")
    private_write(settings_file, json.dumps(settings, indent=2) + "\n")
    credentials_file = GENERATED / "credentials.json"
    if credentials_file.exists():
        credentials = json.loads(credentials_file.read_text())
    else:
        credentials = {"npm_email": args.email, "npm_password": secrets.token_urlsafe(32),
                       "papergo_user": args.admin_user, "papergo_password": secrets.token_urlsafe(32)}
        private_write(credentials_file, json.dumps(credentials, indent=2) + "\n")
    proxy_secret = GENERATED / "proxy-secret"
    if not proxy_secret.exists():
        private_write(proxy_secret, secrets.token_hex(32))
    secret = proxy_secret.read_text().strip()
    if not re.fullmatch(r"[0-9a-f]{64}", secret):
        raise RuntimeError("generated/proxy-secret must contain exactly 64 lowercase hexadecimal characters")
    public_url = "https://" + args.host + ("" if args.https_port == 443 else f":{args.https_port}")
    private_write(ROOT / ".env", "\n".join([f"PUBLIC_URL={public_url}", f"PAPERGO_ADMIN={args.admin_user}",
                   f"HTTP_PORT={args.http_port}", f"HTTPS_PORT={args.https_port}", f"ADMIN_PORT={args.admin_port}",
                   f"PRIVATE_SUBNET={network}", f"NPM_PRIVATE_IP={network.network_address + 2}",
                   f"PAPERGO_PRIVATE_IP={network.network_address + 3}"]) + "\n")
    private_write(GENERATED / "curl.netrc", f"machine {args.host}\nlogin {args.admin_user}\npassword {credentials['papergo_password']}\n")
    compose("up", *([] if args.no_build else ["--build"]), "-d", "--wait", "--wait-timeout", "240")
    api = API(f"http://127.0.0.1:{args.admin_port}/api")
    for attempt in range(60):
        try:
            api.token = api.call("POST", "/tokens", {"identity": args.email, "secret": credentials["npm_password"]})["token"]
            break
        except urllib.error.HTTPError as error:
            if error.code not in (400, 401, 403):
                raise
            # The public setup endpoint creates the first management account only.
            if attempt == 0:
                api.call("POST", "/users", {"name": "PaperGo Proxy Administrator", "nickname": "Admin", "email": args.email,
                         "auth": {"type": "password", "secret": credentials["npm_password"]}})
        except (urllib.error.URLError, ConnectionError):
            pass
        time.sleep(2)
    else:
        raise RuntimeError("NPM management API did not become ready")
    access_lists = api.call("GET", "/nginx/access-lists")
    access = next((item for item in access_lists if item["name"] == "PaperGo users"), None)
    if access is None:
        access = api.call("POST", "/nginx/access-lists", {"name": "PaperGo users", "satisfy_any": False, "pass_auth": False,
                         "items": [{"username": args.admin_user, "password": credentials["papergo_password"]}], "clients": []})
    if access.get("pass_auth") or access.get("satisfy_any"):
        raise RuntimeError("PaperGo Access List must disable Pass Auth and Satisfy Any")
    certificates = api.call("GET", "/nginx/certificates")
    certificate = next((item for item in certificates if args.host in item.get("domain_names", [])), None)
    if certificate is None:
        if args.tls == "local":
            certificate = api.call("POST", "/nginx/certificates", {"provider": "other", "nice_name": "PaperGo local TLS"})
        else:
            certificate = api.call("POST", "/nginx/certificates", {"provider": "letsencrypt", "domain_names": [args.host], "meta": {"dns_challenge": False}})
    if args.tls == "local" and certificate is not None:
        cert, key, ca = local_certificate(args.host)
        api.upload(f"/nginx/certificates/{certificate['id']}/upload", {"certificate": cert, "certificate_key": key, "intermediate_certificate": ca})
    headers = (f'proxy_set_header X-Papergo-Subject $remote_user;\n'
               f'proxy_set_header X-Papergo-Proxy-Secret "{secret}";\n'
               'proxy_request_buffering off;\nproxy_buffering off;\n'
               'proxy_read_timeout 300s;\nproxy_send_timeout 300s;\n')
    server_config = "client_max_body_size 80m;\n"
    # NPM's Force SSL redirect targets port 443. Custom published TLS ports
    # need an explicit public-origin redirect in the supported Advanced field.
    if args.https_port != 443:
        server_config += f'if ($scheme = http) {{ return 308 {public_url}$request_uri; }}\n'
    host_config = {"domain_names": [args.host], "forward_scheme": "http", "forward_host": "papergo", "forward_port": 8080,
                   "certificate_id": certificate["id"], "ssl_forced": args.https_port == 443, "hsts_enabled": True, "hsts_subdomains": False,
                   "http2_support": True, "block_exploits": False, "caching_enabled": False, "allow_websocket_upgrade": True,
                   "access_list_id": access["id"], "advanced_config": server_config,
                   "locations": [{"path": "/", "forward_scheme": "http", "forward_host": "papergo", "forward_port": 8080,
                                  "forward_path": "", "access_list_id": access["id"], "advanced_config": headers}]}
    hosts = api.call("GET", "/nginx/proxy-hosts")
    host = next((item for item in hosts if args.host in item["domain_names"]), None)
    result = api.call("PUT" if host else "POST", f"/nginx/proxy-hosts/{host['id']}" if host else "/nginx/proxy-hosts", host_config)
    if result.get("meta", {}).get("nginx_online") is False:
        raise RuntimeError("NPM rejected the generated proxy configuration; inspect the NPM management UI")
    print(f"PaperGo configured at {public_url}/v1/identity/me")
    print(f"NPM management UI: http://127.0.0.1:{args.admin_port}")
    print("Credentials are stored privately in generated/credentials.json; curl credentials in generated/curl.netrc.")
    if args.tls == "local":
        print("Use generated/ca.crt to verify the local TLS certificate; no TLS verification bypass is needed.")


if __name__ == "__main__":
    try:
        main()
    except urllib.error.HTTPError as error:
        raise SystemExit(f"NPM API request failed (HTTP {error.code}); inspect the management UI. Credentials were not printed.")
