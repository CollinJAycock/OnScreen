BINARY       := onscreen
MODULE       := github.com/onscreen/onscreen
GO           := go
GOFLAGS      :=
BUILD_DIR    := bin
CMD_SERVER   := ./cmd/server
CMD_WORKER   := ./cmd/worker

# Version info injected at build time
VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME   := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS      := -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)

.PHONY: all build build-server build-worker frontend generate migrate test-unit test-int test-e2e test-browser test-browser-install lint fmt coverage docker docker-up docker-down check clean dev help client-deps client-check client-dev client-build installer-windows

## all: build everything (frontend + server + worker)
all: build

## build: build frontend then server and worker binaries
build: frontend
	mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/server $(CMD_SERVER)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/worker $(CMD_WORKER)

## build-server: build server binary only (syncs frontend if web/dist exists)
build-server:
	@if [ -d web/dist ]; then rm -rf internal/webui/dist && cp -r web/dist internal/webui/dist; fi
	mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/server $(CMD_SERVER)

## build-worker: build worker binary only
build-worker:
	mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/worker $(CMD_WORKER)

## installer-windows: build the portable Windows test zip (dist/onscreen-windows-amd64-<ver>.zip)
installer-windows:
	powershell -NoProfile -ExecutionPolicy Bypass -File installer/windows/build.ps1

## installer-windows-msi: build the all-in-one Windows installer .exe (dist/OnScreen-Setup-<ver>.exe)
installer-windows-msi:
	powershell -NoProfile -ExecutionPolicy Bypass -File installer/windows-msi/build.ps1

## frontend: build SvelteKit SPA and sync into Go embed directory
frontend:
	cd web && npm ci && npm run build
	rm -rf internal/webui/dist
	cp -r web/dist internal/webui/dist

## generate: run sqlc code generation
generate:
	sqlc generate

## migrate: run pending goose migrations (requires DATABASE_URL)
migrate:
	goose -dir internal/db/migrations postgres "$(DATABASE_URL)" up

## migrate-status: show migration status
migrate-status:
	goose -dir internal/db/migrations postgres "$(DATABASE_URL)" status

## test-unit: run unit tests only (no external deps, <10s)
test-unit:
	$(GO) test -tags dev -count=1 -short ./internal/...

## test-int: run integration tests (testcontainers-go, requires Docker, <2min)
test-int:
	$(GO) test -tags dev -count=1 -run Integration ./cmd/... ./internal/... ./test/...

## test-e2e: run full stack tests via docker-compose (<5min)
test-e2e:
	docker compose -f docker/docker-compose.yml up -d --wait
	$(GO) test -tags e2e -count=1 -run E2E ./test/e2e/... ; ret=$$?; docker compose -f docker/docker-compose.yml down; exit $$ret

## test-browser: run Playwright browser-driven specs from web/tests/e2e
## Requires a running OnScreen server reachable at $$BASE_URL (default
## http://localhost:7070) plus E2E_USERNAME / E2E_PASSWORD for the auth
## paths and E2E_GAPLESS_ALBUM for the gapless rollover spec. First
## run only: `make test-browser-install` to download browser engines.
test-browser:
	cd web && npx playwright test

test-browser-install:
	cd web && npx playwright install --with-deps

## lint: run golangci-lint
lint:
	golangci-lint run ./cmd/... ./internal/...

## dev: start Vite dev server + Go server in dev mode side-by-side
## Override any var on the command line: make dev MEDIA_PATH=/your/media
DATABASE_URL     ?= postgres://onscreen:onscreen@localhost:5432/onscreen?sslmode=disable
VALKEY_URL       ?= redis://localhost:6379
MEDIA_PATH       ?= /tmp/onscreen-media
SECRET_KEY       ?= dev-secret-key-change-in-production-32b
dev:
	mkdir -p $(MEDIA_PATH)
	DATABASE_URL=$(DATABASE_URL) \
	VALKEY_URL=$(VALKEY_URL) \
	MEDIA_PATH=$(MEDIA_PATH) \
	SECRET_KEY=$(SECRET_KEY) \
	DEV_FRONTEND_URL=http://localhost:5173 \
	$(GO) run -tags dev $(CMD_SERVER) & GO_PID=$$!; \
	trap "kill $$GO_PID 2>/dev/null" EXIT; \
	cd web && npm run dev; \
	kill $$GO_PID 2>/dev/null

## docker: build Docker image
docker:
	docker build -f docker/Dockerfile -t onscreen .

## docker-up: start all services via docker-compose
docker-up:
	docker compose -f docker/docker-compose.yml up -d

## docker-down: stop all services
docker-down:
	docker compose -f docker/docker-compose.yml down

## coverage: generate test coverage report
coverage:
	$(GO) test -tags dev -count=1 -coverprofile=coverage.out ./internal/...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## fmt: format Go and frontend code
fmt:
	goimports -w .
	cd web && npx prettier --write .

## check: run lint + unit tests (pre-push convenience)
check: lint test-unit
	cd web && npm run check

## deploy: full build + migrate + restart (requires DATABASE_URL, server running on :7070)
deploy: frontend
	mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/server $(CMD_SERVER)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/worker $(CMD_WORKER)
	goose -dir internal/db/migrations postgres "$(DATABASE_URL)" up
	@echo "Build complete. Restart the server to pick up changes."

## clean: remove build artifacts
clean:
	rm -rf $(BUILD_DIR)
	rm -rf web/dist web/.svelte-kit internal/webui/dist
	rm -rf clients/desktop/src-tauri/target

# ── Desktop client (Tauri 2) ───────────────────────────────────────────────
# Build pipeline for clients/desktop. First run on a new machine
# needs `make client-deps` to install the Tauri CLI; subsequent
# `client-check` / `client-dev` / `client-build` calls assume it's
# present.

CLIENT_DESKTOP_DIR := clients/desktop/src-tauri

## client-deps: install the Tauri 2 CLI globally (one-time per dev box)
client-deps:
	cargo install tauri-cli --locked --version "^2.0"

## client-check: cargo check the desktop client (~30s after first cache fill)
## Fast smoke test that proves the Rust + cpal + claxon + ureq stack
## compiles without going through a full bundle. Run this first when
## you don't trust a Rust change.
client-check:
	cd $(CLIENT_DESKTOP_DIR) && cargo check

## client-dev: launch the desktop client pointing at the Vite dev server
## Starts Vite in the background (web/dev mode at :5173) and runs
## tauri dev so the webview hot-reloads on Svelte changes. Tauri config
## already points devUrl at :5173 — this target just orchestrates the
## two processes and tears Vite down on exit so you don't have to.
client-dev:
	cd web && npm run dev & VITE_PID=$$!; \
	trap "kill $$VITE_PID 2>/dev/null" EXIT; \
	cd $(CLIENT_DESKTOP_DIR) && cargo tauri dev; \
	kill $$VITE_PID 2>/dev/null

## client-build: produce the platform installer in target/release/bundle
## On first run after a clean checkout the cargo step downloads ~300+
## crates and takes 5-10 minutes; subsequent builds with a warm cache
## land in 30-90 seconds. Output paths printed by tauri at the end.
client-build: frontend
	cd $(CLIENT_DESKTOP_DIR) && cargo tauri build

## help: display this help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
