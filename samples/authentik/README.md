# authentik + Caddy + PaperGo

This stack signs users in with **authentik**, a self-hosted identity provider, over OIDC. PaperGo runs with `APP_ENV=production` and `AUTH_MODE=oidc`; **Caddy** terminates HTTPS for two hostnames, one for PaperGo and one for authentik. Users sign in from the PaperGo web app with the authorization code flow and PKCE, manage their password, MFA and passkeys in authentik, and get PaperGo groups from their authentik groups.

Why authentik: one admin UI for users, groups, invitations, MFA (TOTP, WebAuthn/passkeys), password recovery and social or LDAP sources; declarative configuration through blueprints; JWT access tokens signed with an asymmetric key; and refresh tokens for browser clients. It needs PostgreSQL and about 1.5 GB of memory. For a single-container setup without an identity provider, use the [NPM sample](../npm/README.md) instead.

| Service | Role | Exposure |
| --- | --- | --- |
| `caddy` | HTTPS for both hostnames, certificates, HTTP redirects | Ports 80 and 443 (TCP and UDP) |
| `papergo` | API, web app and WebDAV | Internal network only |
| `authentik-server` | Sign-in pages, OIDC endpoints, admin UI | Internal network, reached through Caddy |
| `authentik-worker` | Background tasks, blueprints, email | Internal network with outbound access |
| `postgresql` | authentik's database | authentik's internal network only |

Requirements: Docker with Compose v2, Python 3, and OpenSSL for the local certificate option. Run each deployment from its own repository checkout. The setup script stores deployment-specific state under `generated/` and refuses conflicting settings for an existing stack.

## Run locally

From this directory:

```sh
python3 setup.py --host papergo.localhost --auth-host auth.papergo.localhost \
  --email admin@papergo.example --tls local --http-port 18080 --https-port 18443
python3 verify.py
```

The first start takes a few minutes while authentik migrates its database. The local option generates a private CA and one certificate for both hostnames; Caddy serves it and PaperGo trusts the CA when it loads the issuer's discovery document and keys. Verification uses the same CA and checks hostnames; TLS verification remains enabled.

Open `https://papergo.localhost:18443` in a browser (browsers resolve `*.localhost` to this machine; import `generated/ca.crt` as a trusted authority or accept the warning), choose **Sign in with single sign-on**, and sign in as `papergo-admin` with the `papergo_password` from `generated/credentials.json`. The authentik admin UI is at `https://auth.papergo.localhost:18443/if/admin/` as `akadmin` with `authentik_password`.

`verify.py` checks the HTTP-to-HTTPS redirects, issuer discovery, the embedded app and its browser sign-in settings, then signs in through authentik's flows with the authorization code flow and PKCE, as the browser does. It checks the token's issuer, audience and groups, the administrator group mapping, rejection of unauthenticated and forged tokens, refresh tokens and CORS for the browser, that redirects only go to the registered PaperGo callback, and that a user outside `papergo-users` is denied. It then creates a personal access token, a workspace and a WebDAV library, exercises Unicode WebDAV PUT/GET/MOVE/COPY with chunked uploads, signs out through authentik and checks that the authentik session ended, and confirms that only Caddy publishes ports. Run `python3 verify.py --large-upload` to also check a 64 MiB upload and download at the configured limit.

## Deploy with public certificates

Point two DNS names at the server, for example `docs.example.com` and `auth.example.com`, and make ports 80 and 443 reachable. In a fresh checkout on the server:

```sh
python3 setup.py --host docs.example.com --auth-host auth.example.com --email owner@example.com
python3 verify.py
```

Caddy obtains and renews Let's Encrypt certificates for both names, using the email for the ACME account. PaperGo reaches the issuer through Caddy on the internal network under its public name, so no hairpin NAT is needed.

The email also becomes `papergo-admin`'s address. `akadmin` keeps authentik's placeholder address `root@example.com`; give it a different real address in the admin UI if you want its recovery emails. Two accounts must not share an address, because people can sign in with their email.

## Users and groups

Manage people in authentik (**Directory → Users** and **Groups**):

- **Access.** Only members of `papergo-users` can sign in to PaperGo; authentik shows "Permission denied" to anyone else. Add new users to this group.
- **Groups.** Groups named `papergo-<name>` appear in PaperGo as `<name>` (`OIDC_GROUPS_PREFIX=papergo-`) and are synchronized on every sign-in. Use them for PaperGo roles and permissions. Other authentik groups are ignored.
- **Administrators.** Members of `papergo-admins` receive PaperGo's Administrator role (`ADMIN_GROUP=admins`). Setup creates `papergo-admin` in `papergo-users` and `papergo-admins`.
- **New users** enroll in PaperGo with the Member role on their first sign-in. Their PaperGo subject is authentik's hashed user ID, so renaming a user or changing their email keeps their PaperGo identity and permissions.
- **Leaving.** Deactivate the user in authentik to block new sign-ins. Access tokens already issued stay valid for at most 10 minutes; deactivate the user in PaperGo (**Administration → Users**) to end access at once.

