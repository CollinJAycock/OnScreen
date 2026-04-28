<script lang="ts">
  import { focusable, focusScope } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';

  interface Props {
    value: string;
    onchange: (v: string) => void;
    onsubmit?: () => void;
    layout?: 'text' | 'url';
  }
  let { value = $bindable(), onchange, onsubmit, layout = 'text' }: Props = $props();

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
</script>

<div class="osk" use:focusScope>
  <div class="display">{value || '\u00A0'}</div>

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
    <button use:focusable class="key wide" onclick={backspace}>&lt;&lt;</button>
    {#if onsubmit}
      <button use:focusable class="key wide submit" onclick={onsubmit}>done</button>
    {/if}
  </div>
</div>

<style>
  .osk {
    background: var(--bg-elevated);
    border-radius: 20px;
    padding: 24px;
    display: flex;
    flex-direction: column;
    gap: 12px;
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

  .row {
    display: flex;
    gap: 8px;
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
