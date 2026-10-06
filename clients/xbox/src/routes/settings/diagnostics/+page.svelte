<script lang="ts">
  // Settings > Diagnostics: what this console's web view can play
  // (lib/diagnostics), and every key event it delivers with what the app
  // makes of it, so a visit with the console settles the codec claims and the
  // controller mapping. Read-only; the key log never takes a key (Back still
  // leaves, through the page's own back handler).

  import { onMount } from 'svelte';
  import { focusable } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import { toRemoteKey } from '$lib/focus/keys';
  import { scrollPageToTop } from '$lib/focus/memory';
  import { goBack } from '$lib/nav';
  import { clientCapabilitiesHeader } from '$lib/api/capabilities';
  import { collectDiagnostics, liveDiagEnv, type DiagSection } from '$lib/diagnostics';
  import { shellParams } from '$lib/shell';
  import { APP_VERSION } from '$lib/version';

  let sections = $state<DiagSection[]>([]);
  let keyLog = $state<string[]>([]);

  function consoleInfo(): Record<string, string> {
    const s = shellParams();
    const pads = typeof navigator.getGamepads === 'function'
      ? navigator.getGamepads().filter((g) => g).map((g) => g!.id)
      : [];
    return {
      Version: APP_VERSION,
      Shell: s.shell ?? 'none (a browser)',
      Device: s.device || 'unknown',
      'Shell display': `HDR ${s.hdr ?? 'unknown'}, 4K ${s.uhd ?? 'unknown'}`,
      Viewport: `${innerWidth}x${innerHeight} CSS px, devicePixelRatio ${devicePixelRatio}, scale ${document.documentElement.style.getPropertyValue('--tv-scale') || '1'}`,
      Screen: `${screen.width}x${screen.height}`,
      Gamepads: pads.length ? pads.join('; ') : 'none seen (press a button)',
      'User agent': navigator.userAgent,
    };
  }

  function logKey(e: KeyboardEvent) {
    const mapped = toRemoteKey(e) ?? '-';
    const line = `${e.type} key=${e.key} code=${e.keyCode}${e.repeat ? ' repeat' : ''} -> ${mapped}`;
    keyLog = [line, ...keyLog].slice(0, 12);
  }

  onMount(() => {
    scrollPageToTop();
    void collectDiagnostics(liveDiagEnv(consoleInfo(), clientCapabilitiesHeader())).then((s) => (sections = s));
    // Capture, passive: seen before the focus manager, never consumed.
    window.addEventListener('keydown', logKey, { capture: true, passive: true });
    window.addEventListener('keyup', logKey, { capture: true, passive: true });
    const offBack = focusManager.pushBack(() => {
      goBack('#/settings');
      return true;
    });
    return () => {
      window.removeEventListener('keydown', logKey, { capture: true });
      window.removeEventListener('keyup', logKey, { capture: true });
      offBack();
    };
  });
</script>

<div class="page">
  <div class="head">
    <h1>Diagnostics</h1>
    <button use:focusable={{ autofocus: true }} class="done" onclick={() => goBack('#/settings')}>Done</button>
  </div>
  <div class="cols">
    <div class="col">
      {#each [sections[0], sections[2]].filter(Boolean) as section (section.title)}
        <div class="section">
          <div class="section-title">{section.title}</div>
          {#each section.rows as row (row.label)}
            <div class="row" class:yes={row.ok === true} class:no={row.ok === false}>
              <span class="label">{row.label}</span><span class="value">{row.value}</span>
            </div>
          {/each}
        </div>
      {/each}
    </div>
    <div class="col">
      {#each [sections[1], sections[3], sections[4]].filter(Boolean) as section (section.title)}
        <div class="section">
          <div class="section-title">{section.title}</div>
          {#each section.rows as row (row.label)}
            <div class="row" class:yes={row.ok === true} class:no={row.ok === false}>
              <span class="label">{row.label}</span><span class="value">{row.value}</span>
            </div>
          {/each}
        </div>
      {/each}
      <div class="section">
        <div class="section-title">Keys (press any button)</div>
        {#each keyLog as line, i (i)}
          <div class="key">{line}</div>
        {:else}
          <div class="key muted">Nothing yet.</div>
        {/each}
      </div>
    </div>
  </div>
</div>

<style>
  /* Full width, and compact enough that every section fits one 1920x1080
     screen: the page doesn't scroll (nothing on it is focusable but Done). */
  .page {
    width: 100%;
    box-sizing: border-box;
    padding: 36px 80px;
    color: var(--text-primary, #fff);
    font-size: 18px;
  }
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 24px;
  }
  h1 {
    font-size: 40px;
    margin: 0;
  }
  .done {
    font-size: 22px;
    padding: 10px 28px;
    border-radius: 999px;
    border: 2px solid transparent;
    background: rgba(255, 255, 255, 0.08);
    color: inherit;
  }
  .cols {
    display: flex;
    width: 100%;
  }
  .col {
    flex: 1;
    min-width: 0;
  }
  .col + .col {
    margin-left: 48px;
  }
  .section {
    margin-bottom: 14px;
  }
  .section-title {
    font-weight: 700;
    font-size: 22px;
    margin-bottom: 6px;
    color: var(--accent, #9b87f5);
  }
  .row {
    display: flex;
    justify-content: space-between;
    padding: 1px 0;
    border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  }
  .label {
    margin-right: 16px;
    color: rgba(255, 255, 255, 0.75);
  }
  .value {
    text-align: right;
    word-break: break-all;
    font-family: ui-monospace, Consolas, monospace;
    font-size: 16px;
  }
  .row.yes .value {
    color: #7ee08a;
  }
  .row.no .value {
    color: #ff8a8a;
  }
  .key {
    font-family: ui-monospace, Consolas, monospace;
    font-size: 16px;
    padding: 2px 0;
  }
  .muted {
    color: rgba(255, 255, 255, 0.5);
  }
</style>
