# Configuration

Every setting has a key in `config.yaml` and an environment variable. PaperGo reads three sources, and a later one wins wherever it sets a non-empty value:

1. `defaults.yaml` beside the executable, when there is one. The [container image](docker.md) installs one with its listen address, data directory and production mode; release ZIPs have none.
2. `config.yaml` in the working directory, or the file `PAPERGO_CONFIG` names.
3. Environment variables.

A YAML file sets only what it names, so a mounted `config.yaml` keeps the image's defaults for everything it leaves out: one that sets only `server.port` still binds to the image's `0.0.0.0`. Unknown keys, duplicate keys and more than one YAML document are refused, and a YAML value is checked as its environment variable is. The server and `papergo admin health`, `backup`, `restore`, `initialize` and `workers` load the same configuration, so a setting the server refuses also stops an admin command, which says so; `--help` works without it.

## Settings

| YAML key | Environment variable | Default and notes |
|---|---|---|
| `server.bind`, `server.port` | `HTTP_ADDR` | `127.0.0.1:8080`. The variable is `host:port`; in YAML, a key left out keeps the other's default. |
| `server.data_dir` | `DATABASE_PATH` | `data`: the database is `papergo.db` in it, and the search store and files default to `search.db` and `blobs` beside it. The variable names the database file. |
| `server.public_url` | `PUBLIC_URL` | The origin clients use, such as `https://docs.example.com`; see [reverse proxies](reverse-proxies.md#public-origin). |
| `server.environment` | `APP_ENV` | `development`; `test` or `production`. |
| `server.max_in_flight` | `MAX_IN_FLIGHT` | `32` concurrent API requests before new ones get 503. |
| `server.request_timeout` | `REQUEST_TIMEOUT` | `30s`; reads running longer are cancelled. |
| `server.live_streams_per_user` | `LIVE_STREAMS_PER_USER` | `4` live streams one person holds at once, 1 to 64; each open tab holds one. Raise it for kiosks that share one account. |
| `database.url` | `DATABASE_URL` | A `postgres://` URL keeps the database in PostgreSQL instead of SQLite. |
| `auth.mode` | `AUTH_MODE` | `local`; `development`, `oidc` or `proxy`. |
| `auth.admin_user` | `ADMIN_SUBJECT` | The first administrator; `admin` in local mode. |
| `auth.admin_group` | `ADMIN_GROUP` | An identity provider group made administrator, in place of `admin_user`. |
| `auth.admin_password` | `ADMIN_PASSWORD` | The local administrator's initial password; `admin` in development. Empty or `null` in YAML asks on an interactive first start. |
| `auth.admin_password_file` | `ADMIN_PASSWORD_FILE` | The same password, read from a file. |
| `auth.dev_token`, `auth.dev_subject` | `DEV_TOKEN`, `DEV_SUBJECT` | Development authentication: a token of at least 32 characters and its subject (`local-admin`). |
| `auth.proxy_secret_file` | `PROXY_SECRET_FILE` | Proxy mode: the shared secret's file. |
| `auth.proxy_trusted_cidrs` | `PROXY_TRUSTED_CIDRS` | Proxy mode: optional networks the proxy connects from; a YAML list, comma-separated as a variable. |
| `oidc.issuer`, `oidc.audience` | `OIDC_ISSUER`, `OIDC_AUDIENCE` | OIDC mode; see [identity](identity.md). |
| `oidc.client_id`, `oidc.client_scopes` | `OIDC_CLIENT_ID`, `OIDC_CLIENT_SCOPES` | The web app's sign-in client and scopes (`openid profile email offline_access`). |
| `oidc.groups_claim`, `oidc.groups_prefix` | `OIDC_GROUPS_CLAIM`, `OIDC_GROUPS_PREFIX` | Group synchronization. |
| `oidc.token_type` | `OIDC_TOKEN_TYPE` | A required JOSE `typ`, such as `at+jwt`. |
| `oidc.groups_from_userinfo` | `OIDC_GROUPS_FROM_USERINFO` | `true` reads groups from UserInfo when the token has none. |
| `search.store` | `SEARCH_STORE` | `sqlite`, or `postgres` with `DATABASE_URL`; `none` runs without search. |
| `search.database_path` | `SEARCH_DATABASE_PATH` | `search.db` beside the database. |
| `search.languages` | `SEARCH_LANGUAGES` | The organization locale's, else `english`; a YAML list, comma-separated as a variable. See [search](search.md). |
| `comments` | `COMMENTS` | `on` or `off`. |
| `connections.networks` | `CONNECTION_NETWORKS` | The private networks [connections](connections.md) may reach, such as `192.168.1.0/24`; none by default. A YAML list, comma-separated as a variable. Public addresses follow [trusted hosts](trusted-hosts.md) instead. |
| `storage.blob_path` | `BLOB_PATH` | `blobs` beside the database. |
| `storage.max_upload_mb` | `MAX_UPLOAD_BYTES` | 64 MB. YAML takes megabytes, the variable bytes. |
| `storage.edit_max_mb` | `FILE_EDIT_MAX_BYTES` | 2 MB; the largest file edited in the browser. |
| `mcp.enabled` | `MCP` | `true` (`on`); see [MCP](mcp.md). |
| `mcp.extensions` | `MCP_EXTENSIONS` | Compiled MCP tool contributions by ID. |
| `onnxruntime.library` | `ONNXRUNTIME_LIB` | The ONNX Runtime library OCR, local embeddings and decisions load; the bundled one by default. |
| `ocr.models`, `ocr.threads` | `OCR_MODELS`, `OCR_THREADS` | `assets/models/paddleocr` beside the executable; `1` thread. See [OCR](ocr.md). |
| `ai.execution` | `AI_EXECUTION` | `immediate` or `batch`, for requests that do not say. See [AI](ai.md). |
| `ai.chat.provider`, `.endpoint`, `.model`, `.api_key_file` | `AI_CHAT_PROVIDER`, `AI_CHAT_ENDPOINT`, `AI_CHAT_MODEL`, `AI_CHAT_API_KEY_FILE` | `none`; `openai` for any OpenAI-compatible endpoint (OpenAI's by default). |
| `ai.batch.provider`, `.endpoint`, `.model`, `.api_key_file` | `AI_BATCH_PROVIDER`, `AI_BATCH_ENDPOINT`, `AI_BATCH_MODEL`, `AI_BATCH_API_KEY_FILE` | `none`; `openai` or `anthropic`. |
| `ai.batch.max_tokens`, `.deadline` | `AI_BATCH_MAX_TOKENS`, `AI_BATCH_DEADLINE` | Anthropic's answer bound (16,000); how long a batch request waits (`72h`). |
| `ai.embeddings.provider`, `.model` | `AI_EMBEDDINGS_PROVIDER`, `AI_EMBEDDINGS_MODEL` | `none`; `local` (`embeddinggemma-2`) or `openai`. See [embeddings](embeddings.md). |
| `ai.embeddings.models_path`, `.threads`, `.max_tokens` | `AI_EMBEDDINGS_MODELS_PATH`, `AI_EMBEDDINGS_THREADS`, `AI_EMBEDDINGS_MAX_TOKENS` | Local models. |
| `ai.embeddings.endpoint`, `.api_key_file`, `.dimensions` | `AI_EMBEDDINGS_ENDPOINT`, `AI_EMBEDDINGS_API_KEY_FILE`, `AI_EMBEDDINGS_DIMENSIONS` | OpenAI-compatible endpoints. |
| `ai.decisions.models_path`, `.threads`, `.max_tokens`, `.head_tokens` | `AI_DECISION_MODELS_PATH`, `AI_DECISION_THREADS`, `AI_DECISION_MAX_TOKENS`, `AI_DECISION_HEAD_TOKENS` | The local decision engine; see [decisions](decisions.md). |
| `retention.runs`, `.system_runs`, `.result_cache` | `RUN_RETENTION`, `SYSTEM_RUN_RETENTION`, `RESULT_CACHE_TTL` | `720h`, `24h` and `720h`. |
| `quotas.per_creator`, `.per_workspace`, `.installation`, `.workspace_payload_bytes` | `OPERATION_QUOTA_PER_CREATOR`, `OPERATION_QUOTA_PER_WORKSPACE`, `OPERATION_QUOTA_INSTALLATION`, `OPERATION_QUOTA_WORKSPACE_PAYLOAD_BYTES` | Pending operations: 1,000, 5,000, 50,000 and 256 MiB; `0` leaves a bound off. |
| `organization.locale` | `ORGANIZATION_LOCALE` | See [organization defaults](#organization-defaults). |
| `organization.language`, `.number_format` | `ORGANIZATION_LANGUAGE`, `ORGANIZATION_NUMBER_FORMAT` | Organization preferences, applied when the database is created; see [preferences](preferences.md). |
| `organization.time_zone`, `.date_format`, `.time_format` | `ORGANIZATION_TIME_ZONE`, `ORGANIZATION_DATE_FORMAT`, `ORGANIZATION_TIME_FORMAT` | |
| `organization.theme`, `.default_currency`, `.document_languages` | `ORGANIZATION_THEME`, `ORGANIZATION_DEFAULT_CURRENCY`, `ORGANIZATION_DOCUMENT_LANGUAGES` | |
| `workers` | — | Operation workers, YAML only; see the [sample configuration](../deploy/config.yaml). |

A setting's API key or secret is read from a file the setting names, never from the configuration itself. In the container image, PaperGo runs as user and group 65532 and must be able to read those files; see [Docker deployment](docker.md#file-permissions).

## Organization defaults

The organization's preferences, which every person inherits until they choose their own, can come from the configuration. PaperGo applies them once, when it creates the database; after that the configuration no longer changes them, and they are changed in the app (Settings) or through [`PATCH /v1/organization/preferences`](preferences.md).

`organization.locale` sets them all from a preset, with the languages search stems:

| Locale | Time zone | Date | Time | Currency | Document languages | Search languages |
|---|---|---|---|---|---|---|
| `en-US` | (UTC) | `MM/dd/yyyy` | 12h | USD | `eng` | english |
| `en-GB` | Europe/London | `dd/MM/yyyy` | 24h | GBP | `eng` | english |
| `en-IE` | Europe/Dublin | `dd/MM/yyyy` | 24h | EUR | `eng` | english |
| `es-ES` | Europe/Madrid | `dd/MM/yyyy` | 24h | EUR | `spa+eng` | spanish, english |
| `fr-FR` | Europe/Paris | `dd/MM/yyyy` | 24h | EUR | `fra+eng` | french, english |
| `de-DE` | Europe/Berlin | `dd.MM.yyyy` | 24h | EUR | `deu+eng` | german, english |
| `de-AT` | Europe/Vienna | `dd.MM.yyyy` | 24h | EUR | `deu+eng` | german, english |
| `it-IT` | Europe/Rome | `dd/MM/yyyy` | 24h | EUR | `ita+eng` | italian, english |
| `pt-PT` | Europe/Lisbon | `dd/MM/yyyy` | 24h | EUR | `por+eng` | portuguese, english |
| `nl-NL` | Europe/Amsterdam | `dd-MM-yyyy` | 24h | EUR | `nld+eng` | dutch, english |

The language and number format are the locale itself. A preference named on its own replaces the locale's, and `search.languages` replaces its search languages, which, unlike the preferences, apply at every start:

```yaml
organization:
  locale: es-ES
  time_zone: Atlantic/Canary
```
