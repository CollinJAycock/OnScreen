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

  // Two-level nav. Top-level groups collapse the previous 15-tab list
  // into 7 buckets; selecting a group with multiple children renders a
  // second-level sub-nav that links to the existing per-screen routes
  // (no route changes — bookmarks still work).
  type SubTab = { href: string; label: string };
  type Group = {
    label: string;
    href: string; // landing href when the group is clicked
    matches: string[]; // any URL prefix that activates this group
    children: SubTab[]; // empty if the group is a single page
  };

  const groups: Group[] = [
    {
      label: 'General',
      href: '/settings',
      matches: ['/settings', '/settings/security', '/settings/system'],
      children: [
        { href: '/settings', label: 'General' },
        { href: '/settings/security', label: 'Security' },
        { href: '/settings/system', label: 'System' },
      ],
    },
    {
      label: 'Library',
      href: '/settings/unmatched',
      matches: ['/settings/unmatched', '/settings/missing-art', '/settings/tasks', '/settings/maintenance'],
      children: [
        { href: '/settings/unmatched', label: 'Fix Match' },
        { href: '/settings/missing-art', label: 'Set Poster' },
        { href: '/settings/tasks', label: 'Tasks' },
        { href: '/settings/maintenance', label: 'Maintenance' },
      ],
    },
    {
      label: 'Transcode',
      href: '/settings/transcode',
      matches: ['/settings/transcode'],
      children: [],
    },
    {
      label: 'Users',
      href: '/settings/users',
      matches: ['/settings/users', '/settings/sso', '/settings/email'],
      children: [
        { href: '/settings/users', label: 'Users' },
        { href: '/settings/sso', label: 'SSO' },
        { href: '/settings/email', label: 'Email' },
      ],
    },
    {
      label: 'Integrations',
      href: '/settings/webhooks',
      matches: ['/settings/webhooks', '/settings/arr-services', '/settings/plugins', '/settings/storage'],
      children: [
        { href: '/settings/webhooks', label: 'Webhooks' },
        { href: '/settings/arr-services', label: 'Arr Services' },
        { href: '/settings/plugins', label: 'Plugins' },
        { href: '/settings/storage', label: 'Storage' },
      ],
    },
    {
      label: 'Live TV',
      href: '/settings/tv',
      matches: ['/settings/tv'],
      children: [],
    },
    {
      label: 'Observability',
      href: '/settings/observability',
      matches: ['/settings/observability', '/settings/audit', '/settings/backup'],
      children: [
        { href: '/settings/observability', label: 'Dashboards' },
        { href: '/settings/audit', label: 'Audit Log' },
        { href: '/settings/backup', label: 'Backup' },
      ],
    },
  ];

  // The General group's pages all live directly under /settings (the
  // General page itself + Security), but every OTHER settings route also
  // starts with /settings — so plain prefix matching would make General
  // match everything. Enumerate its exact pages instead.
  function groupActive(g: Group, current: string): boolean {
    if (g.label === 'General') return current === '/settings' || current === '/settings/security' || current === '/settings/system';
    return g.matches.some((m) => current === m || current.startsWith(m + '/'));
  }

  $: activeGroup = groups.find((g) => groupActive(g, path));
  $: subTabs = activeGroup?.children ?? [];
</script>

{#if ready}
  <div class="settings-shell">
    <div class="settings-header">
      <h1>Settings</h1>
      <nav class="tab-bar" role="tablist">
        {#each groups as g}
          <a
            href={g.href}
            class="tab"
            class:active={groupActive(g, path)}
            role="tab"
            aria-selected={groupActive(g, path)}
          >
            {g.label}
          </a>
        {/each}
      </nav>
      {#if subTabs.length > 0}
        <nav class="sub-tab-bar" role="tablist" aria-label="{activeGroup?.label} sections">
          {#each subTabs as st}
            <a
              href={st.href}
              class="sub-tab"
              class:active={path === st.href}
              role="tab"
              aria-selected={path === st.href}
            >
              {st.label}
            </a>
          {/each}
        </nav>
      {/if}
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

  /* Sub-nav rendered when the active top-level group has children.
     Lighter styling than the top tabs so the hierarchy reads at a
     glance — top tabs are the route, sub-tabs are the section. */
  .sub-tab-bar {
    display: flex;
    gap: 0.4rem;
    margin-top: 0.85rem;
    flex-wrap: wrap;
  }
  .sub-tab {
    padding: 0.3rem 0.7rem;
    font-size: 0.72rem;
    font-weight: 500;
    color: var(--text-muted);
    text-decoration: none;
    background: rgba(255,255,255,0.03);
    border: 1px solid rgba(255,255,255,0.06);
    border-radius: 999px;
    transition: color 0.12s, background 0.12s, border-color 0.12s;
  }
  .sub-tab:hover {
    color: var(--text-secondary);
    background: rgba(255,255,255,0.06);
  }
  .sub-tab.active {
    color: var(--accent-text);
    background: rgba(124,106,247,0.18);
    border-color: rgba(124,106,247,0.4);
  }

  @media (max-width: 768px) {
    .settings-shell {
      padding: 1.25rem 1rem 5rem;
      max-width: 100%;
    }

    .tab-bar, .sub-tab-bar {
      overflow-x: auto;
      -webkit-overflow-scrolling: touch;
    }

    .tab {
      padding: 0.5rem 0.75rem;
      font-size: 0.72rem;
    }
  }
</style>
