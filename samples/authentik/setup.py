#!/usr/bin/env python3
"""Provision PaperGo with authentik OIDC sign-in behind Caddy with HTTPS."""
import argparse
import base64
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import secrets
import socket
import ssl
import string
import subprocess
import time

ROOT = Path(__file__).resolve().parent
GENERATED = ROOT / "generated"
CLIENT_ID = "papergo"
SYSTEM_TRUST = "/etc/ssl/certs/ca-certificates.crt"
LOCAL_TRUST = "/data/.local-ca.pem"
HOST = re.compile(r"[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?")


def private_write(path, content):
    with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as f:
        f.write(content)
    os.chmod(path, 0o600)


def compose(*args, **kwargs):
    return subprocess.run(["docker", "compose", "--env-file", str(ROOT / ".env"),
                           "-f", str(ROOT / "compose.yaml"), *args], check=True, **kwargs)


def origin(host, port):
    return "https://" + host + ("" if port == 443 else f":{port}")


def issuer(settings):
    return origin(settings["auth_host"], settings["https_port"]) + f"/application/o/{CLIENT_ID}/"


def password_hash(password, iterations=1_000_000):
    """A Django PBKDF2-SHA256 hash, the format authentik stores; the password itself never enters a container."""
    salt = "".join(secrets.choice(string.ascii_letters + string.digits) for _ in range(22))
    digest = hashlib.pbkdf2_hmac("sha256", password.encode(), salt.encode(), iterations)
    return f"pbkdf2_sha256${iterations}${salt}${base64.b64encode(digest).decode()}"


def env_file(values):
    # Single quotes keep Compose from interpolating the $ separators of password hashes.
    for key, value in values.items():
        if "'" in value or "\n" in value:
            raise RuntimeError(f"{key} cannot be written to .env")
    return "".join(f"{key}='{value}'\n" for key, value in values.items())


def load_credentials(args):
    path = GENERATED / "credentials.json"
    if path.exists():
        return json.loads(path.read_text())
    authentik_password, papergo_password = secrets.token_urlsafe(32), secrets.token_urlsafe(32)
    credentials = {"authentik_user": "akadmin", "authentik_password": authentik_password,
                   "authentik_password_hash": password_hash(authentik_password),
                   "papergo_user": args.admin_user, "papergo_password": papergo_password,
                   "papergo_password_hash": password_hash(papergo_password)}
    private_write(path, json.dumps(credentials, indent=2) + "\n")
    return credentials


def ensure_secret(name, generate):
    path = GENERATED / name
    if not path.exists():
        private_write(path, generate())
    return path.read_text().strip()


