import { describe, expect, it, vi } from 'vitest';
import { ASSET_TOKEN_BOOT_WAIT_MS, assetTokenStale, ensureFreshAssetToken, type AssetTokenDeps } from './assetToken';

const HOUR = 60 * 60_000;
const NOW = 1_000 * HOUR;

function deps(over: Partial<AssetTokenDeps> = {}): AssetTokenDeps {
  return {
    accessToken: () => 'access',
    assetToken: () => 'asset',
    issuedAt: () => NOW - HOUR,
    now: () => NOW,
    refresh: vi.fn(async () => 'ok' as const),
    ...over,
  };
}

describe('assetTokenStale', () => {
  it('is fresh within its 24 h, short of the last hour', () => {
    expect(assetTokenStale(deps({ issuedAt: () => NOW - 22 * HOUR }))).toBe(false);
  });

  // The Q80B: a sign-in from days before, by a build that didn't record when.
  it('is stale near or past its expiry, and when its age is unknown', () => {
    expect(assetTokenStale(deps({ issuedAt: () => NOW - 23.5 * HOUR }))).toBe(true);
    expect(assetTokenStale(deps({ issuedAt: () => NOW - 72 * HOUR }))).toBe(true);
    expect(assetTokenStale(deps({ issuedAt: () => null }))).toBe(true);
  });

  it('is never stale signed out, or on a server that issues no asset token', () => {
    expect(assetTokenStale(deps({ accessToken: () => null, issuedAt: () => null }))).toBe(false);
    expect(assetTokenStale(deps({ assetToken: () => null, issuedAt: () => null }))).toBe(false);
  });
});

describe('ensureFreshAssetToken', () => {
  it('renews a stale token and waits for it', async () => {
    const d = deps({ issuedAt: () => null });
    await expect(ensureFreshAssetToken(d)).resolves.toBe(true);
    expect(d.refresh).toHaveBeenCalledTimes(1);
  });

  it('leaves a fresh token alone', async () => {
    const d = deps();
    await expect(ensureFreshAssetToken(d)).resolves.toBe(false);
    expect(d.refresh).not.toHaveBeenCalled();
  });

  it('stops waiting after the cap (offline), and never rejects', async () => {
    let fire: () => void = () => {};
    const timer = vi.fn((fn: () => void) => (fire = fn));
    const d = deps({ issuedAt: () => null, refresh: () => new Promise(() => {}) });
    const p = ensureFreshAssetToken(d, ASSET_TOKEN_BOOT_WAIT_MS, timer);
    expect(timer).toHaveBeenCalledWith(expect.any(Function), ASSET_TOKEN_BOOT_WAIT_MS);
    fire();
    await expect(p).resolves.toBe(true);
    const failing = deps({ issuedAt: () => null, refresh: () => Promise.reject(new Error('offline')) });
    await expect(ensureFreshAssetToken(failing)).resolves.toBe(true);
  });
});
