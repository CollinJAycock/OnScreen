# Contributing to OnScreen

Thanks for your interest in contributing to OnScreen. This guide covers everything you need to get a development environment running and submit changes.

## Prerequisites

| Tool | Version | Notes |
|------|---------|-------|
| Go | 1.25+ | |
| Node.js | 24+ | For the SvelteKit frontend |
| PostgreSQL | 16+ | `pgvector` is optional — used for Phase 5 embedding work; migration skips it cleanly if unavailable |
| Valkey (or Redis) | 7+ | Sessions, job queue, rate limiting |
| FFmpeg | Latest stable | Required for transcoding and `ffprobe` |
| sqlc | Latest | SQL-to-Go code generation |
| goose | v3 | Database migrations |
| golangci-lint | Latest | Go linting |
| GitHub CLI (`gh`) | 2.50+ | Cuts the release form down to one command (`gh release create`), fetches PR comments inline (`gh api …`), drives Dependabot triage. Install: `winget install GitHub.cli` (Windows), `brew install gh` (macOS), [`docs.github.com/en/github-cli/github-cli/quickstart`](https://docs.github.com/en/github-cli/github-cli/quickstart) (Linux). Run `gh auth login` once after install. |

## Dev Setup

```bash
# 1. Fork and clone
git clone https://github.com/<your-fork>/onscreen.git
cd onscreen

# 2. Copy environment config
cp .env.example .env.dev
# Edit .env.dev with your local values (DATABASE_URL, MEDIA_PATH, etc.)

# 3. Start Postgres and Valkey
docker compose -f docker/docker-compose.yml up -d postgres valkey

# 4. Run migrations
make migrate DATABASE_URL="postgres://onscreen:onscreen@localhost:5432/onscreen?sslmode=disable"

# 5. Start dev servers (Go API on :7070, Vite on :5173)
make dev MEDIA_PATH=/path/to/your/media
```

Open `http://localhost:5173`, create your admin account, add a library, and scan.

Run `make help` to see all available targets.

## Project Structure

```
cmd/server/       API server entry point (embeds transcode worker)
cmd/worker/       Standalone transcode + maintenance worker
internal/
  api/            HTTP handlers, middleware, router
  db/gen/         sqlc-generated query code (do not edit by hand)
  db/migrations/  goose SQL migrations
  domain/         Business logic (library, media, settings, watchevent)
  scanner/        File discovery, hashing, ffprobe, TMDB enrichment
  transcode/      FFmpeg session management, HLS output
  metadata/tmdb/  TMDB API client
web/              SvelteKit frontend (TypeScript)
```

## Adding SQL Queries (sqlc)

OnScreen uses [sqlc](https://sqlc.dev/) to generate type-safe Go from raw SQL. The workflow:

1. Write your query in the appropriate `.sql` file under `internal/db/queries/`.
2. Add a `-- name: YourQueryName :one` (or `:many`, `:exec`, `:execresult`) annotation.
3. Run `make generate` to regenerate `internal/db/gen/`.
4. Never edit files in `internal/db/gen/` by hand -- they are overwritten on every generate.

If you need a schema change, add a new goose migration under `internal/db/migrations/`.

## Code Style

**Go:**
- Run `make lint` (`golangci-lint`) before submitting. CI will enforce this.
- Follow standard Go conventions: `gofmt`, short variable names in tight scopes, exported types documented.

**Frontend:**
- Run `npx svelte-check` from `web/` to catch type errors.
- Run `npx eslint .` from `web/` for lint issues.
- TypeScript strict mode is enabled.

## Branching and PRs

1. Fork the repository and create a feature branch from `main`:
   ```bash
   git checkout -b feature/your-change
   ```
2. Make your changes in small, focused commits.
3. Run tests before pushing:
   ```bash
   make test-unit       # fast unit tests (<10s, no external deps)
   make lint            # Go lint
   cd web && npx svelte-check  # frontend type check
   ```
4. Push to your fork and open a pull request against `main`.
5. Fill out the PR template. Describe *what* changed and *why*.

## Commit Conventions

Use clear, imperative-mood commit messages:

```
add webhook retry backoff
fix ffprobe timeout on large files
update scan pipeline to skip unchanged hashes
```

Prefix with the area when it helps clarity: `web: fix player seek`, `scanner: add music tag parsing`.

No need for Conventional Commits or any formal prefix scheme -- just be descriptive.

## Tests

- **Unit tests:** `make test-unit` -- fast, no Docker required. Always run these before submitting.
- **Integration tests:** `make test-int` -- uses testcontainers-go, requires Docker.
- **E2E tests:** `make test-e2e` -- full stack via docker-compose.

At minimum, PRs should not break existing unit tests. Adding tests for new behavior is appreciated.

## License

OnScreen is licensed under **AGPLv3**. By contributing, you agree that your contributions will be licensed under the same terms. See [LICENSE](LICENSE) for the full text.
