<script lang="ts">
  // Licence & terms, from Settings > About: the end-user terms LG's Seller
  // Lounge terms ask every app to carry (§7.1), the app's own Apache-2.0
  // licence, and the notices of the open-source code in the bundle with the
  // licence texts that have to travel with it. The words are lib/legal's.
  //
  // Read with the D-pad: every paragraph and notice is focusable, and the
  // page scrolls as focus moves (the focus manager brings the focused block
  // in), so Up / Down step through the text a block at a time; the chips at
  // the top jump to a section. Back returns to Settings, on the row that
  // opened this page (lib/nav's back stack, lib/focus/memory).

  import { onMount } from 'svelte';
  import { focusable } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import TopNav from '$lib/components/TopNav.svelte';
  import { scrollPageToTop } from '$lib/focus/memory';
  import { goBack } from '$lib/nav';
  import {
    APP_LICENCE,
    BSD_3_LICENCE,
    LICENCE_NAMES,
    MIT_LICENCE,
    NOTICES,
    TERMS,
    apacheTerms,
    usedUnder,
  } from '$lib/legal';

  const sections = [
    { id: 'terms', label: 'Terms of use' },
    { id: 'licence', label: 'Licence' },
    { id: 'notices', label: 'Third-party notices' },
    { id: 'texts', label: 'Licence texts' },
  ];

  const apache = apacheTerms();
  const mitUsers = usedUnder('MIT').join(', ');
  const bsdUsers = usedUnder('BSD-3-Clause').join(', ');
  const apacheUsers = ['this app', ...usedUnder('Apache-2.0')].join(', ');

  // A chip's section to the top of the screen, its heading and first blocks
  // with it, rather than the focus manager's nearest edge (its first line at
  // the bottom); focusing the block then has nothing left to scroll.
  function jumpTo(id: string) {
    const first = document.querySelector<HTMLElement>(`#legal-${id} [data-focusable]`);
    if (!first) return;
    first.scrollIntoView({ block: 'start' });
    focusManager.focus(first);
  }

  // Opened from the bottom of Settings, whose scroll the app's page box
  // keeps: back to the top before the first block's autofocus (queued as a
  // microtask by the focusable action) scrolls to it, so the page opens at
  // its title.
  onMount(() => {
    scrollPageToTop();
    return focusManager.pushBack(() => {
      goBack('#/settings');
      return true;
    });
  });
</script>

<div class="page">
  <TopNav />
  <h1>Licence &amp; terms</h1>

  <div class="chips" data-focus-row>
    {#each sections as s (s.id)}
      <button use:focusable class="chip" onclick={() => jumpTo(s.id)}>{s.label}</button>
    {/each}
  </div>

  <section id="legal-terms">
    <h2>Terms of use</h2>
    {#each TERMS as text, i}
      <p use:focusable={{ autofocus: i === 0 }} class="block">{text}</p>
    {/each}
  </section>

  <section id="legal-licence">
    <h2>Licence</h2>
    {#each APP_LICENCE as text}
      <p use:focusable class="block">{text}</p>
    {/each}
  </section>

  <section id="legal-notices">
    <h2>Third-party notices</h2>
    <p use:focusable class="block">
      The app includes the following open-source software, each used under the licence named. The licence
      texts follow.
    </p>
    {#each NOTICES as n (n.name)}
      <div use:focusable class="block notice">
        <div class="notice-name">{n.name}<span class="notice-role">{n.role}</span></div>
        <div>{LICENCE_NAMES[n.licence]}</div>
        <div>{n.notice}</div>
        <div class="mono">{n.url}</div>
        {#if n.includes}
          <div class="includes">Includes:</div>
          {#each n.includes as inc (inc.name)}
            <div class="included">
              <div class="notice-name">{inc.name}<span class="notice-role">{inc.role}</span></div>
              <div>{inc.terms ?? LICENCE_NAMES[inc.licence]}</div>
              <div>{inc.notice}</div>
              <div class="mono">{inc.url}</div>
            </div>
          {/each}
        {/if}
      </div>
    {/each}
  </section>

  <section id="legal-texts">
    <h2>Licence texts</h2>
    <h3>MIT License <span class="used-by">({mitUsers})</span></h3>
    {#each MIT_LICENCE as text}
      <p use:focusable class="block">{text}</p>
    {/each}
    <h3>BSD 3-Clause License <span class="used-by">({bsdUsers})</span></h3>
    {#each BSD_3_LICENCE as text}
      <p use:focusable class="block">{text}</p>
    {/each}
    <h3>Apache License 2.0 <span class="used-by">({apacheUsers})</span></h3>
    {#each apache as text}
      <p use:focusable class="block">{text}</p>
    {/each}
  </section>
</div>

<style>
  .page {
    padding: 0 var(--page-pad) var(--page-pad);
  }
  h1 {
    font-size: var(--font-2xl);
    margin: 24px 0 24px;
  }

  /* Spacing here is margins, not flexbox `gap` (Chrome 84): webOS 6 runs
     Chromium 79. */
  .chips {
    display: flex;
    margin-bottom: 40px;
  }
  .chip {
    margin-right: 16px;
    padding: 10px 24px;
    font-size: var(--font-sm);
    font-family: inherit;
    color: var(--text-primary);
    background: var(--bg-elevated);
    border: 2px solid transparent;
    border-radius: 999px;
    cursor: pointer;
  }
  /* :focus alone: a list with :focus-visible (Chrome 86) is a rule
     Chromium 79 drops whole, and :focus already covers it. */
  .chip:focus {
    border-color: var(--accent);
    outline: none;
  }

  section {
    max-width: 1200px;
    margin: 0 0 48px;
  }
  h2 {
    font-size: var(--font-sm);
    font-weight: 400;
    color: var(--accent);
    text-transform: uppercase;
    letter-spacing: 0.15em;
    margin: 0 0 16px;
  }
  h3 {
    font-size: var(--font-md);
    font-weight: 600;
    margin: 32px 0 12px;
  }
  .used-by {
    font-size: var(--font-sm);
    font-weight: 400;
    color: var(--text-secondary);
  }

  /* A text block takes focus only so the page scrolls through it: a bar on
     its left edge marks it, not the cards' scaled ring and glow. The
     attribute is the focus manager's (set at run time, so :global for the
     compiler, which would call the selector unused). */
  .block {
    margin: 0 0 8px;
    padding: 10px 20px;
    font-size: var(--font-sm);
    line-height: 1.5;
    color: var(--text-primary);
    border-radius: 8px;
  }
  .block:global([data-focused='true']) {
    transform: none;
    box-shadow: inset 6px 0 0 var(--accent);
    background: rgba(124, 106, 247, 0.1);
  }

  .notice {
    color: var(--text-secondary);
  }
  .notice-name {
    font-size: var(--font-md);
    color: var(--text-primary);
  }
  .notice-role {
    margin-left: 16px;
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }
  .includes {
    margin-top: 12px;
  }
  .included {
    margin: 8px 0 0 32px;
  }
  .mono {
    font-family: monospace;
  }
</style>
