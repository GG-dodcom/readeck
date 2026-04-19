#!/usr/bin/make

# SPDX-FileCopyrightText: © 2020 Olivier Meunier <olivier@neokraft.net>
#
# SPDX-License-Identifier: AGPL-3.0-only

-include .makerc

ifeq (, $(shell which git && git status))
VERSION ?= dev
DATE ?= $(shell date --rfc-3339=seconds)
else
VERSION := $(shell git describe --tags)
DATE := $(shell git log -1 --format=%cI)
endif

BUILD_TAGS := netgo osusergo sqlite_omit_load_extension sqlite_foreign_keys sqlite_json1 sqlite_fts5 sqlite_secure_delete
VERSION_FLAGS := \
	-X 'codeberg.org/readeck/readeck/configs.version=$(VERSION)' \
	-X 'codeberg.org/readeck/readeck/configs.buildTimeStr=$(DATE)'

OUTFILE_NAME ?= readeck
LDFLAGS ?= -s -w
DIST ?= dist
GO ?= go
export CGO_ENABLED ?= 1
export CGO_CFLAGS ?= -D_LARGEFILE64_SOURCE
export CC?=
export GOOS?=
export GOARCH?=

SITECONFIG_SRC=./ftr-site-config
SITECONFIG_DEST=pkg/extract/contentscripts/assets/site-config

TEMPL_PKG ?= github.com/a-h/templ/cmd/templ@latest
GOLANGCI_PKG ?= github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.9.0
AIR_PKG ?= github.com/air-verse/air@v1.64.5
SLOC_PKG ?= github.com/boyter/scc/v3@v3.6.0

# -------------------------------------------------------------------
# Base targets
# -------------------------------------------------------------------

# These targets provide all what's needed to build Readeck.
# On a fresh copy, the workflow would be:
#   make setup && make all
#
# If you plan to write code, have a look at the development
# targets below.


# Build the app
.PHONY: all
all: generate build
	touch $(DIST)/.all

# Generate files (web and documentation artifacts)
.PHONY: generate
generate: web-build docs-build
	${MAKE} -C locales compile
	mkdir -p $(DIST)
	touch $(DIST)/.generate

# Setup prepares the environment
.PHONY: setup
setup:
	@echo "GOVERSION: $(shell go env GOVERSION)"
	@echo "GOOS: $(shell go env GOOS)"
	@echo "GOARCH: $(shell go env GOARCH)"
	$(GO) mod download
	${MAKE} -C web setup

templ:
	$(GO) run $(TEMPL_PKG) generate

# Build the server
.PHONY: build
build:
	@echo "GOVERSION: $(shell go env GOVERSION)"
	@echo "GOOS: $(shell go env GOOS)"
	@echo "GOARCH: $(shell go env GOARCH)"
	@echo "CGO_ENABLED: $(CGO_ENABLED)"
	@echo "CC: $(CC)"
	@echo "CXX: $(CXX)"
	@echo "LDFLAGS: $(LDFLAGS)"
	@echo "OUTFILE_NAME: $(OUTFILE_NAME)"
	$(GO) build \
		-v \
		-tags "$(BUILD_TAGS)" \
		-ldflags="$(VERSION_FLAGS) $(LDFLAGS)" \
		-trimpath \
		-o $(DIST)/$(OUTFILE_NAME)

# Build the documentation
.PHONY: docs-build
docs-build:
	${MAKE} -C docs build

# Build the frontend assets
.PHONY: web-build
web-build:
	@$(MAKE) -C web build

# Launch tests
.PHONY: test
test: docs-build
	test -f assets/www/manifest.json || echo "{}" > assets/www/manifest.json
	GOTOOLCHAIN=$(shell go env GOVERSION) $(GO) test \
		-tags "$(BUILD_TAGS)" \
		-ldflags="$(VERSION_FLAGS) $(LDFLAGS)" \
		-cover ./...

