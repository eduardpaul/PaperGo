# Dependencies

Direct Go modules and the licences they ship under. Add a row when a change adds a direct dependency, and check its licence first.

| Module | Used for | Licence |
| --- | --- | --- |
| `ariga.io/atlas` | migration directory validation and checksums | Apache-2.0 |
| `entgo.io/ent` | data access and generated models | Apache-2.0 |
| `github.com/coreos/go-oidc/v3` | OIDC bearer token verification | Apache-2.0 |
| `github.com/dbos-inc/dbos-transact-golang` | the embedded workflow runner (`internal/runner` only) | MIT |
| `github.com/go-jose/go-jose/v4` | JWT handling | Apache-2.0 |
| `github.com/google/uuid` | identifiers | BSD-3-Clause |
| `github.com/robfig/cron/v3` | parsing workflow schedule triggers | MIT |
| `modernc.org/sqlite` | CGO-free SQLite driver | BSD-3-Clause |

The runner also links, through DBOS: `github.com/jackc/pgx/v5`, `puddle/v2`, `pgpassfile`, `pgservicefile` and `pgerrcode` (MIT), and `github.com/gorilla/websocket` (BSD-2-Clause; DBOS's optional Conductor client, not used).
