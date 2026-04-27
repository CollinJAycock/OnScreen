# Dependabot triage — 2026-04-27

10 PRs queued at v2.0 tag time. Sorted by merge order so chained
deps land in the right sequence.

Run `gh auth login` once before any of the merge commands below.
The merges use `--squash --auto` so each PR waits for green checks
before going in — safe to fire in batches.

---

## Group A — Ship now (mechanical, low risk)

These bump GitHub Action major versions that already exist in the
runner registry and don't change action input shapes used by our
workflows.

| PR | Bump | Why safe |
|----|------|---------|
| [#1](https://github.com/CollinJAycock/OnScreen/pull/1) | `actions/setup-node` v4 → v6 | Same `node-version` / `cache` inputs we use; no breaking changes for non-pnpm consumers. |
| [#2](https://github.com/CollinJAycock/OnScreen/pull/2) | `golangci/golangci-lint-action` v8 → v9 | Action wraps the same `golangci-lint` binary; v9's only break is the deprecation of the `working-directory` input which we don't pass. |
| [#4](https://github.com/CollinJAycock/OnScreen/pull/4) | `docker/setup-buildx-action` v3 → v4 | Used to install QEMU + buildx for the multi-arch image build; v4 just bumps the Buildx CLI floor. |
| [#7](https://github.com/CollinJAycock/OnScreen/pull/7) | npm minor/patch (vitest 4.1.4 → 4.1.5) | Patch bump, bug fixes only. |

```bash
for n in 1 2 4 7; do
  gh pr merge $n --squash --auto --delete-branch
done
```

---

## Group B — Ship after a one-shot build+test verify (medium risk)

These touch compile-time deps with multi-minor jumps. None of them
should break, but the safe path is a quick CI run before merge.

### PR [#12](https://github.com/CollinJAycock/OnScreen/pull/12) — Go module aggregate (11 packages)

Notable jumps (all minor/patch, no API breaks documented):

- `pgx/v5` 5.7.4 → 5.9.2 — adds `Conn.PgConn().LingerTime()`; we don't use the new surface
- `goose` 3.24.1 → 3.27.1 — migration runner; protocol unchanged
- `prometheus/client_golang` 1.20 → 1.23 — additive metric helpers
- `redis/go-redis/v9` 9.7.3 → 9.18.0 — biggest jump but compat-stable; we use Valkey via the client basics

```bash
gh pr checkout 12
go build ./... && go test ./... && cd web && npm run test:unit && cd ..
gh pr merge 12 --squash --auto --delete-branch
git checkout main && git pull
```

### PR [#5](https://github.com/CollinJAycock/OnScreen/pull/5) — Node 24-alpine → 25-alpine (Dockerfile)

Affects `docker/Dockerfile` only. Node 25 is current. Risk is the bundled npm version — sometimes major Node minors flip the engines floor for indirect deps.

```bash
gh pr checkout 5
docker build -f docker/Dockerfile -t onscreen:node25-test .
gh pr merge 5 --squash --auto --delete-branch
```

### PR [#6](https://github.com/CollinJAycock/OnScreen/pull/6) — CUDA 12.8.0-runtime → 13.2.1-runtime (Dockerfile.gpu base)

The roadmap flagged this as the one bump that may need an FFmpeg-image rebuild check. CUDA 13 is a major version — check that the FFmpeg base image (`Dockerfile.ffmpeg`) builds against it before merging. If FFmpeg is pinned to a CUDA-12 base, this PR has to wait.

```bash
gh pr checkout 6
# Verify: does Dockerfile.ffmpeg's nvidia base track 12.x specifically?
grep -i nvidia docker/Dockerfile.ffmpeg
# If it does, rebuild that one first against the 13.2.1 base (or skip this PR)
docker build -f docker/Dockerfile.ffmpeg -t onscreen-ffmpeg:cuda13-test .
docker build -f docker/Dockerfile.gpu -t onscreen:gpu-cuda13-test .
```

If the FFmpeg image has its own CUDA pin, **don't merge #6** until that pin moves first — `make truenas-deploy` will pull two layers with different CUDA majors and the runtime will refuse the GPU.

---

## Group C — Coordinated major-version chain (high risk)

These three are interlocked and have to ship together or in the
declared order.

### Chain: Vite 8 → vite-plugin-svelte 7

PR [#9](https://github.com/CollinJAycock/OnScreen/pull/9) (vite-plugin-svelte 4 → 7) explicitly requires Vite 8 (PR [#8](https://github.com/CollinJAycock/OnScreen/pull/8)) and Svelte 5.46.4+. Merging #9 alone breaks the build.

**Order**:
1. Bump Svelte to ≥5.46.4 first (check `web/package-lock.json` — `^5.0.0` may already resolve high enough, but Dependabot didn't open a separate PR for it)
2. Merge #8 (Vite 5 → 8)
3. Merge #9 (vite-plugin-svelte 4 → 7)

Each major in the chain has breaking changes worth reviewing:

- **Vite 8**: drops Node 18; renamed `server.warmup` → `warmup`; CSS modules now strict-export by default. Our config uses none of these.
- **vite-plugin-svelte 7**: removes deprecated options, integrates vite-plugin-svelte-inspector inline (drop the separate `@sveltejs/vite-plugin-svelte-inspector` dep if we have it).

```bash
# Branch off main locally and stack the chain
git checkout -b deps/vite-svelte-chain
gh pr checkout 8 && git rebase main
gh pr checkout 9 && git rebase main
# Test
cd web && npm install && npm run check && npm run build && cd ..
# Merge in order via gh
gh pr merge 8 --squash --delete-branch
gh pr merge 9 --squash --delete-branch
```

### PR [#10](https://github.com/CollinJAycock/OnScreen/pull/10) — TypeScript 5.9 → 6.0

Independent of the Vite chain. TypeScript 6 deprecates a handful of compiler options but core language behavior is unchanged from 5.x. svelte-check is the most likely friction point — its own peer-dep range may not yet allow TS 6.

```bash
gh pr checkout 10
cd web && npm install && npm run check
# If svelte-check chokes on the peer range, hold this PR until svelte-check
# adds TS 6 support (usually within a week of a TS major).
```

---

## Estimated time to clear queue

| Group | PRs | Hands-on time |
|------|-----|--------------|
| A — mechanical | 4 | 5 min (one loop, watch checks) |
| B — verify-then-merge | 3 | ~30 min total (one Docker build is the long pole) |
| C — chained majors | 3 | 1–2 h (real testing + handle either chain breaking) |

Total: ~2.5 h to fully clear if everything passes; longer if Vite 8 surfaces a config break or CUDA 13 needs the FFmpeg image rebuilt.
