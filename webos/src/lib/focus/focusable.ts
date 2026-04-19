import { focusManager } from './manager';

interface Options {
  autofocus?: boolean;
  onFocus?: () => void;
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

  return {
    destroy() {
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
