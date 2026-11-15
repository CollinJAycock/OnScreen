// `use:lazySrc={url}` on an <img loading="lazy">: the image loads when it
// nears the screen, also where the engine ignores loading="lazy".
//
// loading="lazy" needs Chromium 77. Tizen 5.5 (Chromium 69) and 6.0 (76),
// both inside config.xml's required_version, ignore it and fetched every
// poster of every row and grid page at once. There an IntersectionObserver
// (Chromium 51) sets the src instead, a little ahead of the screen edge;
// with neither, the src is set at once, as before.

/** How the image's src is set: by the engine ('native': loading="lazy"
 *  works), when it nears the screen ('observe'), or at once ('eager'). */
export type LazyMode = 'native' | 'observe' | 'eager';

export function lazyMode(g: { HTMLImageElement?: unknown; IntersectionObserver?: unknown } = globalThis): LazyMode {
  const img = g.HTMLImageElement as { prototype?: object } | undefined;
  if (img?.prototype && 'loading' in img.prototype) return 'native';
  if (typeof g.IntersectionObserver === 'function') return 'observe';
  return 'eager';
}

/** How far outside the screen an image starts loading. */
export const LAZY_MARGIN = '600px';

type Pending = { src: string };
let observer: IntersectionObserver | null = null;
const waiting = new WeakMap<Element, Pending>();

function shared(): IntersectionObserver {
  if (!observer) {
    observer = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (!e.isIntersecting) continue;
          const pending = waiting.get(e.target);
          observer?.unobserve(e.target);
          waiting.delete(e.target);
          if (pending?.src) (e.target as HTMLImageElement).src = pending.src;
        }
      },
      { rootMargin: LAZY_MARGIN },
    );
  }
  return observer;
}

/** The Svelte action. */
export function lazySrc(node: HTMLImageElement, src: string) {
  const mode = lazyMode();
  const set = (url: string) => {
    if (mode !== 'observe') {
      node.src = url;
      return;
    }
    const pending = waiting.get(node);
    if (pending) {
      // Not near the screen yet: the newest URL is the one it will load.
      pending.src = url;
      return;
    }
    if (node.getAttribute('src') !== null) {
      // Already shown: a new URL (a retry) loads at once.
      node.src = url;
      return;
    }
    waiting.set(node, { src: url });
    shared().observe(node);
  };
  set(src);
  return {
    update(next: string) {
      set(next);
    },
    destroy() {
      if (waiting.has(node)) {
        waiting.delete(node);
        observer?.unobserve(node);
      }
    },
  };
}

/** Test hook: forget the shared observer. */
export function resetLazyObserver(): void {
  observer?.disconnect();
  observer = null;
}
