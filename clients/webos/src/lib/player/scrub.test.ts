import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PendingScrub } from './scrub';
import { SCRUB_COMMIT_MS, classifySeek } from './session';

describe('PendingScrub', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  function make(ready = () => true) {
    const commits: number[] = [];
    const seen: Array<number | null> = [];
    const s = new PendingScrub({
      commit: (t) => commits.push(t),
      ready,
      changed: (t) => seen.push(t),
    });
    return { s, commits, seen };
  }

  it('collapses a burst of presses into one commit at the summed target', () => {
    const { s, commits } = make();
    for (let i = 0; i < 6; i++) {
      s.nudge(10_000, 100_000, 3_600_000);
      vi.advanceTimersByTime(SCRUB_COMMIT_MS - 100);
    }
    expect(commits).toEqual([]);
    expect(s.value).toBe(160_000);
    vi.advanceTimersByTime(100);
    expect(commits).toEqual([160_000]);
    expect(s.value).toBeNull();
  });

  it('commits at once on flush (OK) and never twice', () => {
    const { s, commits } = make();
    s.nudge(-30_000, 100_000, 3_600_000);
    s.flush();
    vi.advanceTimersByTime(SCRUB_COMMIT_MS * 2);
    expect(commits).toEqual([70_000]);
  });

  it('drops the target on cancel (Back)', () => {
    const { s, commits, seen } = make();
    s.nudge(10_000, 0, 3_600_000);
    s.cancel();
    vi.advanceTimersByTime(SCRUB_COMMIT_MS * 2);
    expect(commits).toEqual([]);
    expect(seen).toEqual([10_000, null]);
  });

  it('holds a due target until the player is ready, then lands it on flush', () => {
    let ready = false;
    const { s, commits } = make(() => ready);
    s.nudge(10_000, 50_000, 3_600_000);
    vi.advanceTimersByTime(SCRUB_COMMIT_MS);
    expect(commits).toEqual([]);
    expect(s.value).toBe(60_000);
    ready = true;
    s.flush();
    expect(commits).toEqual([60_000]);
  });

  it('parks a target without a timer', () => {
    const { s, commits } = make(() => false);
    s.park(90_000, 60_000);
    expect(s.value).toBe(60_000); // clamped to the item
    expect(s.counting).toBe(false);
    vi.advanceTimersByTime(SCRUB_COMMIT_MS * 2);
    expect(commits).toEqual([]);
  });

  it('turns a held ← on a resumed session into exactly one re-issue', () => {
    // Resumed at 45:00 on a remux (offset 2695 s), 60 s produced. Ten
    // presses of ←10 s land before the session head: one re-issue at 43:25.
    const w = { offsetMs: 2_695_000, producedEndMs: 60_000, ended: false, durationMs: 7_200_000 };
    const reissues: number[] = [];
    const locals: number[] = [];
    const s = new PendingScrub({
      commit: (t) => {
        const plan = classifySeek(t, w);
        if (plan.kind === 'reissue') reissues.push(plan.positionMs);
        else locals.push(plan.streamSec);
      },
      ready: () => true,
      changed: () => {},
    });
    for (let i = 0; i < 10; i++) {
      s.nudge(-10_000, 2_705_000, w.durationMs);
      vi.advanceTimersByTime(120);
    }
    vi.advanceTimersByTime(SCRUB_COMMIT_MS);
    expect(reissues).toEqual([2_605_000]);
    expect(locals).toEqual([]);
  });
});
