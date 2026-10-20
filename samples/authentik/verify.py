#!/usr/bin/env python3
"""Exercise the running sample: authentik sign-in, PaperGo REST and WebDAV over verified HTTPS."""
import argparse
import base64
import hashlib
import http.client
import http.cookiejar
import json
from pathlib import Path
import re
import secrets
import socket
import ssl
import subprocess
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parent
GENERATED = ROOT / "generated"
CLIENT_ID = "papergo"
SCOPE = "openid profile email offline_access"


def load_deployment():
    try:
        settings = json.loads((GENERATED / "deployment.json").read_text())
        port = "" if settings["https_port"] == 443 else f":{settings['https_port']}"
        app, idp = "https://" + settings["host"] + port, "https://" + settings["auth_host"] + port
        if (GENERATED / "ready").read_text().strip() != app:
            raise RuntimeError("Setup completion does not match the recorded public origin; rerun setup.py.")
        credentials = json.loads((GENERATED / "credentials.json").read_text())
        context = ssl.create_default_context(cafile=str(GENERATED / "ca.crt") if settings["tls"] == "local" else None)
    except (OSError, ValueError, KeyError):
        raise RuntimeError("Sample setup is incomplete or its generated files are invalid. Resolve the setup.py error and rerun it successfully before running verify.py.") from None
    return settings, credentials, app, idp, context


def b64url(data):
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode()


def claims(jwt):
    payload = jwt.split(".")[1]
    return json.loads(base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4)))


class Client:
    """HTTPS with verified certificates. Local TLS connects to this machine under the public hostnames."""

    def __init__(self, settings, context):
        self.settings, self.context = settings, context
        self.jar = http.cookiejar.CookieJar()

    def connection(self, host):
        port = self.settings["https_port"]
        connection = http.client.HTTPSConnection(host, port, timeout=60, context=self.context)
        if self.settings["tls"] == "local":
            connection.sock = self.context.wrap_socket(socket.create_connection(("127.0.0.1", port), 30), server_hostname=host)
        return connection

    def request(self, method, url, body=None, *, headers=None, expected=None, chunked=False, cookies=False):
        parts = urllib.parse.urlsplit(url)
        fields = dict(headers or {})
        if isinstance(body, dict) and fields.get("Content-Type") != "application/x-www-form-urlencoded":
            body = json.dumps(body).encode()
            fields["Content-Type"] = "application/json"
        elif isinstance(body, dict):
            body = urllib.parse.urlencode(body).encode()
        if cookies:
            request = urllib.request.Request(url, method=method)
            self.jar.add_cookie_header(request)
            fields.update(request.header_items())
            csrf = next((cookie.value for cookie in self.jar if cookie.name == "authentik_csrf"), None)
            if csrf:
                fields["X-authentik-CSRF"] = csrf
        connection = self.connection(parts.hostname)
        target = parts.path + ("?" + parts.query if parts.query else "")
        connection.request(method, target or "/", body=body, headers=fields, encode_chunked=chunked)
        response = connection.getresponse()
        data = response.read()
        connection.close()
        if cookies:
            self.jar.extract_cookies(response, urllib.request.Request(url))
        if expected is not None and response.status != expected:
            raise AssertionError(f"{method} {parts.path}: expected {expected}, got {response.status}")
        payload = json.loads(data) if data and data.lstrip().startswith((b"{", b"[")) else data
        return response.status, response.headers, payload


