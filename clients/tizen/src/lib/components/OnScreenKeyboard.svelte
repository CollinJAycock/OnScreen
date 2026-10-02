<script lang="ts">
  import { focusable, focusScope } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import type { RemoteKey } from '$lib/focus/keys';
  import { isDirection, verticalExit } from '$lib/focus/spatial';
  import { maskText } from '$lib/textMask';

  interface Props {
    value: string;
    onchange: (v: string) => void;
    onsubmit?: () => void;
    layout?: 'text' | 'url';
    /** A secret (password, TOTP code, token): the display shows a bullet
     *  per character instead of the text. Only the display changes; `value`
     *  and onchange still carry the real text. */
    masked?: boolean;
    /** The D-pad may leave the keyboard by its top and bottom edges: Up
     *  from the top row, Down from the bottom one (to the filter chips above
     *  and the results below it on the search page). Left and Right stay
     *  inside, as on any keyboard. By default the D-pad can't leave at all,
     *  and a page leaves it through its done key. */
    exitable?: boolean;
  }
  let {
    value = $bindable(),
    onchange,
    onsubmit,
    layout = 'text',
    masked = false,
    exitable = false,
  }: Props = $props();

  const shown = $derived(masked ? maskText(value) : value);

  let shift = $state(false);
  let symbols = $state(false);

  const rows = $derived.by(() => {
    if (symbols) {
      return [
        ['1', '2', '3', '4', '5', '6', '7', '8', '9', '0'],
        ['!', '@', '#', '$', '%', '&', '*', '(', ')', "'"],
        ['-', '_', '=', '+', '[', ']', '{', '}', '\\', '|'],
        [';', ':', '"', '?', '/', '<', '>', ',', '.', '~']
      ];
    }
    const alpha = [
      ['1', '2', '3', '4', '5', '6', '7', '8', '9', '0'],
      ['q', 'w', 'e', 'r', 't', 'y', 'u', 'i', 'o', 'p'],
      ['a', 's', 'd', 'f', 'g', 'h', 'j', 'k', 'l', '.'],
      ['z', 'x', 'c', 'v', 'b', 'n', 'm', '@', '_', '-']
    ];
    if (layout === 'url') {
      alpha[2][9] = '.';
      alpha[3][7] = ':';
      alpha[3][8] = '/';
    }
    return shift ? alpha.map((r) => r.map((k) => k.toUpperCase())) : alpha;
  });

  function type(char: string) {
    onchange(value + char);
    if (shift) shift = false;
  }

  function backspace() {
    onchange(value.slice(0, -1));
  }

  function space() {
    onchange(value + ' ');
  }

  let root: HTMLDivElement | undefined = $state();

  // The keyboard is a focus scope either way, so the focus manager keeps
  // every press inside it; an exitable one also takes Up / Down at its top
  // and bottom edges to the nearest control outside (verticalExit).
  function leaveVertically(k: RemoteKey): boolean {
    const cur = focusManager.currentElement();
    if (!root || !cur || !root.contains(cur) || !isDirection(k)) return false;
    const kb = root;
    const inside = [...kb.querySelectorAll<HTMLElement>('[data-focusable]')];
    // The controls the keyboard sits among: those of the scope around it
    // (a dialog), else the page's.
    const around = kb.parentElement?.closest('[data-focus-scope]') ?? document.body;
    const outside = [...around.querySelectorAll<HTMLElement>('[data-focusable]')].filter(
      (el) => !kb.contains(el),
    );
    const next = verticalExit(cur, inside, outside, k);
    if (!next) return false;
    focusManager.focus(next as HTMLElement);
    return true;
  }

  $effect(() => {
    if (!exitable) return;
    return focusManager.pushKeyHandler(leaveVertically);
  });
</script>

<div class="osk" use:focusScope bind:this={root}>
  <div class="display" class:masked>{shown || '\u00A0'}</div>

  {#each rows as row, ri (ri)}
    <div class="row">
      {#each row as key (key)}
        <button
          use:focusable
          class="key"
          onclick={() => type(key)}
        >{key}</button>
      {/each}
    </div>
  {/each}

  <div class="row controls">
    <button use:focusable class="key wide" onclick={() => (shift = !shift)} class:active={shift}>
      {shift ? 'abc' : 'ABC'}
    </button>
    <button use:focusable class="key wide" onclick={() => (symbols = !symbols)} class:active={symbols}>
      {symbols ? 'abc' : '#+='}
    </button>
    <button use:focusable class="key extra-wide" onclick={space}>space</button>
    <!-- repeatOk: holding OK on delete deletes on, one character per
         auto-repeat, as Android TV's keyboards do. Only delete: a held OK
         is one press everywhere else, letters included: an OK held a beat
         long on the Magic Remote's wheel-click shouldn't type "aaaa" into a
         title, a password or a server address, and a run of the same
         letter is a few presses. A field to clear is the case a hold is
         for. -->
    <button use:focusable={{ repeatOk: true }} class="key wide" onclick={backspace}>&lt;&lt;</button>
    {#if onsubmit}
      <button use:focusable class="key wide submit" onclick={onsubmit}>done</button>
    {/if}
  </div>
</div>

<style>
  /* Rows and keys are spaced with margins, not flexbox `gap` (Chrome 84):
     webOS 6 runs Chromium 79. */
  .osk {
    background: var(--bg-elevated);
    border-radius: 20px;
    padding: 24px;
    display: flex;
    flex-direction: column;
    width: max-content;
  }

  .display {
    font-size: var(--font-lg);
    padding: 16px 24px;
    background: var(--bg-secondary);
    border: 2px solid var(--border);
    border-radius: 12px;
    min-height: 72px;
    min-width: 700px;
    line-height: 1.4;
    word-break: break-all;
  }

  /* Bullets read better spaced out a little. */
  .display.masked {
    letter-spacing: 0.15em;
  }

  .row {
    display: flex;
    margin-top: 12px;
  }

  /* :global() on the second part: Svelte 5 would scope it as :where(),
     which Chromium 79 doesn't know, and drop the rule there. */
  .key + :global(.key) {
    margin-left: 8px;
  }

  .key {
    font-size: var(--font-md);
    font-family: inherit;
    width: 72px;
    height: 72px;
    border-radius: 10px;
    border: 2px solid var(--border);
    background: var(--bg-secondary);
    color: var(--text-primary);
    cursor: pointer;
  }

  .key.wide { width: 110px; }
  .key.extra-wide { width: 240px; }
  .key.submit { background: var(--accent); border-color: var(--accent); color: white; }
  .key.active { background: var(--accent-bg); border-color: var(--accent); }
</style>
