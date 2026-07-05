# VPS-Sync-GSBS

Self-contained weekly publisher for the GSBS PCGW manifest bundle. Keeps a local SQLite mirror of PCGamingWiki data in sync and uploads a pre-built bundle to Cloudflare R2 so every GSBS server can fetch game/save-path data without hammering the PCGW API.

**No gsbs-manifest dependency.** Export, validation, index versioning, and R2 upload are implemented in this repo. GSBS is used as a Go library for the PCGW sync engine, the bundle exporter, and the `index.json` schema — so the publisher can never drift from what GSBS servers parse.

## Flow

```
Production gsbs.db (one-time seed via rsync/SFTP)
        │
        ▼
  VPS working DB ──► PCGW API incremental sync (weekly)
        │
        ▼
  Safe SQLite snapshot ──► export manifest.json.gz + index.json
        │
        ▼
  Cloudflare R2  manifest/
        │
        ▼
  GSBS servers (s3 bundle mode) auto-fetch
```

**Full-bundle-only publishing.** Every publish uploads the complete manifest and bumps `manifest_version` by 1. GSBS servers read `index.json` (one cheap round-trip, ETag-cached), and when behind they merge the full bundle — the import upserts with skip-unchanged semantics, so catching up from any version is a single fetch. Deltas are not published; current GSBS ignores them.

## Commands

| Command | Purpose |
|---------|---------|
| `vps-sync bootstrap` | First run: pull prod DB (if needed) + publish full bundle to R2 |
| `vps-sync run` | Weekly job: PCGW sync → export → validate → R2 upload → prune archives |
| `vps-sync fetch-prod` | Rsync production `gsbs.db` |
| `vps-sync pcgw-sync` | Incremental PCGW API sync only |
| `vps-sync export` | Local export without upload |
| `vps-sync validate` | Validate artifacts in `OUT_DIR` |
| `vps-sync repair-db` | Recreate GSBS tables missing from a sanitized seed DB |

### Sanitized seed databases

If you seed the publisher from a production `gsbs.db` with the user tables stripped (recommended — no user data on the VPS), newer GSBS migrations that alter those tables will fail with `no such table`. Run `vps-sync repair-db` once: it recreates every missing table/index empty, in current shape, from GSBS's own schema, and stamps the schema version. PCGW data is never touched.

## Quick start

```bash
git clone <this-repo> /opt/vps-sync-gsbs
cd /opt/vps-sync-gsbs

cp .env.example .env
nano .env   # replace every REPLACE_WITH_* placeholder (R2 account ID + API keys)
chmod 600 .env

go build -o bin/vps-sync ./cmd/vps-sync
./scripts/bootstrap.sh

# Weekly cron (Sunday 03:00 UTC)
sudo cp deploy/cron.gsbs-vps-sync /etc/cron.d/gsbs-vps-sync
sudo cp deploy/logrotate.gsbs-vps-sync /etc/logrotate.d/gsbs-vps-sync
```

### Configuration (`.env`)

| Variable | Purpose |
|----------|---------|
| `GSBS_DB` | Local publisher database |
| `PROD_DB_SRC` / `FETCH_PROD_DB` | `user@host:/path/to/gsbs.db` rsync seed (optional) |
| `RUN_PCGW_SYNC` | `1` = incremental PCGW sync before each export |
| `PUBLIC_BASE` | Public read URL, e.g. `https://gsbs.ohhcloud.com/manifest/` |
| `R2_*`, `AWS_*` | R2 write credentials (bucket-scoped, VPS only) |
| `WEBHOOK_URL` | Optional Discord/Slack webhook — posts run result + published version |

Secrets can live in a separate root-owned file instead of `.env` — see `deploy/secrets.env.example` (`ENV_FILE=/etc/gsbs-sync/env ./bin/vps-sync run`; `.env` still supplies the non-secret settings).

## R2 layout

```
gsbs/  (bucket)
  manifest/
    index.json           ← versioned pointer, uploaded last (atomic cutover)
    manifest.json.gz     ← full bundle, content-hash cache key in index URL
    manifest.meta.json
  archive/v<N>-<timestamp>/   ← pruned automatically (R2_KEEP newest kept)
```

GSBS servers read via your public domain (`PUBLIC_BASE`). Writes use the R2 S3 endpoint with your API token. If the local `out/` copy of `index.json` is ever lost (redeploy), the publisher re-seeds the version counter from the live published index so `manifest_version` never regresses and every server keeps updating.

## Docker

```bash
cp .env.example .env
docker compose build
docker compose --profile manual run --rm sync bootstrap   # first run
docker compose --profile manual run --rm sync run         # weekly
```

## Development

Requires a local GSBS checkout as a sibling directory (see `replace` in `go.mod`):

```
Github/
  GSBS (Game Sync & Backup Service)/
  VPS-Sync-GSBS/
```

```bash
go build -o bin/vps-sync ./cmd/vps-sync
go test ./...
PUBLIC_BASE=https://example.com/manifest/ ./bin/vps-sync export
```

## Operating model

- **GSBS servers** — default to `pcgw_sync_source=s3` and read `https://gsbs.ohhcloud.com/manifest/index.json` out of the box; no per-server configuration needed.
- **This VPS** — owns PCGW API sync and publishing. Runs weekly; publishing more often is safe (each run is a full re-baseline).
- **Seed once** from production so you skip a multi-day initial PCGW crawl.

## License

PCGW data is [CC BY-SA](https://creativecommons.org/licenses/by-sa/3.0/). Application code follows GSBS licensing.
