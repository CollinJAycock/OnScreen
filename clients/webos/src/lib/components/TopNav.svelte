<script lang="ts">
  // Shared top navigation bar for the hub-level pages. Renders as
  // pill buttons; the current route lights up with the accent color
  // and the focus manager picks each pill up via use:focusable so
  // D-pad navigates the bar.
  import { focusable } from '$lib/focus/focusable';
  import { page } from '$app/state';

  interface Item {
    href: string;
    label: string;
  }

  const items: Item[] = [
    { href: '#/hub/',        label: 'Home' },
    { href: '#/libraries/',  label: 'Libraries' },
    { href: '#/search/',     label: 'Search' },
    { href: '#/favorites/',  label: 'Favorites' },
    { href: '#/history/',    label: 'History' },
    { href: '#/discover/',   label: 'Discover' },
    { href: '#/livetv/',     label: 'Live TV' },
    { href: '#/recordings/', label: 'Recordings' },
    { href: '#/settings/',   label: 'Settings' },
  ];

  const currentHash = $derived(typeof location !== 'undefined' ? location.hash : '');
  $effect(() => { void page.url; });

  function isActive(href: string): boolean {
    const a = href.replace(/^#/, '').replace(/\/$/, '');
    const b = currentHash.replace(/^#/, '').replace(/\/$/, '');
    return a === b;
  }
</script>

<header class="topnav">
  <a class="brand" href="#/hub/" data-sveltekit-preload-data="false">OnScreen</a>
  <nav>
    {#each items as it (it.href)}
      <a
        use:focusable
        class="link"
        class:active={isActive(it.href)}
        href={it.href}
        data-sveltekit-preload-data="false"
      >
        {it.label}
      </a>
    {/each}
  </nav>
</header>

<style>
  .topnav {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 32px var(--page-pad);
    gap: 48px;
  }

  .brand {
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
    gap: 12px;
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
