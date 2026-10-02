import { describe, expect, it, vi } from 'vitest';
import { render } from 'svelte/server';
import ExitPopup from './ExitPopup.svelte';

// The project has no Node typings, so node:fs comes in untyped.
const fsModule = 'node:fs';
const { readFileSync } = (await import(/* @vite-ignore */ fsModule)) as {
  readFileSync(path: URL, encoding: 'utf8'): string;
};
const source = (file: string) => readFileSync(new URL(`./${file}`, import.meta.url), 'utf8');

// The popup rendered on the server (no DOM, so no actions run and nothing
// is clicked): what it offers. Its keys are the options dialog's and the
// focus manager's (lib/focus/manager.test.ts); which screens open it,
// lib/appExit.test.ts.

describe('ExitPopup', () => {
  const html = () => render(ExitPopup, { props: { onexit: vi.fn(), oncancel: vi.fn() } }).body;

  it('asks whether to exit, as a dialog', () => {
    const body = html();
    expect(body).toContain('Exit OnScreen?');
    expect(body).toContain('role="dialog"');
    expect(body).toContain('aria-modal="true"');
  });

  it('offers Exit, then Cancel, as buttons', () => {
    const body = html();
    const re = /<button[^>]*>([^<]*)<\/button>/g;
    const labels: string[] = [];
    for (let m = re.exec(body); m; m = re.exec(body)) labels.push(m[1].trim());
    expect(labels).toEqual(['Exit', 'Cancel']);
  });

  // Not run on the server (the focusable action's autofocus): the ring
  // starts on Cancel, so a second stray Back or OK doesn't end the app.
  it('starts on Cancel', () => {
    const popup = source('ExitPopup.svelte');
    expect(popup.slice(popup.indexOf('<OptionsDialog'), popup.indexOf('/>', popup.indexOf('<OptionsDialog')))).toMatch(
      /\bfocusCancel\b/,
    );
    const dialog = source('OptionsDialog.svelte');
    expect(dialog).toContain('use:focusable={{ autofocus: !focusCancel && i === startAt }}');
    expect(dialog).toContain('use:focusable={{ autofocus: focusCancel || options.length === 0 }}');
  });
});
