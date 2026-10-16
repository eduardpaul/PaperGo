#!/usr/bin/env python3
"""Provision NPM access-list authentication and TLS for PaperGo."""
import argparse
import getpass
import base64
import http.client
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import socket
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parent
GENERATED = ROOT / "generated"


def private_write(path, content):
    with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as f:
        f.write(content)
    os.chmod(path, 0o600)


def compose(*args, check=True):
    subprocess.run(["docker", "compose", "--env-file", str(ROOT / ".env"),
                    "-f", str(ROOT / "compose.yaml"), *args], check=check)


def start_stack(no_build):
    try:
        compose("up", *([] if no_build else ["--build"]), "-d", "--wait", "--wait-timeout", "240")
    except subprocess.CalledProcessError as error:
        print("Docker Compose startup failed; recent service logs:", flush=True)
        try:
            compose("logs", "--no-color", "--tail", "80",
                    "volume-permissions", "initialize", "papergo", "npm", check=False)
        except OSError:
            pass
        raise RuntimeError(
            f"Docker Compose startup failed (exit {error.returncode}). Setup is incomplete. "
            "Inspect the service logs above. If initialize reports a schema checksum mismatch, "
            "recreate the disposable PaperGo data volume using the commands in "
            "samples/npm/README.md under Storage and operations, then rerun setup."
        ) from None



def verify_egress():
    """Resolve and reach public HTTPS from the actual PaperGo container."""
    try:
        compose("exec", "-T", "papergo", "/papergo-admin", "health", "--url",
                "https://calendar.google.com/", "--timeout", "15s")
    except subprocess.CalledProcessError:
        raise RuntimeError(
            "PaperGo cannot reach calendar.google.com over verified HTTPS. "
            "Allow outbound DNS and TCP 443 from the backend Docker network; "
            "check the host firewall and samples/npm/README.md before rerunning setup."
        ) from None


def reset_management_password(credentials):
    # Use the pinned NPM image's password model, including its bcrypt update hook.
    # Credentials go over stdin, never command arguments, environment or logs.
    script = """
import authModel from '/app/models/auth.js';
import userModel from '/app/models/user.js';
let input = '';
for await (const chunk of process.stdin) input += chunk;
const credentials = JSON.parse(input);
const user = await userModel.query().select('id', 'roles')
  .where('email', credentials.email.toLowerCase().trim())
  .where('is_deleted', 0).where('is_disabled', 0).first();
if (!user || !user.roles.includes('admin')) process.exit(2);
const changed = await authModel.query().where('user_id', user.id)
  .where('type', 'password').where('is_deleted', 0)
  .patch({type: 'password', secret: credentials.password});
process.exit(changed === 1 ? 0 : 3);
"""
    result = subprocess.run(
        ["docker", "compose", "--env-file", str(ROOT / ".env"), "-f", str(ROOT / "compose.yaml"),
         "exec", "-T", "npm", "node", "--input-type=module", "-e", script],
        input=json.dumps({"email": credentials["npm_email"], "password": credentials["npm_password"]}),
        text=True, capture_output=True,
    )
    if result.returncode != 0:
        raise RuntimeError("NPM password reset failed. The recorded npm_email must identify an active administrator with password authentication. Credentials were not printed.")
    print("NPM administrator password reset to the value in generated/credentials.json.")


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


def authenticate(api, credentials):
    for attempt in range(60):
        try:
            status = api.call("GET", "/")
            break
        except (urllib.error.URLError, ConnectionError):
            time.sleep(2)
    else:
        raise RuntimeError("NPM management API did not become ready")
    if status.get("setup") is False:
        api.call("POST", "/users", {"name": "PaperGo Proxy Administrator", "nickname": "Admin",
                 "email": credentials["npm_email"],
                 "auth": {"type": "password", "secret": credentials["npm_password"]}})
    try:
        result = api.call("POST", "/tokens", {"identity": credentials["npm_email"], "secret": credentials["npm_password"]})
    except urllib.error.HTTPError as error:
        error.close()
        if error.code not in (400, 401, 403):
            raise
        if not sys.stdin.isatty():
            raise RuntimeError(
                "NPM rejected the recorded management credentials. Run setup.py in an interactive terminal "
                "to enter the existing administrator login privately, or set npm_email and npm_password "
                "in generated/credentials.json before unattended setup. Credentials were not printed."
            ) from None
        print("NPM is already initialized. Enter its administrator login to configure this stack.")
        email = input(f"NPM administrator email [{credentials['npm_email']}]: ").strip() or credentials['npm_email']
        password = getpass.getpass("NPM administrator password: ")
        try:
            result = api.call("POST", "/tokens", {"identity": email, "secret": password})
        except urllib.error.HTTPError as error:
            error.close()
            if error.code not in (400, 401, 403):
                raise
            raise RuntimeError(
                "NPM rejected the administrator email or password. Use the NPM management password, "
                "which is separate from the PaperGo application password. If it is lost, rerun with "
                "--reset-npm-admin-password to replace that administrator's password with the recorded "
                "npm_password while retaining configuration and data. Credentials were not printed."
            ) from None
        credentials = {**credentials, "npm_email": email, "npm_password": password}
    if result.get("requires_2fa"):
        if not sys.stdin.isatty():
            raise RuntimeError("NPM requires a two-factor code. Run setup.py in an interactive terminal to complete login.")
        code = getpass.getpass("NPM two-factor code: ").strip()
        result = api.call("POST", "/tokens/2fa", {"challenge_token": result["challenge_token"], "code": code})
    api.token = result["token"]
    return credentials


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


