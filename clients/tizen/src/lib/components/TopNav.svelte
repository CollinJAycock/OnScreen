<script lang="ts">
  // Shared top navigation bar for the hub-level pages. Renders as
  // pill buttons; the current route lights up with the accent color
  // and the focus manager picks each pill up via use:focusable so
  // D-pad navigates the bar.
  import { focusable } from '$lib/focus/focusable';
  import { page } from '$app/state';
  import { ensureNavGates, navGates, type NavGates } from '$lib/navGates';

  interface Item {
    href: string;
    label: string;
    /** Shown only while this gate is open (lib/navGates). */
    gate?: keyof NavGates;
  }

  const items: Item[] = [
    { href: '#/hub/',        label: 'Home' },
    { href: '#/libraries/',  label: 'Libraries' },
    { href: '#/search/',     label: 'Search' },
    { href: '#/favorites/',  label: 'Favorites' },
    { href: '#/history/',    label: 'History' },
    { href: '#/discover/',   label: 'Discover',   gate: 'discover' },
    { href: '#/livetv/',     label: 'Live TV',    gate: 'liveTv' },
    { href: '#/recordings/', label: 'Recordings', gate: 'recordings' },
    { href: '#/settings/',   label: 'Settings' },
  ];

  // Discover, Live TV and Recordings only once the server has said they
  // apply to this account (closed while that loads). Keyed by href, so a
  // pill popping in leaves the others, and the focus on one, in place; the
  // focus manager moves by position, not by index, so fewer pills need
  // nothing from it.
  const shown = $derived(items.filter((it) => !it.gate || $navGates[it.gate]));

  // Asked by each new bar (a no-op while known and fresh; a failed answer
  // is asked again). At the top level rather than in onMount: once per
  // instance all the same, and it also runs under svelte/server, which is
  // how the tests render this.
  void ensureNavGates();

  const currentHash = $derived(typeof location !== 'undefined' ? location.hash : '');
  $effect(() => { void page.url; });

  function isActive(href: string): boolean {
    const a = href.replace(/^#/, '').replace(/\/$/, '');
    const b = currentHash.replace(/^#/, '').replace(/\/$/, '');
    return a === b;
  }
</script>

<!-- data-focus-row: Left / Right stay in the bar (the focus manager), so
     Left on Home and Right on Settings stop there. aria-current marks this
     page's pill, where the focus manager goes when a page has nothing of
     its own focused. -->
<header class="topnav">
  <a class="brand" href="#/hub/" data-sveltekit-preload-data="false">OnScreen</a>
  <nav data-focus-row>
    {#each shown as it (it.href)}
      <a
        use:focusable
        class="link"
        class:active={isActive(it.href)}
        aria-current={isActive(it.href) ? 'page' : undefined}
        href={it.href}
        data-sveltekit-preload-data="false"
      >
        {it.label}
      </a>
    {/each}
  </nav>
</header>

<style>
  /* Spacing here is margins, not flexbox `gap` (Chrome 84): webOS 6 runs
     Chromium 79, where the brand would butt up against the pills and the
     pills against each other. */
  /* No side padding: every page already pads by --page-pad, and the bar
     padding by it again put it 80 px further in than on the hub (whose rows
     run full-bleed, so it insets the bar itself). */
  .topnav {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 32px 0;
  }

  .brand {
    /* The bar's old 48px gap: space-between spreads what's left over. */
    margin-right: 48px;
    /* Hard-override the user-agent <a> defaults — webOS Chromium 79
       sometimes wins before scoped CSS applies. */
    color: var(--accent);
    text-decoration: none !important;
    font-size: var(--font-xl);
    font-weight: 700;
    letter-spacing: 0.02em;
    white-space: nowrap;
  }

  nav {
    display: flex;
    flex-wrap: nowrap;
    align-items: center;
  }

  .link + .link {
    margin-left: 12px;
  }

  .link {
    color: var(--text-secondary);
    text-decoration: none !important;
    font-size: var(--font-md);
    padding: 10px 22px;
    border-radius: 999px;
    border: 2px solid transparent;
    transition: color 0.15s ease, background 0.15s ease, border-color 0.15s ease;
    white-space: nowrap;
  }

  .link.active {
    color: var(--text-primary);
    background: rgba(124, 106, 247, 0.18);
    border-color: rgba(124, 106, 247, 0.45);
  }
</style>
