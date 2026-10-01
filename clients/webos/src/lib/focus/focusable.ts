import { focusManager } from './manager';

interface Options {
  autofocus?: boolean;
  onFocus?: () => void;
  /** Holding OK on this element calls this instead of clicking it (a short
   *  press still clicks). See FocusManager's long-press handling. */
  onLongPress?: () => void;
  /** A held OK goes on clicking this element (once per auto-repeat) instead
   *  of counting as one press: the on-screen keyboard's delete key. Every
   *  other element gets one click per press, whatever the hold. See
   *  repeatActivates in ./hold. */
  repeatOk?: boolean;
}

// Kept as an attribute so the focus manager reads it off the element.
function setRepeatOk(node: HTMLElement, on: boolean | undefined) {
  if (on) node.setAttribute('data-repeat-ok', 'true');
  else node.removeAttribute('data-repeat-ok');
}

export function focusable(node: HTMLElement, opts: Options = {}) {
  node.setAttribute('data-focusable', 'true');
  node.setAttribute('data-focused', 'false');
  node.setAttribute('tabindex', '-1');

  const observer = new MutationObserver(() => {
    if (node.getAttribute('data-focused') === 'true') opts.onFocus?.();
  });
  observer.observe(node, { attributes: true, attributeFilter: ['data-focused'] });

  if (opts.autofocus) {
    queueMicrotask(() => focusManager.focus(node));
  }
  focusManager.setLongPress(node, opts.onLongPress);
  setRepeatOk(node, opts.repeatOk);

  return {
    update(next: Options = {}) {
      opts = next;
      focusManager.setLongPress(node, next.onLongPress);
      setRepeatOk(node, next.repeatOk);
    },
    destroy() {
      focusManager.setLongPress(node, undefined);
      setRepeatOk(node, false);
      observer.disconnect();
      node.removeAttribute('data-focusable');
      node.removeAttribute('data-focused');
    }
  };
}

export function focusScope(node: HTMLElement) {
  node.setAttribute('data-focus-scope', 'true');
  return {
    destroy() {
      node.removeAttribute('data-focus-scope');
    }
  };
}
