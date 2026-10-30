# Configuration

Environment variables; every one has a default. Personal settings (history to read, scans to keep, the graph, connections to the other Pocket apps, notifications, AI) live in the UI per person — the `OPENAI_*`/`NOTIFY_*` values below are copied once into the first admin's settings.

| Variable | Default | What |
|---|---|---|
| `PORT`, `HOST` | `8080`, `0.0.0.0` | Listen address |
| `BASE_URL` | – | Public URL (links in notifications, the OIDC callback) |
| `DATA_DIR` | `./data` | Database, clones and unpacked archives (`work/`), Core token, backups |
| `DB_PATH` | `$DATA_DIR/pocket-blueprint.db` | SQLite file |
| `SOURCE_ROOTS` | – | Comma-separated server folders admins may scan in place (mount them read-only in Docker) |
| `MAX_UPLOAD_MB` | `300` | Largest archive (also the largest folder fetched from Pocket Files) |
| `MAX_FILES` | `20000` | Files read per scan; the rest is counted as left out |
| `SCAN_TIMEOUT`, `CLONE_TIMEOUT` | `15m`, `10m` | How long reading the code / cloning may take |
| `MAX_BODY_BYTES` | `67108864` | Largest JSON request (an export being imported) |
| `TZ` | – | Time zone for dates shown by the server |
| `AUTH_MODE` | `local` | `local` (accounts), `proxy` (trust `AUTH_PROXY_HEADER`/`AUTH_PROXY_EMAIL_HEADER`), `none` |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD`, `ADMIN_NAME` | – | Seed the first admin while the database is empty |
| `SESSION_TTL`, `SECURE_COOKIES` | `720h`, `false` | Set `SECURE_COOKIES=true` behind HTTPS |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_NAME`, `OIDC_AUTO_CREATE` | – | Sign in with an OpenID Connect provider (not needed with Pocket Core) |
| `NOTIFY_URL`, `NOTIFY_FORMAT` | – | Apprise (`…/notify/key`), ntfy topic URL or JSON webhook; format `apprise`/`ntfy`/`json` |
| `OPENAI_API_KEY`, `OPENAI_MODEL`, `OPENAI_BASE_URL` | – | Explanations and questions without Pocket AI |
| `CORE_URL`, `CORE_REGISTRATION_KEY`, `CORE_APP_URL` | – | Pocket Core |
| `TRUST_PROXY`, `METRICS_ENABLED`, `LOG_LEVEL`, `LOG_FORMAT` | `true`, `true`, `info`, `text` | |
| `DEMO_DATA` | `false` | Add "Northwind Shop", a small demo codebase scanned twice, to the owner account |

The container has `git`. Scans run one at a time; a restart marks the running scan as failed and carries on with the queue.

Commands: `pocket-blueprint backup [-o FILE]`, `pocket-blueprint reset-password [-email] [-password]`, `pocket-blueprint version`.