# Clean the build
.PHONY: clean
clean:
	rm -rf $(DIST)
	rm -rf assets/www/*
	make -C docs clean
	make -C locales clean
	make -C web clean

# List all the modules included by the build
list:
	$(GO) list \
		-tags "$(BUILD_TAGS)" \
		-ldflags="$(VERSION_FLAGS) $(LDFLAGS)" \
		-m all

# Run the linter and print a report
.PHONY: lint
lint: docs-build
	test -f assets/www/manifest.json || echo "{}" > assets/www/manifest.json
	$(GO) run $(GOLANGCI_PKG) run

.PHONY: vulncheck
vulncheck:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest \
		-show color,verbose,version \
		./...

# Single lines of code
.PHONY: sloc
sloc:
	$(GO) run $(SLOC_PKG) -i go,js,scss,html,md


# Update site-config folder with definitions from
# graby git repository
.PHONY: update-site-config
update-site-config:
	rm -rf $(SITECONFIG_DEST)
	$(GO) run ./tools/ftr $(SITECONFIG_SRC) $(SITECONFIG_DEST)


# -------------------------------------------------------------------
# Development targets
# -------------------------------------------------------------------

# These targets provide helper for autoreload during development.
# `make dev` starts all the needed watch/autoreload and is only
# needed when working on web/* or docs/src/*.
# When working only on go and templ files, `make serve` is enough.

# Starts the HTTP server
# It runs air watching the source files and the assets. It builds and reloads
# the server on any change.
.PHONY: serve
serve:
	${MAKE} -j2 watch/templ watch/server

# Starts 3 watchers/reloaders for a full autoreload
# dev server.
# The initial errors during startup are normal
.PHONY: dev
dev:
	${MAKE} -j3 watch/docs watch/web serve

# Starts the HTTP server
# It runs air watching the source files and the assets. It builds and reloads
# the server on any change.
.PHONY: watch/server
watch/server: SERVE_CMD ?= serve
watch/server:
	$(GO) run $(AIR_PKG) \
		--tmp_dir "dist" \
		--build.log "" \
		--build.cmd "${MAKE} DATE= build" \
		--build.bin "dist/readeck" \
		--build.args_bin "$(SERVE_CMD)" \
		--build.delay 2000 \
		--build.exclude_dir "" \
		--build.include_dir "assets,components,configs,docs/assets,locales,internal,pkg" \
		--build.include_ext "go,html,json,js,mo,tmpl,toml,xsl" \
		--build.kill_delay "10s" \
		--build.stop_on_error "false" \
		--misc.clean_on_exit false

# Rebuild components on change.
# It runs air watching the .templ files and rebuilds them on any change.
watch/templ:
	$(GO) run $(AIR_PKG) \
		--tmp_dir "dist" \
		--build.log "" \
		--build.cmd "$(GO) run $(TEMPL_PKG) generate -lazy" \
		--build.bin "/usr/bin/true" \
		--build.delay 1000 \
		--build.include_dir "components,internal" \
		--build.include_ext "templ" \
		--build.exclude_regex ".*_templ.go" \
		--build.kill_delay "0s" \
		--build.send_interrupt "false" \
		--build.stop_on_error "true" \
		--misc.clean_on_exit false

# Watch the docs/src folder and rebuild the documentation
# on changes.
.PHONY: watch/docs
watch/docs:
	$(GO) run $(AIR_PKG) \
		--tmp_dir "dist" \
		--build.log "" \
		--build.cmd "${MAKE} docs-build" \
		--build.bin "/usr/bin/true" \
		--build.exclude_dir "" \
		--build.include_file "CHANGELOG.md" \
		--build.include_dir "docs/src/en,docs/translations,docs/api" \
		--build.include_ext "md,png,po,svg,json,yaml" \
		--build.delay 100

# Starts the watcher on the web folder.
.PHONY: watch/web
watch/web:
	@$(MAKE) -C web watch


# -------------------------------------------------------------------
# Release targets
# -------------------------------------------------------------------

# Unless you plan to build a full release, you don't need to use
# these targets.

# Build readeck for production use (linux amd64 v3 only, no SQLite)
.PHONY: build-prod
build-prod: CGO_ENABLED=0
build-prod: LDFLAGS=-s -w
build-prod: GOOS=linux
build-prod: GOARCH=amd64
build-prod: export GOAMD64=v3
build-prod: BUILD_TAGS=netgo osusergo nosqlite
build-prod: OUTFILE_NAME=readeck-$(VERSION)-$(GOOS)-$(GOARCH)-prod
build-prod: build

# Builds Readeck using xgo.
.PHONY: xbuild
xbuild: | $(DIST)/.generate
	@echo "CC: $(CC)"
	@echo "CGO_ENABLED": $$CGO_ENABLED
	@echo "CGO_CFLAGS": $$CGO_CFLAGS
	@echo "XGO_FLAGS": $(XGO_FLAGS)
	@echo "LDFLAGS": $(LDFLAGS)

	xgo -v \
		-tags "$(BUILD_TAGS)" \
		-targets "$(XGO_TARGET)" \
		-ldflags "$(VERSION_FLAGS) $(LDFLAGS)" \
		$(XGO_FLAGS) \
		-out readeck-$(VERSION) \
		./

.PHONY: xbuild-linux
xbuild-linux: CC=
xbuild-linux: CGO_ENABLED=1
xbuild-linux: LDFLAGS=-linkmode external -extldflags "-static" -s -w
xbuild-linux: XGO_FLAGS=
xbuild-linux: XGO_TARGET="linux/amd64,linux/386,linux/arm,linux/arm64"
xbuild-linux: xbuild
	touch $(DIST)/.xbuild-linux

.PHONY: xbuild-windows
xbuild-windows: CC=
xbuild-windows: CGO_ENABLED=1
xbuild-windows: LDFLAGS=-linkmode external -extldflags "-static" -s -w
xbuild-windows: XGO_FLAGS=-buildmode exe
xbuild-windows: XGO_TARGET="windows/amd64,windows/386"
xbuild-windows: xbuild
	touch $(DIST)/.xbuild-windows

.PHONY: xbuild-darwin
xbuild-darwin: CC=
xbuild-darwin: CGO_ENABLED=1
xbuild-darwin: LDFLAGS=-s -w
xbuild-darwin: XGO_FLAGS=
xbuild-darwin: XGO_TARGET="darwin-10.12/amd64,darwin-10.12/arm64"
xbuild-darwin: xbuild
	touch $(DIST)/.xbuild-darwin

.PHONY: xbuild-freebsd
xbuild-freebsd: CC=
xbuild-freebsd: CGO_ENABLED=1
xbuild-freebsd: LDFLAGS=-linkmode external -extldflags "-static" -s -w
xbuild-freebsd: XGO_FLAGS=
xbuild-freebsd: XGO_TARGET="freebsd/amd64"
xbuild-freebsd: xbuild
	touch $(DIST)/.xbuild-freebsd


.PHONY: stamp-version
stamp-version:
	echo $(VERSION) > $(DIST)/VERSION

.PHONY: release
release:
	cp CHANGELOG.md $(DIST)
	${MAKE} stamp-version
	${MAKE} release-checksums
	${MAKE} release-container

.PHONY: release-checksums
release-checksums:
	rm -rf $(DIST)/*.sha256
	cd $(DIST)/; for file in `find . -type f -name "readeck-*"`; do echo "checksumming $${file}" && sha256sum -b `echo $${file} | sed 's/^..//'` > $${file}.sha256; done;

.PHONY: release-container
release-container: TAG?=readeck-release:$(VERSION)
release-container: | $(DIST)/.xbuild-linux
	./tools/build-container $(VERSION) $(DIST)/container-$(VERSION).tar --rm
