import { focusManager } from './manager';

interface Options {
  autofocus?: boolean;
  onFocus?: () => void;
  /** Holding OK on this element calls this instead of clicking it (a short
   *  press still clicks). See FocusManager's long-press handling. */
  onLongPress?: () => void;
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

  return {
    update(next: Options = {}) {
      opts = next;
      focusManager.setLongPress(node, next.onLongPress);
    },
    destroy() {
      focusManager.setLongPress(node, undefined);
      observer.disconnect();
      node.removeAttribute('data-focusable');
      node.removeAttribute('data-focused');
      focusManager.clearIfCurrent(node);
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