Users manage their password, TOTP and passkeys in authentik's user interface. To require MFA, edit the `default-authentication-mfa-validation` stage and set **Not configured action** to **Force the user to configure an authenticator**. Password recovery and invitations need email: add the `AUTHENTIK_EMAIL__*` settings (host, port, username, password, TLS, from) to both authentik services in a `compose.override.yaml`; the worker sends mail through the `idp-egress` network.

Scripts and integrations use **personal access tokens** (PaperGo **Settings → access tokens**, or `POST /v1/identity/me/tokens`) and WebDAV clients use those tokens as their Basic password with library and document scopes. Tokens are capped by the user's current PaperGo roles. They keep working while the user stays active in PaperGo.

## Sign-in configuration

`papergo-blueprint.yaml` declares the PaperGo side of authentik: the two groups, the first administrator, the OAuth2 provider and the application with its access policy. The worker applies it at startup and whenever the file changes; setup applies it explicitly and waits for the issuer. The provider is a **public client** (`papergo`) for the authorization code flow with PKCE and refresh tokens only, with a strict redirect URI `<PUBLIC_URL>/login/callback` and a sign-out redirect to `<PUBLIC_URL>/login`. Access tokens are RS256 JWTs signed with authentik's self-signed key, valid for 10 minutes, with `aud` set to the client ID; refresh tokens are valid for 30 days. Signing out of PaperGo ends the authentik session as well.

PaperGo reads those settings from the mounted `generated/config.yaml`: `auth.mode` set to `oidc`, `oidc.issuer` at `https://<auth host>/application/o/papergo/`, `oidc.audience` set to `papergo`, `oidc.client_id` set to `papergo`, `oidc.groups_claim` set to `groups`, `oidc.groups_prefix` set to `papergo-`, and `auth.admin_group` set to `admins`. See the [identity provider guide](../../docs/reverse-proxies.md) for what each one does.

The blueprint creates the administrator once: later changes in authentik (password, groups, profile) are kept. Changes to the provider in the admin UI are overwritten the next time the blueprint is applied; edit the blueprint instead and rerun setup.

## Storage and operations

Named volumes hold PaperGo's data, authentik's database and media, and Caddy's certificates. A network-isolated job installs secrets, the Caddy configuration, the blueprint and the local CA into volumes owned by each service's user on every `up`. Setup writes `generated/config.yaml` and Compose mounts it into the PaperGo container. PaperGo applies its schema when the application starts. No password is passed to a container: authentik receives only PBKDF2 hashes of the two generated administrator passwords, and its secret key and database password are read from files.

PaperGo and Caddy run with read-only root filesystems; all services drop Linux capabilities (Caddy keeps only binding ports 80 and 443), cannot gain privileges, and have bounded memory, processes and rotated logs. PaperGo has no outbound network access. PaperGo loads the issuer's discovery document when it starts; if authentik is not ready yet, for example after a reboot, PaperGo exits and Docker restarts it until it is.

```sh
docker compose --env-file .env ps
docker compose --env-file .env logs --tail 100 papergo authentik-server caddy
docker compose --env-file .env restart papergo
docker compose --env-file .env down
```

`down` preserves named volumes. `down --volumes` permanently deletes them and is only appropriate when intentionally discarding a deployment. Keep `generated/` private and back it up securely: it holds the administrator passwords, authentik's secret key and the database password.

Back up PaperGo with its consistent online backup, and authentik with a database dump plus its data volume:

```sh
stamp=$(date -u +%Y%m%dT%H%M%SZ)
docker compose --env-file .env exec -T papergo /opt/papergo/papergo-admin backup --file "/data/papergo-$stamp.tar.gz"
docker compose --env-file .env cp "papergo:/data/papergo-$stamp.tar.gz" "generated/papergo-$stamp.tar.gz"
docker compose --env-file .env exec -T postgresql pg_dump -U authentik -Fc authentik > "generated/authentik-$stamp.dump"
```

Keep copied archives on separate backup storage and manage retention of the archives in `/data`. Stop PaperGo before restoring so the exclusive data lease can be acquired; see [administrative backup and restore](../../README.md#administrative-backup-and-restore). Restore authentik's dump with `pg_restore --clean` into the stopped stack's `postgresql` service, using the same `generated/authentik-secret-key`.

Setup reuses the recorded settings on later runs; credentials and secrets stay stable. Run `python3 setup.py --no-build` to finish or reconfigure an already built stack without repeating the hostnames, email or ports. If the `akadmin` password is lost, create a one-time recovery link with `docker compose --env-file .env exec authentik-worker ak create_recovery_key 10 akadmin` and open the printed path on the authentik hostname.

Run verification only after setup succeeds. Setup waits for authentik's discovery document and PaperGo's sign-in settings over verified HTTPS before writing `generated/ready`; a failed or interrupted setup leaves verification disabled.

The public hostnames, certificate issuance, email delivery and server firewall are deployment inputs. The local stack verifies sign-in, authorization and storage without requiring public DNS.

Sources: [authentik Docker Compose installation](https://docs.goauthentik.io/install-config/install/docker-compose/), [authentik blueprints](https://docs.goauthentik.io/customize/blueprints/), [authentik OAuth2 provider](https://docs.goauthentik.io/add-secure-apps/providers/oauth2/), [Caddy reverse proxy](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy), and the [PaperGo identity provider guide](../../docs/reverse-proxies.md).
