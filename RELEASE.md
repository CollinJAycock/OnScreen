# Release Status

## v2.0 — code freeze

**Frozen:** 2026-04-26
**Freeze HEAD:** `fe9cd21` (main at freeze time)
**Release branch:** `release/v2.0`

The v2.0 roadmap is feature-complete. Every item in [docs/v2-roadmap.md](docs/v2-roadmap.md) has shipped or is explicitly deferred to v2.1. The test suite is at its strongest of the project's life (87 UAT, 73 integration, fuzz tests for parsers, strong unit coverage on every previously-weak package). Three security passes are closed.

From this point until v2.0 is tagged, **`release/v2.0` only accepts:**

1. Bug fixes triaged out of the manual test plan ([docs/manual-test-plan.md](docs/manual-test-plan.md)) Tier 2 sweep and Tier 3 hardware validation.
2. Critical security fixes (anything that would otherwise gate the release).
3. Documentation corrections.

Everything else — new features, refactors, dependency bumps that aren't security-driven, performance polish — lands on `main` and waits for v2.1.

### Merge policy

- Bug fixes go to `release/v2.0` first, then cherry-pick to `main` (so we don't lose them in v2.1).
- New work goes to `main` only. `release/v2.0` is closed to features.
- The branch is fast-forward only until tagging: rebase on `release/v2.0` if your fix needs to land on top of another fix that landed first.

### Validation gates before tagging v2.0

In order:

1. **Tier 2 manual sweep (~90 min)** on staging — all checkboxes green or triaged.
2. **Tier 3 hardware encode validation** on the TrueNAS RTX 5000 box — every encoder family in the matrix verified or flagged as "not present on this hardware."
3. **Integration suite with Docker**: `go test -tags=integration ./internal/db/gen/...` (73 tests). Must pass.
4. **Beta soak**: at least 7 days of the staging deployment running on the freeze HEAD, exercised by the audiophile friend's normal usage. The endurance section of the manual test plan should also have run for at least one 8h session in this window.
5. **External pen-test or final security re-scan** — re-run the security probe checklist against the freeze HEAD.

When all five gates are green, tag from the tip of `release/v2.0`:

```bash
git tag -s v2.0.0 -m "v2.0.0"
git push origin v2.0.0
```

### Roadmap items deferred to v2.1

These are documented in the v2 roadmap as deliberate v2.1 targets, not as gaps:

- Books / comics as a media type
- Tidal / Qobuz integration
- Podcast RSS auto-fetch (out of scope per project memo — OnScreen doesn't download content)
- Hardware bit-perfect playback (lands with the native client phase)
- Native client apps (Windows/macOS/Linux/iOS/Android/TV)

### Where to find things

| Surface | Doc |
|---|---|
| Comparison vs Plex / Emby / Jellyfin | [docs/comparison-matrix.md](docs/comparison-matrix.md) |
| v2 roadmap (history of decisions) | [docs/v2-roadmap.md](docs/v2-roadmap.md) |
| Manual test plan + automated-coverage map | [docs/manual-test-plan.md](docs/manual-test-plan.md) |
| Deployment guide | [docs/deployment.md](docs/deployment.md) |
| Plugin authoring (MCP) | [docs/plugins.md](docs/plugins.md) |
