# Self-contained GSBS manifest publisher (PCGW sync + R2 upload).
#
# The image is the scheduler: `vps-sync serve` stays up and publishes on
# SCHEDULE, so a fresh host needs nothing but `docker compose up -d`.
#
# GSBS is cloned at build time to satisfy the go.mod replace directive, and is
# built with `-tags pcgwcrawl` — the tag that permits crawling PCGamingWiki.
# This is the only image in the fleet that carries it; see GSBS
# server/job/crawl_disabled.go.
ARG GSBS_REPO_URL=https://github.com/dlommm/GSBS-Game-Sync-Backup-Service.git
ARG GSBS_REF=main

FROM golang:1.25-bookworm AS builder
ARG GSBS_REPO_URL GSBS_REF
ARG VERSION=dev
WORKDIR /build

RUN apt-get update && apt-get install -y --no-install-recommends \
      git gcc libc6-dev libsqlite3-dev \
  && rm -rf /var/lib/apt/lists/*

RUN git clone --depth 1 --branch "$GSBS_REF" "$GSBS_REPO_URL" /deps/gsbs

COPY . .

# Repoint the replace at the clone. This has to run AFTER the source copy:
# doing it before, as an optimisation alongside `COPY go.mod go.sum`, left the
# rewrite in place only until `COPY . .` restored the original go.mod, and the
# build then failed looking for a sibling checkout that does not exist here.
RUN sed -i -E 's|^(replace github.com/gsbs/gsbs =>).*|\1 /deps/gsbs|' go.mod \
  && grep -q '=> /deps/gsbs' go.mod

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -tags pcgwcrawl -buildvcs=false \
      -ldflags "-X main.version=${VERSION}" \
      -o /out/vps-sync ./cmd/vps-sync

FROM debian:bookworm-slim
ARG VERSION=dev
ARG REVISION=unknown

# openssh-client is required for PROD_DB_SRC: fetch.ProdDB shells out to rsync,
# and a user@host:/path source is an rsync-over-ssh transfer.
RUN apt-get update \
  && apt-get install -y --no-install-recommends \
       ca-certificates sqlite3 rsync openssh-client tzdata \
  && rm -rf /var/lib/apt/lists/*

# Run unprivileged. The volume is chowned here so a named volume created by
# Docker (root-owned by default) is writable by the service account.
RUN useradd --system --create-home --uid 10001 --shell /usr/sbin/nologin vpssync \
  && mkdir -p /data/out /data/logs \
  && chown -R vpssync:vpssync /data

COPY --from=builder /out/vps-sync /usr/local/bin/vps-sync
COPY scripts/ /opt/vps-sync-gsbs/scripts/

WORKDIR /opt/vps-sync-gsbs
USER vpssync

ENV GSBS_DB=/data/gsbs.db \
    OUT_DIR=/data/out \
    LOG_FILE=/data/logs/vps-sync.log \
    LOG_MIRROR_STDERR=1

VOLUME ["/data"]

LABEL org.opencontainers.image.source="https://github.com/dlommm/vps-sync-gsbs" \
      org.opencontainers.image.description="GSBS PCGW manifest publisher (scheduled bundle export to Cloudflare R2)" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"

# vps-sync installs its own SIGTERM handler, so it is safe as PID 1: `docker
# stop` unwinds an in-flight publish rather than killing it mid-upload.
ENTRYPOINT ["vps-sync"]
CMD ["serve"]
