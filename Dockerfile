# Self-contained GSBS manifest publisher (PCGW sync + R2 upload).
#
# Build clones GSBS at compile time for the store/job/pcgw packages.
ARG GSBS_REPO_URL=https://github.com/dlommm/GSBS--Game-Sync---Backup-Service-.git
ARG GSBS_REF=main

FROM golang:1.25-bookworm AS builder
ARG GSBS_REPO_URL GSBS_REF
WORKDIR /build

RUN apt-get update && apt-get install -y --no-install-recommends git gcc libc6-dev libsqlite3-dev \
  && rm -rf /var/lib/apt/lists/*

# Clone GSBS dependency (replace target in go.mod)
RUN git clone --depth 1 --branch "$GSBS_REF" "$GSBS_REPO_URL" /deps/gsbs

COPY go.mod go.sum ./
RUN sed -i 's|../GSBS (Game Sync & Backup Service)|/deps/gsbs|g' go.mod

COPY . .
RUN CGO_ENABLED=1 go build -o /out/vps-sync ./cmd/vps-sync

FROM debian:bookworm-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates sqlite3 rsync \
  && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/vps-sync /usr/local/bin/vps-sync
COPY scripts/ /opt/vps-sync-gsbs/scripts/

WORKDIR /opt/vps-sync-gsbs
ENV GSBS_DB=/data/gsbs.db OUT_DIR=/data/out
VOLUME ["/data"]
ENTRYPOINT ["vps-sync"]
CMD ["run"]
