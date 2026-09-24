# VPS-Sync-GSBS

Self-contained weekly publisher for the [GSBS](https://github.com/dlommm/GSBS-Game-Sync-Backup-Service) PCGW manifest bundle. Keeps a local SQLite mirror of PCGamingWiki data in sync and uploads a pre-built bundle to Cloudflare R2 so every GSBS server can fetch game/save-path data without hammering the PCGW API.

**Companion repo to [GSBS — Game Sync & Backup Service](https://github.com/dlommm/GSBS-Game-Sync-Backup-Service).** GSBS is used as a Go library for the PCGW sync engine, the bundle exporter, and the `index.json` schema — so the publisher can never drift from what GSBS servers parse. Export, validation, index versioning, and R2 upload are implemented in this repo; every GSBS install consumes the published bundle by default (see GSBS's [docs/MANIFEST_BUNDLE.md](https://github.com/dlommm/GSBS-Game-Sync-Backup-Service/blob/main/docs/MANIFEST_BUNDLE.md)).

## Flow

```
Seed: production gsbs.db via rsync  ─or─  newest R2 db-backup/ snapshot
        │
        ▼
  VPS working DB ──► PCGW incremental sync (weekly, via Special:CargoExport)
        │
        ▼
  Safe SQLite snapshot ──► export manifest.json.gz + index.json
        │                          │
        │                          └──► R2 db-backup/  (mirror backup)
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
| `vps-sync serve` | Stay up and publish on `SCHEDULE` — what the container runs |
| `vps-sync bootstrap` | First run: seed the DB (if needed) + publish full bundle to R2 |
| `vps-sync run` | One publish now: PCGW sync → export → validate → R2 upload → prune archives |
| `vps-sync fetch-prod` | Rsync production `gsbs.db` |
| `vps-sync restore-db` | Restore `gsbs.db` from R2 `db-backup/` (newest, or a given key) |
| `vps-sync pcgw-sync` | Incremental PCGW API sync only |
| `vps-sync export` | Local export without upload |
| `vps-sync validate` | Validate artifacts in `OUT_DIR` |
| `vps-sync repair-db` | Recreate GSBS tables missing from a sanitized seed DB |
| `vps-sync version` | Print the build version stamped into the binary |

### Seeding a fresh host

`bootstrap` and `serve` both seed an absent database on their own: from `PROD_DB_SRC`
when it is set, otherwise from the newest snapshot under the R2 `db-backup/` prefix.
That second path is what makes a destroyed VPS recoverable — the weekly job has
always been writing those snapshots, and they are the only full copy of the mirror
once the original host is gone. Set `AUTO_BOOTSTRAP=0` to require seeding by hand.

### Sanitized seed databases

If you seed the publisher from a production `gsbs.db` with the user tables stripped (recommended — no user data on the VPS), newer GSBS migrations that alter those tables will fail with `no such table`. Run `vps-sync repair-db` once: it recreates every missing table/index empty, in current shape, from GSBS's own schema, and stamps the schema version. PCGW data is never touched.

## Quick start — a fresh VPS

The only host requirement is Docker. No Go toolchain, no cron, no checkout of
GSBS: the image is prebuilt on GHCR and carries its own schedule.

```bash
# 1. Docker (Debian/Ubuntu)
curl -fsSL https://get.docker.com | sudo sh

# 2. This repo — only the compose file and .env are needed
sudo mkdir -p /opt/vps-sync-gsbs && cd /opt/vps-sync-gsbs
sudo git clone https://github.com/dlommm/vps-sync-gsbs.git .

# 3. Configure
sudo cp .env.example .env
sudo nano .env      # R2 keys, R2_ENDPOINT, R2_BUCKET, PUBLIC_BASE
sudo chmod 600 .env

# 4. Start. With no database present it restores the newest R2 db-backup/
#    snapshot, publishes once, then waits for SCHEDULE.
sudo docker compose up -d
sudo docker compose logs -f
```

Set `RUN_ON_START=1` in `.env` for that first immediate publish; leave it `0`
afterwards so a restart does not republish every time.

Useful one-offs:

```bash
docker compose run --rm sync run          # publish now
docker compose run --rm sync restore-db   # re-pull the mirror from R2
docker compose run --rm sync validate     # check artifacts
docker compose pull && docker compose up -d   # update to the latest image
```

<details>
<summary>Running without Docker (the previous cron-based setup)</summary>

```bash
go build -tags pcgwcrawl -o bin/vps-sync ./cmd/vps-sync
./scripts/bootstrap.sh
sudo cp deploy/cron.gsbs-vps-sync /etc/cron.d/gsbs-vps-sync
sudo cp deploy/logrotate.gsbs-vps-sync /etc/logrotate.d/gsbs-vps-sync
```

`-tags pcgwcrawl` is required: GSBS refuses to crawl PCGamingWiki without it.
To update immediately instead of waiting for Sunday: `./scripts/update-and-run.sh run`.
</details>

### Configuration (`.env`)

| Variable | Purpose |
|----------|---------|
| `GSBS_DB` | Local publisher database |
| `PROD_DB_SRC` / `FETCH_PROD_DB` | `user@host:/path/to/gsbs.db` rsync seed (optional) |
| `RUN_PCGW_SYNC` | `1` = incremental PCGW sync before each export |
| `PUBLIC_BASE` | Public read URL, e.g. `https://gsbs.ohhcloud.com/manifest/` |
| `R2_*`, `AWS_*` | R2 write credentials (bucket-scoped, VPS only) |
| `WEBHOOK_URL` | Optional Discord/Slack webhook — posts run result + published version |
| `DB_BACKUP` / `DB_BACKUP_KEEP` | Weekly gzip'd DB snapshot to private `db-backup/` prefix (default on, keep 6) |
| `SCHEDULE` / `SCHEDULE_TZ` | `serve` cron expression and its zone (default `0 3 * * 0`, `UTC`) |
| `RUN_ON_START` | `1` = publish once at container start instead of waiting for the first tick |
| `AUTO_BOOTSTRAP` | `1` = seed a missing DB before the first run (default on) |
| `SYNC_IMAGE` | Pin a specific image tag instead of tracking `:latest` |

Secrets can live in a separate root-owned file instead of `.env` — see `deploy/secrets.env.example` (`ENV_FILE=/etc/gsbs-sync/env ./bin/vps-sync run`; `.env` still supplies the non-secret settings).

## R2 layout

```
gsbs/  (bucket)
  manifest/
    index.json           ← versioned pointer, uploaded last (atomic cutover)
    manifest.json.gz     ← full bundle, content-hash cache key in index URL
    manifest.meta.json
  archive/v<N>-<timestamp>/   ← pruned automatically (R2_KEEP newest kept)
  db-backup/gsbs-<timestamp>.db.gz  ← weekly full-mirror snapshot (DB_BACKUP_KEEP newest kept)
```

GSBS servers read via your public domain (`PUBLIC_BASE`). Writes use the R2 S3 endpoint with your API token. If the local `out/` copy of `index.json` is ever lost (redeploy), the publisher re-seeds the version counter from the live published index so `manifest_version` never regresses and every server keeps updating.

## Docker

The container is the scheduler: `vps-sync serve` is the default command, so the
publisher stays up and fires on `SCHEDULE` with nothing installed on the host.
Images are built by CI and pushed to `ghcr.io/dlommm/vps-sync-gsbs`.

State lives in the `gsbs-data` volume (`/data`): the mirror database, `out/`
artifacts, and logs. Logs also go to stdout, so `docker compose logs -f` is the
one place to look.

To build locally rather than pull — for an unpushed change, or a host that
cannot reach GHCR:

```bash
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

The image is built with `-tags pcgwcrawl`. GSBS gates PCGW crawling behind that
tag, so this is the only image in the fleet permitted to reach the wiki; every
other GSBS install consumes the published bundle instead.

## Development

Requires a local GSBS checkout as a sibling directory (see `replace` in `go.mod`):

```
Applications/
  GSBS-Game-Sync-Backup-Service/
  VPS-Sync-GSBS/
```

```bash
go build -tags pcgwcrawl -o bin/vps-sync ./cmd/vps-sync
go test ./...
PUBLIC_BASE=https://example.com/manifest/ ./bin/vps-sync export
```

Without a Go toolchain on the host, the same checks run in a container:

```bash
docker run --rm -e CGO_ENABLED=1 -v "$PWD/..":/work -w /work/VPS-Sync-GSBS \
  golang:1.25-bookworm go test -buildvcs=false -tags pcgwcrawl ./...
```

## Resilience

- **Publish survives a failed PCGW sync** — the pipeline warns (log + webhook) and publishes the existing data instead of skipping the week.
- **Shrink guard** — an export whose row counts collapse >25% vs the previous publish is refused before any artifact is touched (a truncated database can't ship); `FORCE_PUBLISH=1` overrides deliberately. GSBS consumers additionally cap deletion reconciliation at 25% per import.
- **DB backup and restore** — the publisher database is the only full PCGW mirror in the fleet (published bundles are lite). Every run uploads a gzip'd snapshot to the private `db-backup/` prefix, and `vps-sync restore-db` pulls the newest one back; `serve` and `bootstrap` do it automatically when they find no database. Disaster recovery is one command, not a multi-day API crawl.
- **Version regression protection** — if the local `out/index.json` is lost, the previous version is re-seeded from the live published index.

## Operating model

- **GSBS servers** — default to `pcgw_sync_source=s3` and read `https://gsbs.ohhcloud.com/manifest/index.json` out of the box; no per-server configuration needed.
- **This VPS** — owns PCGW API sync and publishing. Runs weekly; publishing more often is safe (each run is a full re-baseline).
- **Seed once** from production so you skip a multi-day initial PCGW crawl.

## License

PCGW data is [CC BY-SA](https://creativecommons.org/licenses/by-sa/3.0/). Application code follows GSBS licensing.