def local_certificate(hosts):
    ca_key, ca_cert = GENERATED / "ca.key", GENERATED / "ca.crt"
    tls = GENERATED / "tls"
    key, csr, cert = tls / "server.key", GENERATED / "server.csr", tls / "server.crt"
    run = lambda *args: subprocess.run(["openssl", *args], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if not ca_cert.exists():
        run("req", "-x509", "-newkey", "rsa:3072", "-nodes", "-sha256", "-days", "3650", "-subj", "/CN=PaperGo local sample CA", "-addext", "basicConstraints=critical,CA:TRUE", "-addext", "keyUsage=critical,keyCertSign,cRLSign", "-keyout", str(ca_key), "-out", str(ca_cert))
    if not cert.exists():
        run("req", "-new", "-newkey", "rsa:3072", "-nodes", "-subj", "/CN=" + hosts[0], "-keyout", str(key), "-out", str(csr))
        extensions = GENERATED / "certificate.ext"
        names = ",".join("DNS:" + host for host in hosts)
        private_write(extensions, f"subjectAltName={names}\nbasicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectKeyIdentifier=hash\nauthorityKeyIdentifier=keyid,issuer\n")
        run("x509", "-req", "-in", str(csr), "-CA", str(ca_cert), "-CAkey", str(ca_key), "-CAcreateserial", "-out", str(cert), "-days", "365", "-sha256", "-extfile", str(extensions))
    return cert, ca_cert


def caddyfile(settings):
    local = settings["tls"] == "local"
    options = [f"\thttp_port {settings['http_port']}", f"\thttps_port {settings['https_port']}"]
    if not local:
        options.insert(0, f"\temail {settings['email']}")
    tls = "\ttls /etc/caddy/tls/server.crt /etc/caddy/tls/server.key\n" if local else ""
    hsts = '\theader Strict-Transport-Security "max-age=31536000"\n'
    # Caddy's automatic HTTP redirect targets port 443; other ports need an explicit one.
    redirect = "" if settings["https_port"] == 443 else (
        f"\nhttp://{settings['host']}, http://{settings['auth_host']} {{\n"
        f"\tredir https://{{host}}:{settings['https_port']}{{uri}} 308\n}}\n")
    return ("{\n" + "\n".join(options) + "\n}\n\n"
            f"{settings['host']} {{\n{tls}{hsts}"
            "\trequest_body {\n\t\tmax_size 80MB\n\t}\n"
            # Stream uploads, downloads and live events instead of buffering them.
            "\treverse_proxy papergo:8080 {\n\t\tflush_interval -1\n\t}\n}\n\n"
            f"{settings['auth_host']} {{\n{tls}{hsts}"
            "\treverse_proxy authentik-server:9000\n}\n" + redirect)


def https_get(settings, host, path, headers=None, certificate=None, timeout=3):
    """GET over verified HTTPS; local TLS connects to this machine and verifies the generated CA."""
    local = settings["tls"] == "local"
    context = ssl.create_default_context(cafile=str(GENERATED / "ca.crt") if local else None)
    connection = http.client.HTTPSConnection(host, settings["https_port"], timeout=timeout, context=context)
    try:
        if local:
            connection.sock = context.wrap_socket(
                socket.create_connection(("127.0.0.1", settings["https_port"]), timeout), server_hostname=host)
        else:
            connection.connect()
        if certificate and connection.sock.getpeercert(binary_form=True) != certificate:
            raise ssl.SSLError("the proxy is still serving a different certificate")
        connection.request("GET", path, headers=headers or {})
        response = connection.getresponse()
        return response.status, response.read()
    finally:
        connection.close()


def wait_until(description, check, timeout):
    deadline = time.monotonic() + timeout
    reason = "no response"
    while time.monotonic() < deadline:
        try:
            result = check()
            if result is True:
                return
            reason = result
        except ssl.SSLCertVerificationError:
            reason = "the served TLS certificate does not verify with the configured trust roots"
        except (OSError, http.client.HTTPException, ValueError) as error:
            reason = str(error) or type(error).__name__
        time.sleep(1)
    raise RuntimeError(f"{description} did not become ready: {reason}. Setup is incomplete; inspect `docker compose logs`.")


def apply_blueprint(settings, certificate):
    """Applies the PaperGo blueprint and waits until the issuer publishes its discovery document.

    On a fresh install the worker is still creating authentik's default flows, scope
    mappings and signing key, which the blueprint references, so failures are retried."""
    expected = issuer(settings)
    path = f"/application/o/{CLIENT_ID}/.well-known/openid-configuration"
    deadline = time.monotonic() + 600
    while True:
        result = subprocess.run(["docker", "compose", "--env-file", str(ROOT / ".env"), "-f", str(ROOT / "compose.yaml"),
                                 "exec", "-T", "authentik-worker", "ak", "apply_blueprint", "custom/papergo.yaml"],
                                capture_output=True, text=True)
        if result.returncode != 0:
            errors = [line for line in result.stderr.splitlines() if "Entry invalid" in line or "Blueprint invalid" in line]
            reason = "authentik rejected papergo-blueprint.yaml:\n" + "\n".join(errors or result.stderr.splitlines()[-5:])[-2000:]
        else:
            try:
                status, body = https_get(settings, settings["auth_host"], path, certificate=certificate)
                if status == 200 and json.loads(body).get("issuer") == expected:
                    return
                reason = f"discovery returned HTTP {status}"
            except ssl.SSLCertVerificationError:
                reason = "the served TLS certificate does not verify with the configured trust roots"
            except (OSError, http.client.HTTPException, ValueError) as error:
                reason = str(error) or type(error).__name__
        if time.monotonic() > deadline:
            raise RuntimeError(f"authentik did not publish the PaperGo issuer {expected}: {reason}\nInspect `docker compose logs authentik-worker`.")
        time.sleep(5)


def wait_for_papergo(settings, certificate):
    expected = {"mode": "bearer", "oidc": {"issuer": issuer(settings), "client_id": CLIENT_ID,
                                           "scope": "openid profile email offline_access"}}

    def ready():
        status, body = https_get(settings, settings["host"], "/auth/config", certificate=certificate)
        if status != 200 or json.loads(body) != expected:
            return f"/auth/config returned HTTP {status}"
        status, _ = https_get(settings, settings["host"], "/v1/identity/me")
        return True if status == 401 else f"unauthenticated API request returned HTTP {status}"
    wait_until("PaperGo on verified HTTPS", ready, 180)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", help="Public PaperGo hostname (required for first setup)")
    parser.add_argument("--auth-host", help="Public authentik hostname (required for first setup)")
    parser.add_argument("--email", help="Email of the PaperGo administrator, also used for Let's Encrypt (required for first setup)")
    parser.add_argument("--tls", choices=["letsencrypt", "local"], default="letsencrypt")
    parser.add_argument("--admin-user", default="papergo-admin")
    parser.add_argument("--http-port", type=int, default=80)
    parser.add_argument("--https-port", type=int, default=443)
    parser.add_argument("--no-build", action="store_true", help="Use the already built PaperGo image when reconfiguring this stack")
    settings_file = GENERATED / "deployment.json"
    if settings_file.exists():
        try:
            parser.set_defaults(**json.loads(settings_file.read_text()))
        except (OSError, ValueError, TypeError):
            parser.error("generated/deployment.json is invalid")
    args = parser.parse_args()
    if not args.host or not args.auth_host or not args.email:
        parser.error("--host, --auth-host and --email are required for first setup")
    for host in (args.host, args.auth_host):
        if not HOST.fullmatch(host) or ".." in host:
            parser.error("hosts must be DNS hostnames without a scheme, port or path")
    if args.host == args.auth_host:
        parser.error("PaperGo and authentik need separate hostnames")
    if not re.fullmatch(r"[a-zA-Z0-9._@-]{1,150}", args.admin_user) or args.admin_user == "akadmin":
        parser.error("admin username must use letters, digits, dots, underscores, @ or hyphens and differ from akadmin")
    if not re.fullmatch(r"[^\s@'\"]+@[^\s@'\"]+", args.email):
        parser.error("email must be an email address")
    for port in [args.http_port, args.https_port]:
        if not 1 <= port <= 65535:
            parser.error("ports must be between 1 and 65535")
    if args.http_port == args.https_port:
        parser.error("HTTP and HTTPS ports must differ")
    if args.tls == "letsencrypt" and (args.http_port != 80 or any(h.endswith(".localhost") for h in (args.host, args.auth_host))):
        parser.error("Let's Encrypt requires public DNS hostnames and HTTP port 80")
    os.umask(0o077)
    GENERATED.mkdir(mode=0o700, exist_ok=True)
    settings = {key: value for key, value in vars(args).items() if key != "no_build"}
    if settings_file.exists() and json.loads(settings_file.read_text()) != settings:
        parser.error("existing deployment settings differ; use the recorded settings for this stack")
    (GENERATED / "ready").unlink(missing_ok=True)
    private_write(settings_file, json.dumps(settings, indent=2) + "\n")

    credentials = load_credentials(args)
    ensure_secret("postgres-password", lambda: secrets.token_urlsafe(36))
    ensure_secret("authentik-secret-key", lambda: secrets.token_urlsafe(60))
    certificate = None
    (GENERATED / "tls").mkdir(mode=0o700, exist_ok=True)
    if args.tls == "local":
        cert, ca = local_certificate([args.host, args.auth_host])
        certificate = ssl.PEM_cert_to_DER_cert(cert.read_text())
        private_write(GENERATED / "local-ca.pem", ca.read_text())
    else:
        # Caddy obtains public certificates itself; these files stay empty.
        for name in ("local-ca.pem", "tls/server.crt", "tls/server.key"):
            private_write(GENERATED / name, "")
    private_write(GENERATED / "Caddyfile", caddyfile(settings))
    public_url = origin(args.host, args.https_port)
    private_write(ROOT / ".env", env_file({
        "PUBLIC_URL": public_url, "PAPERGO_HOST": args.host, "AUTH_HOST": args.auth_host,
        "OIDC_ISSUER": issuer(settings), "PAPERGO_ADMIN": args.admin_user,
        "PAPERGO_TRUST_FILE": LOCAL_TRUST if args.tls == "local" else SYSTEM_TRUST,
        "HTTP_PORT": str(args.http_port), "HTTPS_PORT": str(args.https_port),
        "PAPERGO_ADMIN_EMAIL": args.email, "IDP_ADMIN_PASSWORD_HASH": credentials["authentik_password_hash"],
        "PAPERGO_ADMIN_PASSWORD_HASH": credentials["papergo_password_hash"]}))

    # The issuer must exist before PaperGo starts: PaperGo loads its discovery document at startup.
    compose("up", "-d", "--wait", "--wait-timeout", "600", "caddy", "authentik-server", "authentik-worker")
    # Caddy reads its configuration at startup; reload picks up a changed Caddyfile on reruns.
    compose("exec", "-T", "caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile", stdout=subprocess.DEVNULL)
    apply_blueprint(settings, certificate)
    compose("up", *([] if args.no_build else ["--build"]), "-d", "--wait", "--wait-timeout", "300")
    wait_for_papergo(settings, certificate)
    private_write(GENERATED / "ready", public_url + "\n")
    print(f"PaperGo: {public_url} (sign in as {args.admin_user})")
    print(f"authentik: {origin(args.auth_host, args.https_port)}/if/admin/ (sign in as akadmin)")
    print("Passwords are stored privately in generated/credentials.json.")
    if args.tls == "local":
        print("Trust generated/ca.crt to verify the local TLS certificate; no TLS verification bypass is needed.")


if __name__ == "__main__":
    try:
        main()
    except subprocess.CalledProcessError as error:
        raise SystemExit(f"Command failed with exit status {error.returncode}: {' '.join(error.cmd[:6])} …; setup is incomplete.")
    except RuntimeError as error:
        raise SystemExit(str(error))
    except KeyboardInterrupt:
        raise SystemExit("Setup cancelled; no successful setup was recorded.")
