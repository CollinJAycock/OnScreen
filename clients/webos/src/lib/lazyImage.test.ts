import { afterEach, describe, expect, it, vi } from 'vitest';
import { lazyMode, lazySrc, resetLazyObserver } from './lazyImage';

/** An <img> as far as the action touches it. */
function img() {
  const attrs = new Map<string, string>();
  return {
    get src() {
      return attrs.get('src') ?? '';
    },
    set src(v: string) {
      attrs.set('src', v);
    },
    getAttribute: (k: string) => attrs.get(k) ?? null,
  } as unknown as HTMLImageElement;
}

/** IntersectionObserver that intersects when told. */
class FakeObserver {
  static last: FakeObserver | null = null;
  observed = new Set<Element>();
  constructor(private readonly cb: (entries: Array<{ isIntersecting: boolean; target: Element }>) => void) {
    FakeObserver.last = this;
  }
  observe(el: Element) {
    this.observed.add(el);
  }
  unobserve(el: Element) {
    this.observed.delete(el);
  }
  disconnect() {
    this.observed.clear();
  }
  show(el: Element) {
    this.cb([{ isIntersecting: true, target: el }]);
  }
}

afterEach(() => {
  resetLazyObserver();
  vi.unstubAllGlobals();
});

describe('lazyMode', () => {
  it('leaves it to the engine where loading="lazy" works (Chromium 77+)', () => {
    class Img {}
    Object.defineProperty(Img.prototype, 'loading', { value: 'auto' });
    expect(lazyMode({ HTMLImageElement: Img, IntersectionObserver: FakeObserver })).toBe('native');
  });

  it('observes on Tizen 5.5 / 6.0 (Chromium 69 / 76), and loads at once with neither', () => {
    class Img {}
    expect(lazyMode({ HTMLImageElement: Img, IntersectionObserver: FakeObserver })).toBe('observe');
    expect(lazyMode({ HTMLImageElement: Img })).toBe('eager');
  });
});

describe('lazySrc', () => {
  it('without loading="lazy", sets the src only once the image nears the screen', () => {
    vi.stubGlobal('HTMLImageElement', class {});
    vi.stubGlobal('IntersectionObserver', FakeObserver);
    const el = img();
    const action = lazySrc(el, '/a.jpg');
    expect(el.getAttribute('src')).toBeNull();
    // A retry's new URL before it shows: the newest one loads.
    action.update('/a2.jpg');
    FakeObserver.last!.show(el as unknown as Element);
    expect(el.src).toBe('/a2.jpg');
    expect(FakeObserver.last!.observed.size).toBe(0);
    // Shown already: a new URL loads at once.
    action.update('/b.jpg');
    expect(el.src).toBe('/b.jpg');
  });

  it('stops watching an image that goes away unseen', () => {
    vi.stubGlobal('HTMLImageElement', class {});
    vi.stubGlobal('IntersectionObserver', FakeObserver);
    const el = img();
    lazySrc(el, '/a.jpg').destroy();
    expect(FakeObserver.last!.observed.size).toBe(0);
  });

  it('sets the src at once where the engine does the lazy loading', () => {
    class Img {}
    Object.defineProperty(Img.prototype, 'loading', { value: 'auto' });
    vi.stubGlobal('HTMLImageElement', Img);
    const el = img();
    lazySrc(el, '/a.jpg');
    expect(el.src).toBe('/a.jpg');
  });
});