class Identity:
    """Signs in at authentik as a browser would: authorization code flow with PKCE."""

    def __init__(self, client, app, idp):
        self.client, self.app, self.idp = client, app, idp
        self.redirect_uri = app + "/login/callback"

    def flow(self, location, answers):
        """Runs an authentik flow through its executor API and returns where the flow redirects."""
        parts = urllib.parse.urlsplit(urllib.parse.urljoin(self.idp, location))
        slug = parts.path.strip("/").split("/")[-1]
        executor = f"{self.idp}/api/v3/flows/executor/{slug}/?" + urllib.parse.urlencode({"query": parts.query})
        challenge = None
        for _ in range(20):
            if challenge is None:
                url = executor
                for _ in range(5):
                    status, headers, challenge = self.client.request("GET", url, cookies=True)
                    if status != 302:
                        break
                    url = urllib.parse.urljoin(self.idp, headers["Location"])
            component = challenge.get("component") if isinstance(challenge, dict) else None
            if component == "xak-flow-redirect":
                return challenge["to"]
            if component not in answers:
                return component
            status, _, response = self.client.request("POST", executor, answers[component], cookies=True)
            challenge = response if status == 200 else None
        raise AssertionError("authentik flow did not finish")

    def authorize(self, username, password, redirect_uri=None):
        verifier = secrets.token_urlsafe(48)
        state = secrets.token_urlsafe(16)
        query = urllib.parse.urlencode({
            "client_id": CLIENT_ID, "response_type": "code", "redirect_uri": redirect_uri or self.redirect_uri,
            "scope": SCOPE, "state": state, "code_challenge": b64url(hashlib.sha256(verifier.encode()).digest()),
            "code_challenge_method": "S256"})
        answers = {"ak-stage-identification": {"component": "ak-stage-identification", "uid_field": username},
                   "ak-stage-password": {"component": "ak-stage-password", "password": password}}
        location = f"{self.idp}/application/o/authorize/?{query}"
        for _ in range(5):
            status, headers, body = self.client.request("GET", urllib.parse.urljoin(self.idp, location), cookies=True)
            if status == 200 and b"Request has been denied." in body:
                return None, "access denied"
            if status != 302:
                return None, f"authorize returned HTTP {status}"
            location = headers["Location"]
            if location.startswith(self.redirect_uri + "?"):
                result = dict(urllib.parse.parse_qsl(urllib.parse.urlsplit(location).query))
                assert result.get("state") == state, "authorization response state mismatch"
                return self.redeem(result["code"], verifier), None
            if "/if/flow/" in location:
                outcome = self.flow(location, answers)
                if not outcome.startswith(("/", self.idp, self.redirect_uri)):
                    return None, outcome
                location = outcome
        return None, "authorization did not redirect back to PaperGo"

    def token(self, form):
        _, headers, tokens = self.client.request(
            "POST", self.idp + "/application/o/token/", {"client_id": CLIENT_ID, **form},
            headers={"Content-Type": "application/x-www-form-urlencoded", "Origin": self.app}, expected=200)
        # The browser calls the token endpoint from PaperGo's origin.
        assert headers.get("Access-Control-Allow-Origin") == self.app, "token endpoint does not allow PaperGo's origin (CORS)"
        return tokens

    def redeem(self, code, verifier):
        return self.token({"grant_type": "authorization_code", "code": code, "code_verifier": verifier,
                           "redirect_uri": self.redirect_uri})


def compose(*args, **kwargs):
    return subprocess.run(["docker", "compose", "--env-file", str(ROOT / ".env"), "-f", str(ROOT / "compose.yaml"), *args],
                          check=True, **kwargs)


