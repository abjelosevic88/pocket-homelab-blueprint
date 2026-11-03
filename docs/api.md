# API

JSON over HTTP under `/api/v1`. Authenticate with the session cookie, a personal token (`Authorization: Bearer pbp_…`, Settings → API & shortcuts), a Pocket Core token, or a Pocket Core link token (`pcx_…`, only the routes of its scope). Everything is per person: other people's ids answer 404.

## Codebases

| | |
|---|---|
| `GET /codebases` | Your codebases, each with `latest` (any status) and `done` (the latest finished scan, with `stats`) |
| `POST /codebases` | `{source: git\|path\|files, location, name?, branch?, subdir?, git_token?, git_user?, vendored?, rescan_hours?, scan?}` — `location` is the https address, the server folder, or `space:/path` in Pocket Files. Scans right away unless `scan: false` |
| `POST /codebases/upload` | multipart: `file` (.zip/.tar.gz/.tar), `name?`, `subdir?`; with `codebase=<id>` it replaces that uploaded codebase's code |
| `GET / PUT / DELETE /codebases/{id}` | One codebase; `PUT` takes the same fields, `scan: true` scans after saving |
| `POST /codebases/{id}/scan` | Queue a scan (`202`), or get the one already waiting (`200`) |
| `GET /codebases/{id}/scans` | Every scan, newest first |
| `GET/POST /codebases/{id}/notes`, `PUT/DELETE /notes/{id}` | Notes on a path (`?path=` filters; `""` = the whole codebase) |
| `GET /codebases/{id}/shares`, `PUT/DELETE /codebases/{id}/shares/{uid}` | Who a codebase is shared with; share it (`{"role": "view" \| "scan"}`) or stop. The owner shares; anyone may remove themselves. Shared codebases are in `GET /codebases` with `role` and `owner` |
| `GET /people` | Everyone else on this box, to share with |
| `GET /search?q=&codebase=` | Files, symbols, endpoints, tables and variables in the latest scan of each codebase |
| `GET /sources`, `/sources/browse?path=`, `/sources/files?space=&path=` | What can be scanned; folders on the server (admins) or in Pocket Files |

## What a scan found

All under `/scans/{id}` (a finished scan of yours).

| | |
|---|---|
| `GET /scans/{id}` | Status, stage, progress, stats; `POST …/cancel`, `DELETE` |
| `GET …/overview` | Numbers, languages, top folders, entry points, packages, loops, hotspots, git |
| `GET …/tree` | Every file (`lines`, `imports`, `imported_by`, `churn`…) and folder |
| `GET …/file?path=` | One file (symbols, imports, imported by, endpoints, variables, calls, tables, notes) or folder (files, subfolders, folders it uses and is used by) |
| `GET …/source?path=` | The file's text as it is on disk now (`changed` when it differs from the scan) |
| `GET …/graph?level=dir\|file&depth=&root=&tests=&focus=&radius=` | Nodes and edges of the import graph |
| `GET …/symbols?q=&kind=&root=`, `…/symbol?file=&name=&parent=` | Find symbols; one symbol with callers and callees (Go) |
| `GET …/endpoints` | Endpoints from the code and from API documents |
| `GET …/openapi.json`, `…/openapi.yaml` | An OpenAPI 3.1 document of them (`?source=code\|spec`, `?download=1`) |
| `GET …/data`, `…/config`, `…/hotspots` | Tables and references; variables, services, entry points, packages; hotspots and authors |
| `GET …/compare/{other}` | What changed from scan `other` to this one |
| `GET …/report.md`, `…/export.json` | Everything as Markdown; the raw result |
| `POST …/explain {path}`, `POST …/ask {question, path?}` | The AI explains a path (kept as its note) / answers a question |

```sh
# scan again from CI after a merge
curl -X POST -H "Authorization: Bearer pbp_…" https://blueprint.example.com/api/v1/codebases/3/scan
# the OpenAPI document of the latest scan
curl -H "Authorization: Bearer pbp_…" https://blueprint.example.com/api/v1/scans/41/openapi.yaml
```

## Pocket Core links

Blueprint offers `blueprint:read` (codebases, scans and everything above except `source`, notes and AI) and `blueprint:write` (`POST /codebases` for git repositories, `POST /codebases/{id}/scan`). It uses Pocket Files with `files:read`. Events: `blueprint.scan_finished`, `blueprint.scan_failed` (`codebase_id`, `name`, `scan_id`, `url`, numbers or `error`, `user_email`), `blueprint.shared` to the person a codebase is shared with (`codebase_id`, `name`, `role`, `by`, `url`). Links reach only the person's own codebases, not those shared with them.

## The rest

`/auth/*`, `/users`, `/settings`, `/tokens`, `/backup` (admin), `/export.json`, `/import`, `/integrations/test`, `/ai/status`, `/ai/models` — as in every Pocket app. `/healthz`, `/readyz`, `/metrics` are public.
