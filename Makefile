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

.PHONY: all build build-server build-worker frontend generate migrate test-unit test-int test-e2e lint fmt coverage docker docker-up docker-down check clean dev help

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
	$(GO) test -tags dev -count=1 -run Integration ./...

## test-e2e: run full stack tests via docker-compose (<5min)
test-e2e:
	docker compose -f docker/docker-compose.yml up -d --wait
	$(GO) test -tags dev -count=1 -run E2E ./... ; ret=$$?; docker compose -f docker/docker-compose.yml down; exit $$ret

## lint: run golangci-lint
lint:
	golangci-lint run ./...

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

## help: display this help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
