# Custom Readeck build with markdown rendering in bookmark description.
# Patch: components.Markdown() helper + 2 templ files use @Markdown(...).
#
# Build: docker build -t readeck-custom:1.0 .
# Run:   docker compose up -d  (after swapping image in docker-compose.yml)

# ---------- Stage 1: frontend assets ----------
# gulpfile writes to ../assets/www (relative to web/) → /src/assets/www
FROM node:22-bookworm-slim AS web
WORKDIR /src
COPY web /src/web
RUN cd web && npm ci --silent && npm run build
# at this point /src/assets/www/ holds the built JS/CSS/etc.

# ---------- Stage 2: Go binary ----------
FROM golang:1.26-bookworm AS go
WORKDIR /src
# install make + xgettext for locales + ca-certificates
RUN apt-get update && apt-get install -y --no-install-recommends \
    make gettext ca-certificates git \
    && rm -rf /var/lib/apt/lists/*

# copy entire source first
COPY . /src
# overlay the built frontend assets (gulp dest = /src/assets/www in stage 1)
COPY --from=web /src/assets/www /src/assets/www

# install templ CLI version that matches go.mod (pinned)
RUN go install github.com/a-h/templ/cmd/templ@v0.3.1020

# regenerate templ files (since we patched the .templ source)
RUN templ generate

# compile locales (needed for the binary to start)
RUN cd locales && make compile || true

# build the binary with the same tags Readeck Makefile uses
ENV CGO_ENABLED=1
RUN go build \
    -v \
    -tags "netgo osusergo sqlite_omit_load_extension sqlite_foreign_keys sqlite_json1 sqlite_fts5 sqlite_secure_delete" \
    -ldflags="-s -w \
        -X 'codeberg.org/readeck/readeck/configs.version=custom-md-render' \
        -X 'codeberg.org/readeck/readeck/configs.buildTimeStr=$(date -u +%Y-%m-%dT%H:%M:%SZ)'" \
    -trimpath \
    -o /readeck .

# ---------- Stage 3: runtime ----------
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

COPY --from=go /readeck /usr/local/bin/readeck

# default data dir matches official image
RUN mkdir -p /readeck && chmod 755 /readeck
VOLUME /readeck
WORKDIR /readeck

EXPOSE 8000

# Bind 0.0.0.0 inside container; docker-compose maps to 127.0.0.1:8000 on host
ENTRYPOINT ["/usr/local/bin/readeck"]
CMD ["serve", "-host", "0.0.0.0", "-port", "8000"]