def configure_access_list(api, username, password):
    access_lists = api.call("GET", "/nginx/access-lists")
    access = next((item for item in access_lists if item["name"] == "PaperGo users"), None)
    items = []
    if access is not None:
        access = api.call("GET", f"/nginx/access-lists/{access['id']}?expand=items")
        # NPM retains a user's current password when an update supplies an empty one.
        items = [{"username": item["username"], "password": ""}
                 for item in access["items"] if item["username"] != username]
    items.append({"username": username, "password": password})
    payload = {"name": "PaperGo users", "satisfy_any": False, "pass_auth": False, "items": items}
    if access is None:
        return api.call("POST", "/nginx/access-lists", {**payload, "clients": []})
    return api.call("PUT", f"/nginx/access-lists/{access['id']}", payload)


def wait_for_proxy(settings, credentials, certificate=None, timeout=60):
    local = settings["tls"] == "local"
    context = ssl.create_default_context(cafile=str(GENERATED / "ca.crt") if local else None)
    expected_certificate = ssl.PEM_cert_to_DER_cert(certificate.read_text()) if certificate else None
    login = credentials["papergo_user"] + ":" + credentials["papergo_password"]
    headers = {"Authorization": "Basic " + base64.b64encode(login.encode()).decode()}
    deadline = time.monotonic() + timeout
    reason = "proxy is not responding"
    while time.monotonic() < deadline:
        connection = http.client.HTTPSConnection(settings["host"], settings["https_port"],
                    timeout=min(3, max(0.1, deadline - time.monotonic())), context=context)
        try:
            if local:
                connection.sock = context.wrap_socket(
                    socket.create_connection(("127.0.0.1", settings["https_port"]), connection.timeout),
                    server_hostname=settings["host"],
                )
            else:
                connection.connect()
            if expected_certificate and connection.sock.getpeercert(binary_form=True) != expected_certificate:
                reason = "Nginx is still serving a different certificate"
            else:
                connection.request("GET", "/v1/identity/me", headers=headers)
                response = connection.getresponse()
                body = response.read()
                profile = json.loads(body) if response.status == 200 else None
                if isinstance(profile, dict) and profile.get("subject") == credentials["papergo_user"]:
                    return
                reason = f"authenticated profile is not ready (HTTP {response.status})"
        except ssl.SSLCertVerificationError:
            reason = "the served TLS certificate does not verify with the configured trust roots"
        except (OSError, http.client.HTTPException, ValueError):
            reason = "verified HTTPS connection or authenticated profile is not ready"
        finally:
            connection.close()
        time.sleep(0.25)
    raise RuntimeError(f"NPM did not become ready on verified HTTPS: {reason}. Setup is incomplete; inspect the proxy host and certificate in NPM.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", help="Public PaperGo hostname (required for first setup)")
    parser.add_argument("--email", help="NPM administrator email (required for first setup)")
    parser.add_argument("--tls", choices=["letsencrypt", "local"], default="letsencrypt")
    parser.add_argument("--admin-user", default="papergo-admin")
    parser.add_argument("--http-port", type=int, default=80)
    parser.add_argument("--https-port", type=int, default=443)
    parser.add_argument("--admin-port", type=int, default=8181)
    parser.add_argument("--no-build", action="store_true", help="Use the already built PaperGo image when reconfiguring this stack")
    parser.add_argument("--reset-npm-admin-password", action="store_true",
                        help="Reset the existing active NPM administrator's password to generated/credentials.json; preserve configuration and data")
    parser.add_argument("--subnet", default="172.29.242.0/24")
    settings_file = GENERATED / "deployment.json"
    if settings_file.exists():
        try:
            recorded = json.loads(settings_file.read_text())
            parser.set_defaults(**recorded)
        except (OSError, ValueError, TypeError):
            parser.error("generated/deployment.json is invalid")
    args = parser.parse_args()
    if not args.host or not args.email:
        parser.error("--host and --email are required for first setup")
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
    settings = {key: value for key, value in vars(args).items() if key not in ("no_build", "reset_npm_admin_password")}
    if settings_file.exists() and json.loads(settings_file.read_text()) != settings:
        parser.error("existing deployment settings differ; use the recorded settings for this stack")
    (GENERATED / "ready").unlink(missing_ok=True)
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
    start_stack(args.no_build)
    verify_egress()
    if args.reset_npm_admin_password:
        reset_management_password(credentials)
    api = API(f"http://127.0.0.1:{args.admin_port}/api")
    credentials = authenticate(api, credentials)
    private_write(credentials_file, json.dumps(credentials, indent=2) + "\n")
    access = configure_access_list(api, args.admin_user, credentials["papergo_password"])
    cert = None
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
    wait_for_proxy(settings, credentials, cert)
    private_write(GENERATED / "ready", public_url + "\n")
    print(f"PaperGo configured at {public_url}/v1/identity/me")
    print(f"NPM management UI: http://127.0.0.1:{args.admin_port}")
    print("Credentials are stored privately in generated/credentials.json; curl credentials in generated/curl.netrc.")
    if args.tls == "local":
        print("Use generated/ca.crt to verify the local TLS certificate; no TLS verification bypass is needed.")


if __name__ == "__main__":
    try:
        main()
    except urllib.error.HTTPError as error:
        path = urllib.parse.urlsplit(error.url).path
        raise SystemExit(f"NPM API request to {path} failed (HTTP {error.code}); inspect the management UI. Credentials were not printed.")
    except RuntimeError as error:
        raise SystemExit(str(error))
    except (EOFError, KeyboardInterrupt):
        raise SystemExit("Setup cancelled; no successful setup was recorded.")
