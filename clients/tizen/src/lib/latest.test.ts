import { describe, expect, it } from 'vitest';
import { coalesce, latestOnly } from './latest';

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

// Lets every pending promise callback run.
const flush = () => new Promise<void>((r) => setTimeout(r, 0));

describe('latestOnly', () => {
  it('drops an older answer that lands after a newer one', async () => {
    const run = latestOnly();
    const applied: string[] = [];
    const older = deferred<string>();
    const newer = deferred<string>();
    // The return-from-player read goes out first, the mark's refresh second.
    const a = run(older.promise, (v) => applied.push(v));
    const b = run(newer.promise, (v) => applied.push(v));
    newer.resolve('watched');
    expect(await b).toBe(true);
    older.resolve('unwatched');
    expect(await a).toBe(false);
    expect(applied).toEqual(['watched']);
  });

  it('applies answers that land in order', async () => {
    const run = latestOnly();
    const applied: number[] = [];
    expect(await run(Promise.resolve(1), (v) => applied.push(v))).toBe(true);
    expect(await run(Promise.resolve(2), (v) => applied.push(v))).toBe(true);
    expect(applied).toEqual([1, 2]);
  });

  it('does not let an older answer win after the newest one failed', async () => {
    const run = latestOnly();
    const applied: string[] = [];
    const older = deferred<string>();
    const newer = deferred<string>();
    const a = run(older.promise, (v) => applied.push(v));
    const b = run(newer.promise, (v) => applied.push(v));
    newer.reject(new Error('offline'));
    await expect(b).rejects.toThrow('offline');
    older.resolve('stale');
    expect(await a).toBe(false);
    expect(applied).toEqual([]);
  });

  it('resolves false for an older failure once a newer request was started', async () => {
    const run = latestOnly();
    const applied: string[] = [];
    const older = deferred<string>();
    const newer = deferred<string>();
    // Season 2's read goes out, then Season 3's.
    const a = run(older.promise, (v) => applied.push(v));
    const b = run(newer.promise, (v) => applied.push(v));
    older.reject(new Error('timeout'));
    // Not thrown: the caller's failure path (empty the list) must not run.
    expect(await a).toBe(false);
    newer.resolve('season 3');
    expect(await b).toBe(true);
    expect(applied).toEqual(['season 3']);
  });

  it('resolves false for an older failure that lands after the newer answer', async () => {
    const run = latestOnly();
    const older = deferred<string>();
    const a = run(older.promise, () => {});
    expect(await run(Promise.resolve('fresh'), () => {})).toBe(true);
    older.reject(new Error('late'));
    expect(await a).toBe(false);
  });

  it('supersede drops the reads in flight and leaves later ones alone', async () => {
    const run = latestOnly();
    const applied: string[] = [];
    const inflight = deferred<string>();
    const a = run(inflight.promise, (v) => applied.push(v));
    // An optimistic check mark is set on screen.
    run.supersede();
    inflight.resolve('before the mark');
    expect(await a).toBe(false);
    // The refresh after the write still applies.
    expect(await run(Promise.resolve('after the mark'), (v) => applied.push(v))).toBe(true);
    expect(applied).toEqual(['after the mark']);
  });

  it('supersede also swallows the dropped read failing', async () => {
    const run = latestOnly();
    const inflight = deferred<string>();
    const a = run(inflight.promise, () => {});
    run.supersede();
    inflight.reject(new Error('offline'));
    expect(await a).toBe(false);
  });

  it('keeps separate sequences apart', async () => {
    const items = latestOnly();
    const children = latestOnly();
    const slow = deferred<string>();
    const applied: string[] = [];
    const a = items(slow.promise, (v) => applied.push(v));
    // A children read doesn't supersede the item read.
    await children(Promise.resolve('kids'), (v) => applied.push(v));
    slow.resolve('item');
    expect(await a).toBe(true);
    expect(applied).toEqual(['kids', 'item']);
  });
});

describe('coalesce', () => {
  it('runs once per idle call', async () => {
    let runs = 0;
    const save = coalesce(async () => {
      runs++;
    });
    await save();
    await save();
    expect(runs).toBe(2);
  });

  it('folds calls made during a run into one follow-up run', async () => {
    const gates = [deferred<void>(), deferred<void>()];
    const seen: string[] = [];
    let value = 'eng';
    let run = 0;
    const save = coalesce(async () => {
      seen.push(value);
      await gates[run++].promise;
    });
    const first = save();
    value = 'spa';
    const second = save();
    value = 'fra';
    const third = save();
    // Still one PUT in flight.
    expect(seen).toEqual(['eng']);
    gates[0].resolve();
    await flush();
    // The follow-up carries the latest value; the middle one never went out.
    expect(seen).toEqual(['eng', 'fra']);
    gates[1].resolve();
    await Promise.all([first, second, third]);
    expect(seen).toEqual(['eng', 'fra']);
  });

  it('still runs the follow-up after a failed run', async () => {
    const errors: unknown[] = [];
    let n = 0;
    const gate = deferred<void>();
    const save = coalesce(
      async () => {
        n++;
        if (n === 1) {
          await gate.promise;
          throw new Error('boom');
        }
      },
      (e) => errors.push(e),
    );
    const first = save();
    const second = save();
    gate.resolve();
    await Promise.all([first, second]);
    expect(n).toBe(2);
    expect(errors).toHaveLength(1);
  });
});
