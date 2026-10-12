# Nginx Proxy Manager + PaperGo

This stack uses **NPM Access List usernames and passwords** to authenticate both REST and WebDAV. PaperGo runs with `APP_ENV=production` and `AUTH_MODE=proxy`. There is no OIDC provider in the stack. NPM administrators control the trusted identity boundary. NPM management accounts control the proxy; they are separate from application logins.

Requirements: Docker with Compose v2, Python 3, and OpenSSL for the local certificate option. Run each deployment from its own repository checkout. The setup script stores deployment-specific state under `generated/` and refuses conflicting settings for an existing stack.

## Run locally

From this directory:

```sh
python3 setup.py --host papergo.localhost --email admin@papergo.example \
  --tls local --http-port 18080 --https-port 18443 --admin-port 18181
python3 verify.py
```

The local option generates a private CA and an HTTPS certificate for the hostname. Verification uses that CA and checks the hostname; TLS verification remains enabled. For custom HTTPS ports, setup installs an explicit redirect to the configured public origin in NPM's Advanced field. Port-443 deployments use NPM's Force SSL setting.

NPM is available at `http://127.0.0.1:18181`. Its administrator email and randomly generated password are in `generated/credentials.json`, readable only by the owner. The same file contains the separate `papergo-admin` application password. The script does not print passwords or pass them through container environment variables.

To read the authenticated profile:

```sh
curl --noproxy '*' --resolve papergo.localhost:18443:127.0.0.1 \
  --cacert generated/ca.crt --netrc-file generated/curl.netrc \
  https://papergo.localhost:18443/v1/identity/me
```

Open the public origin in a browser and sign in with the NPM application username and password at the browser’s HTTP authentication prompt. The embedded frontend automatically loads the verified PaperGo profile without asking for a bearer token. `/auth/config` reports proxy mode; the protected `/` location also covers this endpoint, frontend assets and every API route. WebDAV uses the same NPM credentials. `verify.py` checks the embedded app, deep links, protected frontend assets and proxy auth configuration, then creates a test workspace and library, exercises REST and Unicode WebDAV paths with chunked uploads, COPY and MOVE, and rejects unauthenticated requests and forged identity headers. Run `python3 verify.py --large-upload` to also check a 64 MiB upload and download at the configured limit.

## Deploy with a public certificate

In a fresh checkout on the deployment server, point a real DNS hostname at the server and make ports 80 and 443 reachable:

```sh
python3 setup.py --host docs.example.com --email owner@example.com
python3 verify.py
```

The default certificate option is Let's Encrypt HTTP-01. NPM requests and renews the certificate using its normal certificate management. Running this command registers the certificate account using the supplied email and accepts the Let's Encrypt subscriber agreement through NPM. Local test certificates are not used for this deployment.

The NPM management port is bound to loopback. Reach it through an SSH tunnel when administering a remote server:

```sh
ssh -L 8181:127.0.0.1:8181 user@server
```

Open `http://127.0.0.1:8181`. Public ports expose NPM only; PaperGo has no host port mapping. A dedicated internal network assigns NPM a fixed backend address, and PaperGo trusts only that address with the shared secret. Use `--subnet` to select a different private IPv4 /24–/28 network if the default conflicts with another network.

## Users, roles and profiles

In NPM, edit the **PaperGo users** Access List to add application users. Keep **Satisfy Any** and **Pass Auth to Host** disabled. Preserve the generated TLS redirect, protected `/` location and forwarding headers. New usernames enroll with PaperGo's Member role on their first request. `ADMIN_SUBJECT` names the first application administrator; the initial username is `papergo-admin`.

Use the frontend’s Administration screens or `/v1/identity` to manage application profiles, groups and role assignments. NPM does not supply group or role claims. Usernames are stable application subjects: choosing another username creates another identity and does not transfer permissions. Keep an active application administrator in the Access List when editing credentials.

Removing NPM credentials blocks new requests. Disabling the PaperGo user through its status API also revokes ongoing application access. The frontend hides personal REST tokens and displays proxy WebDAV instructions rather than app-password controls. Browser HTTP credentials are managed by the proxy and browser; PaperGo does not provide a proxy logout endpoint. Personal REST-token and WebDAV app-password endpoints are unavailable in proxy mode; automations should have their own NPM username and appropriately limited PaperGo roles.

PaperGo validates the actual forwarding peer, secret and username. It rejects duplicate assertion headers, forwarded `Authorization`, client attempts to override the assertion in batches, and cross-origin browser mutations. NPM overwrites incoming assertion headers with the authenticated username and configured secret.

## Storage and operations

Named volumes preserve PaperGo data, NPM configuration and certificates. A network-isolated permissions job installs the private bridge secret, and a one-shot `papergo-admin initialize` job applies the single current Atlas schema with PaperGo's SQLite driver, including FTS5. The initializer validates an existing database's schema and checksum without changing user data. Schema changes require a fresh disposable database; there is no upgrade or repair path.

The PaperGo container runs as UID 65532, with a read-only root filesystem, dropped capabilities, an internal network, bounded resources and rotated container logs. Readiness checks use the image's `papergo-admin health` command. NPM uses its supported image layout and persistent directories.

```sh
docker compose --env-file .env logs --tail 100 papergo npm
docker compose --env-file .env restart papergo
docker compose --env-file .env down
```

`down` preserves named volumes. `down --volumes` permanently deletes them and is only appropriate when intentionally discarding a deployment. Keep `generated/` private and back it up securely together with the NPM volumes. Use the image's `papergo-admin backup` for a consistent PaperGo database and blob archive while the API serves reads:

```sh
backup_name="papergo-$(date -u +%Y%m%dT%H%M%SZ).tar.gz"
docker compose --env-file .env exec -T papergo /papergo-admin backup --file "/data/$backup_name"
docker compose --env-file .env cp "papergo:/data/$backup_name" "generated/$backup_name"
```

Keep copied archives on separate backup storage and manage retention of the archives in `/data`. Stop PaperGo before restoring so the exclusive data lease can be acquired. The restore command validates an archive before replacing data; see [administrative backup and restore](../../README.md#administrative-backup-and-restore).

The setup script can be rerun with the recorded settings. Credentials and bridge secrets remain stable. Use `--no-build` to reconfigure an already built stack. After manually rotating the bridge secret, rerun setup to update NPM and install the secret, then restart PaperGo to load it. Application and NPM management passwords are changed separately.

The public hostname, certificate issuance and server firewall are deployment inputs. The local stack verifies the authentication and storage path without requiring public DNS.

Sources: [NPM release](https://github.com/NginxProxyManager/nginx-proxy-manager/releases/tag/v2.16.0), [NPM access-list authentication](https://nginxproxymanager.com/faq/), and [PaperGo proxy contract](../../docs/reverse-proxies.md).
