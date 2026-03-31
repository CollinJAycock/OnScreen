<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { api } from '$lib/api';

  let ready = false;

  onMount(() => {
    const user = api.getUser();
    if (!user) { goto('/login'); return; }
    if (!user.is_admin) { goto('/'); return; }
    ready = true;
  });

  $: path = $page.url.pathname;

  const tabs = [
    { href: '/settings', label: 'General', exact: true },
    { href: '/settings/users', label: 'Users', exact: false },
    { href: '/settings/webhooks', label: 'Webhooks', exact: false },
    { href: '/settings/audit', label: 'Audit Log', exact: false },
  ];

  function isActive(tab: { href: string; exact: boolean }, current: string) {
    return tab.exact ? current === tab.href : current.startsWith(tab.href);
  }
</script>

{#if ready}
  <div class="settings-shell">
    <div class="settings-header">
      <h1>Settings</h1>
      <nav class="tab-bar" role="tablist">
        {#each tabs as tab}
          <a
            href={tab.href}
            class="tab"
            class:active={isActive(tab, path)}
            role="tab"
            aria-selected={isActive(tab, path)}
          >
            {tab.label}
          </a>
        {/each}
      </nav>
    </div>
    <slot />
  </div>
{/if}

<style>
  .settings-shell {
    max-width: 720px;
    padding: 2rem 2.5rem;
  }

  .settings-header {
    margin-bottom: 1.75rem;
  }

  h1 {
    font-size: 1.25rem;
    font-weight: 700;
    color: var(--text-primary);
    letter-spacing: -0.02em;
    margin-bottom: 1rem;
  }

  .tab-bar {
    display: flex;
    gap: 0;
    border-bottom: 1px solid rgba(255,255,255,0.07);
  }

  .tab {
    padding: 0.5rem 1rem;
    font-size: 0.78rem;
    font-weight: 500;
    color: var(--text-muted);
    text-decoration: none;
    border-bottom: 2px solid transparent;
    transition: color 0.12s, border-color 0.12s;
    white-space: nowrap;
  }
  .tab:hover {
    color: var(--text-secondary);
  }
  .tab.active {
    color: var(--accent-text);
    border-bottom-color: var(--accent);
  }

  @media (max-width: 768px) {
    .settings-shell {
      padding: 1.25rem 1rem 5rem;
      max-width: 100%;
    }

    .tab-bar {
      overflow-x: auto;
      -webkit-overflow-scrolling: touch;
    }

    .tab {
      padding: 0.5rem 0.75rem;
      font-size: 0.72rem;
    }
  }
</style>