def authentik_shell(code, data):
    """Runs Python in the authentik worker with input on stdin; used only for a temporary test account."""
    compose("exec", "-T", "authentik-worker", "ak", "shell", "-c", code, input=json.dumps(data), text=True, capture_output=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--large-upload", action="store_true", help="Also verify a 64 MiB upload and download at the sample's configured limit")
    args = parser.parse_args()
    settings, credentials, app, idp, context = load_deployment()
    client = Client(settings, context)
    get = lambda url, **kw: client.request("GET", url, **kw)[2]

    for host in (settings["host"], settings["auth_host"]):
        plain = http.client.HTTPConnection("127.0.0.1" if settings["tls"] == "local" else host, settings["http_port"], timeout=30)
        plain.request("GET", "/v1/identity/me", headers={"Host": host})
        redirect = plain.getresponse()
        port = "" if settings["https_port"] == 443 else f":{settings['https_port']}"
        assert redirect.status in (301, 308) and redirect.getheader("Location") == f"https://{host}{port}/v1/identity/me", "HTTP does not redirect to HTTPS"
        plain.close()

    issuer = f"{idp}/application/o/{CLIENT_ID}/"
    discovery = get(issuer + ".well-known/openid-configuration", expected=200)
    assert discovery["issuer"] == issuer and "S256" in discovery["code_challenge_methods_supported"]
    assert get(discovery["jwks_uri"], expected=200)["keys"], "issuer publishes no signing keys"
    assert get(app + "/auth/config", expected=200) == {"mode": "bearer", "oidc": {"issuer": issuer, "client_id": CLIENT_ID, "scope": SCOPE}}
    index = get(app + "/", expected=200)
    assert b'<div id="root">' in index and get(app + "/login/callback", expected=200) == index
    assets = re.findall(rb'(?:src|href)="(/assets/[^" ]+)"', index)
    assert assets, "frontend entry assets are missing"
    for asset in assets:
        assert get(app + asset.decode(), expected=200), "frontend asset is empty"
    get(app + "/v1/identity/me", expected=401)
    get(app + "/v1/identity/me", headers={"Authorization": "Bearer not-a-token"}, expected=401)

    identity = Identity(client, app, idp)
    tokens, error = identity.authorize(credentials["papergo_user"], credentials["papergo_password"])
    assert tokens, f"administrator sign-in failed: {error}"
    access = claims(tokens["access_token"])
    assert access["iss"] == issuer and access["aud"] == CLIENT_ID and "papergo-admins" in access["groups"], access
    bearer = {"Authorization": "Bearer " + tokens["access_token"]}
    me = get(app + "/v1/identity/me", headers=bearer, expected=200)
    assert me["subject"] == access["sub"] and "workspace.manage" in me["scopes"], "IdP administrator did not receive PaperGo's Administrator role"
    assert {"admins", "users"} <= set(me.get("groups") or []), f"groups were not synchronized: {me.get('groups')}"

    # A token from another key is rejected even with the right claims.
    header = b64url(json.dumps({"alg": "HS256", "typ": "JWT"}).encode())
    forged = f"{header}.{tokens['access_token'].split('.')[1]}.{b64url(secrets.token_bytes(32))}"
    get(app + "/v1/identity/me", headers={"Authorization": "Bearer " + forged}, expected=401)

    # The browser renews access with the refresh token.
    renewed = identity.token({"grant_type": "refresh_token", "refresh_token": tokens["refresh_token"]})
    assert get(app + "/v1/identity/me", headers={"Authorization": "Bearer " + renewed["access_token"]}, expected=200)["subject"] == me["subject"]

    # Redirects only go to the registered PaperGo callback.
    _, error = Identity(Client(settings, context), app, idp).authorize(credentials["papergo_user"], credentials["papergo_password"], "https://attacker.example/callback")
    assert error, "authentik redirected to an unregistered URI"

    # Only papergo-users members may sign in to PaperGo.
    outsider, outsider_password = "papergo-verify-" + secrets.token_hex(4), secrets.token_urlsafe(24)
    create = ("import json,sys\nfrom authentik.core.models import User\nd=json.load(sys.stdin)\n"
              "u=User.objects.create(username=d['username'],name='PaperGo verification')\nu.set_password(d['password'])\nu.save()")
    delete = "import json,sys\nfrom authentik.core.models import User\nUser.objects.filter(username=json.load(sys.stdin)['username']).delete()"
    authentik_shell(create, {"username": outsider, "password": outsider_password})
    try:
        denied, error = Identity(Client(settings, context), app, idp).authorize(outsider, outsider_password)
        assert denied is None and error == "access denied", f"a user outside papergo-users was not denied: {error}"
    finally:
        authentik_shell(delete, {"username": outsider})

    # Personal access tokens for scripts.
    created = client.request("POST", app + "/v1/identity/me/tokens", {"name": "verify " + secrets.token_hex(4), "scopes": ["workspace.read"], "expires_in_days": 1}, headers=bearer, expected=201)[2]
    personal = {"Authorization": "Bearer " + created["secret"]}
    assert get(app + "/v1/identity/me", headers=personal, expected=200)["subject"] == me["subject"]
    client.request("DELETE", app + f"/v1/identity/me/tokens/{created['token']['id']}", headers=bearer, expected=204)
    get(app + "/v1/identity/me", headers=personal, expected=401)

    # REST writes and WebDAV with an app password.
    workspace = client.request("POST", app + "/v1/workspaces", {"name": "Authentik verification " + secrets.token_hex(4)}, headers=bearer, expected=201)[2]
    library = client.request("POST", app + f"/v1/resources/{workspace['id']}/children", {"kind": "library", "name": "Documents", "webdav_enabled": True}, headers=bearer, expected=201)[2]
    credential = client.request("POST", app + "/v1/webdav-credentials", {"label": "verify"}, headers=bearer, expected=201)[2]
    dav = {"Authorization": "Basic " + base64.b64encode(f"{credentials['papergo_user']}:{credential['password']}".encode()).decode()}
    folder = app + f"/webdav/{library['id']}/" + urllib.parse.quote("Sample Ω")
    client.request("MKCOL", folder, headers=dav, expected=201)
    content = b"%PDF-1.7\nPaperGo authentik verification\n%%EOF\n"
    client.request("PUT", folder + "/example.pdf", content, headers={**dav, "Content-Type": "application/pdf"}, expected=201)
    assert get(folder + "/example.pdf", headers=dav, expected=200) == content
    client.request("MOVE", folder + "/example.pdf", headers={**dav, "Destination": folder + "/moved.pdf"}, expected=201)
    client.request("PUT", folder + "/chunked.pdf", iter([content[:9], content[9:]]), headers={**dav, "Content-Type": "application/pdf"}, expected=201, chunked=True)
    client.request("COPY", folder + "/chunked.pdf", headers={**dav, "Destination": folder + "/copied.pdf"}, expected=201)
    assert get(folder + "/copied.pdf", headers=dav, expected=200) == content
    assert get(folder + "/moved.pdf", headers=bearer, expected=200) == content
    if args.large_upload:
        large = content + b"\0" * (64 * 1024 * 1024 - len(content))
        client.request("PUT", folder + "/large.pdf", large, headers={**dav, "Content-Type": "application/pdf"}, expected=201)
        assert get(folder + "/large.pdf", headers=dav, expected=200) == large
        print("Verified a 64 MiB upload and download at MAX_UPLOAD_BYTES.")

    # Sign-out ends the authentik session and returns to PaperGo's sign-in page.
    end_session = discovery["end_session_endpoint"] + "?" + urllib.parse.urlencode({
        "id_token_hint": tokens["id_token"], "post_logout_redirect_uri": app + "/login", "client_id": CLIENT_ID})
    status, headers, _ = client.request("GET", end_session, cookies=True)
    assert status == 302, f"end-session returned HTTP {status}"
    location = headers["Location"]
    if "/if/flow/" in location:
        location = identity.flow(location, {})
    assert location == app + "/login", f"sign-out returned to {location}"
    status, headers, _ = client.request("GET", f"{idp}/application/o/authorize/?client_id={CLIENT_ID}&response_type=code&scope=openid&redirect_uri={urllib.parse.quote(identity.redirect_uri)}", cookies=True)
    assert status == 302 and "/if/flow/default-authentication-flow/" in headers["Location"], "authentik session survived sign-out"

    for service in ("papergo", "authentik-server", "authentik-worker", "postgresql"):
        cid = compose("ps", "-q", service, capture_output=True, text=True).stdout.strip()
        ports = json.loads(subprocess.check_output(["docker", "inspect", "--format", "{{json .NetworkSettings.Ports}}", cid], text=True))
        assert not any(ports.values()), f"{service} is published on the host"
    print("Verified HTTP-to-HTTPS redirects, verified HTTPS, issuer discovery, embedded UI and deep links, browser sign-in settings, "
          "authorization code + PKCE sign-in, token audience and groups, administrator group mapping, forged-token rejection, "
          "refresh tokens with CORS, redirect URI pinning, papergo-users access policy, personal access tokens, REST writes, "
          "Unicode WebDAV PUT/GET/MOVE/COPY with chunked uploads, provider sign-out, and private backends.")
    print(f"Test workspace: {workspace['id']}; WebDAV library: {library['id']}")


if __name__ == "__main__":
    try:
        main()
    except ssl.SSLCertVerificationError:
        raise SystemExit("The served TLS certificate does not verify with the configured trust roots. Rerun setup.py --no-build and wait for verified HTTPS. TLS verification remains enabled.")
    except RuntimeError as error:
        raise SystemExit(str(error))
