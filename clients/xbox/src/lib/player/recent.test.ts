import { describe, expect, it, vi } from 'vitest';
import { RecentRequests } from './recent';

describe('RecentRequests', () => {
  it("shares one answer for the album's tracks, then asks again after the TTL", async () => {
    let clock = 0;
    const memo = new RecentRequests<string>(1000, 16, () => clock);
    const load = vi.fn(async () => 'album');
    expect(await memo.get('u1:album', load)).toBe('album');
    clock = 999;
    expect(await memo.get('u1:album', load)).toBe('album');
    expect(load).toHaveBeenCalledTimes(1);
    clock = 1000;
    await memo.get('u1:album', load);
    expect(load).toHaveBeenCalledTimes(2);
  });

  it('keeps no failure', async () => {
    const memo = new RecentRequests<string>();
    const fail = vi.fn(async () => {
      throw new Error('offline');
    });
    await expect(memo.get('k', fail)).rejects.toThrow('offline');
    await Promise.resolve();
    const ok = vi.fn(async () => 'v');
    expect(await memo.get('k', ok)).toBe('v');
    expect(ok).toHaveBeenCalledTimes(1);
  });

  it('keys apart (another account asks for itself) and holds a few', async () => {
    const memo = new RecentRequests<string>(60_000, 2);
    const load = vi.fn(async () => 'x');
    await memo.get('u1:a', load);
    await memo.get('u2:a', load);
    expect(load).toHaveBeenCalledTimes(2);
    await memo.get('u1:b', load);
    // u1:a was the oldest of three: gone.
    await memo.get('u1:a', load);
    expect(load).toHaveBeenCalledTimes(4);
  });
});
