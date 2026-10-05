// Small ordering helpers for pages that fire overlapping requests. Pure (the
// requests are injected), so the ordering rules are unit-tested.

/** See latestOnly. */
export interface LatestOnly {
  <T>(request: Promise<T>, apply: (value: T) => void): Promise<boolean>;
  /**
   * Makes every request in flight stale without starting a new one. For an
   * optimistic write: the check mark set on screen is newer than any read
   * already on its way, which would otherwise land and take it back.
   */
  supersede(): void;
}

/**
 * Applies only the newest request's answer. Each call is numbered; when an
 * older request settles after a newer one was started, its value is dropped.
 * The item page needs this: the return-from-player re-read (1.5 s after Back)
 * and a "Mark watched" refresh both re-read the item, and an older GET that
 * lands last would otherwise flip the page back to the state before the mark.
 *
 * The returned function resolves true when `apply` ran and false when the
 * request was stale. Failures follow the same rule: only the newest
 * request's rejection reaches the caller, and a stale one resolves false. An
 * older season read timing out after the user picked another season must not
 * run the caller's "couldn't load" path and blank the list that is showing.
 */
export function latestOnly(): LatestOnly {
  let seq = 0;
  const run = async <T>(request: Promise<T>, apply: (value: T) => void): Promise<boolean> => {
    const mine = ++seq;
    let value: T;
    try {
      value = await request;
    } catch (e) {
      if (mine !== seq) return false;
      throw e;
    }
    if (mine !== seq) return false;
    apply(value);
    return true;
  };
  return Object.assign(run, {
    supersede() {
      seq++;
    },
  });
}

/**
 * Runs `task` one at a time, coalescing calls made while it runs into a
 * single follow-up run. A settings row that saves on every press (cycling a
 * language with the remote) then sends its saves in order, and the last one
 * carries the final value: two overlapping PUTs could otherwise reach the
 * server in the wrong order and store the older choice.
 *
 * The returned function resolves once the run that covers this call has
 * settled. A failed run doesn't stop a queued follow-up; the error is handed
 * to `onError` (the task should handle its own errors where it can).
 */
export function coalesce(task: () => Promise<void>, onError: (e: unknown) => void = () => {}): () => Promise<void> {
  let running: Promise<void> | null = null;
  let again = false;

  const loop = async () => {
    do {
      again = false;
      try {
        await task();
      } catch (e) {
        onError(e);
      }
    } while (again);
    running = null;
  };

  return () => {
    if (running) {
      again = true;
      return running;
    }
    running = loop();
    return running;
  };
}
