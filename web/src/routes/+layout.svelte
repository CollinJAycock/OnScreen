<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { api, authApi, userApi } from '$lib/api';
  import type { SwitchableUser } from '$lib/api';
  import { derived } from 'svelte/store';
  import Logo from '$lib/components/Logo.svelte';
  import ToastContainer from '$lib/components/ToastContainer.svelte';
  import NotificationBell from '$lib/components/NotificationBell.svelte';
  import NotificationPanel from '$lib/components/NotificationPanel.svelte';
  import AudioPlayer from '$lib/components/AudioPlayer.svelte';
  import { theme } from '$lib/stores/theme';
  import { initNotifications, stopNotifications } from '$lib/stores/notifications';
  import { currentTrack } from '$lib/stores/audio';

  $: hasAudio = $currentTrack !== null;

  let currentTheme: 'light' | 'dark' | 'system';
  theme.subscribe(v => { currentTheme = v; });

  let checking = true;

  const isAuthPage = derived(page, $p =>
    $p.url.pathname === '/login' || $p.url.pathname.startsWith('/setup')
  );

  // User switcher state
  let currentUsername = '';
  let isAdmin = false;
  let switcherOpen = false;
  let switchableUsers: SwitchableUser[] = [];
  let switchTarget: SwitchableUser | null = null;
  let pinDigits = '';
  let pinError = '';
  let switching = false;
  let notifOpen = false;

  onMount(async () => {
    theme.init();
    try {
      const status = await authApi.setupStatus();
      if (status.setup_required && !$page.url.pathname.startsWith('/setup')) {
        checking = false;
        goto('/setup');
        return;
      }
    } catch (e) { console.warn('setup status check failed', e); }
    checking = false;

    // Auth-callback bootstrap: every SSO/SAML/OIDC handler sets httpOnly
    // auth cookies on the redirect, but the SPA's per-route auth gate
    // checks localStorage.onscreen_user (which can't be written from a
    // server response). On marker query params we hit /auth/refresh,
    // which validates the cookie and returns the user info we need.
    // Without this, the user signs in upstream, lands on /, the gate
    // sees no localStorage user, and bounces to /login — silent loop.
    const authCallbackMarker =
      $page.url.searchParams.get('google_auth') === '1' ||
      $page.url.searchParams.get('oidc_auth') === '1' ||
      $page.url.searchParams.get('saml_auth') === '1';
    if (authCallbackMarker) {
      try {
        const resp = await fetch('/api/v1/auth/refresh', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          credentials: 'same-origin'
        });
        if (resp.ok) {
          const json = await resp.json();
          const pair = json.data;
          api.setUser({ user_id: pair.user_id, username: pair.username, is_admin: pair.is_admin });
          currentUsername = pair.username;
          isAdmin = pair.is_admin;
          initNotifications();
        }
      } catch {}
      // Strip every known marker so refresh doesn't re-trigger the loop.
      const url = new URL(window.location.href);
      url.searchParams.delete('google_auth');
      url.searchParams.delete('oidc_auth');
      url.searchParams.delete('saml_auth');
      window.history.replaceState({}, '', url.pathname + (url.search ? url.search : ''));
    }

    // Load current user info
    const user = api.getUser();
    if (user) {
      currentUsername = user.username;
      isAdmin = user.is_admin;
      initNotifications();
    }
  });

  async function openSwitcher() {
    switcherOpen = true;
    switchTarget = null;
    pinDigits = '';
    pinError = '';
    try {
      switchableUsers = await userApi.listSwitchable();
    } catch { switchableUsers = []; }
  }

  function closeSwitcher() {
    switcherOpen = false;
    switchTarget = null;
    pinDigits = '';
    pinError = '';
  }

  function selectUser(user: SwitchableUser) {
    if (!user.has_pin) return;
    switchTarget = user;
    pinDigits = '';
    pinError = '';
  }

  function handleSwitchPinInput(e: Event) {
    const input = e.target as HTMLInputElement;
    input.value = input.value.replace(/\D/g, '').slice(0, 4);
    pinDigits = input.value;
    if (pinDigits.length === 4) {
      submitPinSwitch();
    }
  }

  async function submitPinSwitch() {
    if (!switchTarget || pinDigits.length !== 4) return;
    switching = true;
    pinError = '';
    try {
      const pair = await userApi.pinSwitch(switchTarget.id, pinDigits);
      const newUser = { user_id: pair.user_id, username: pair.username, is_admin: pair.is_admin };
      api.setUser(newUser);
      currentUsername = newUser.username;
      isAdmin = newUser.is_admin;
      stopNotifications();
      initNotifications();
      closeSwitcher();
      window.location.href = '/';
    } catch (e: unknown) {
      pinError = e instanceof Error ? e.message : 'Invalid PIN';
      pinDigits = '';
    } finally {
      switching = false;
    }
  }

  async function logout() {
    stopNotifications();
    try { await authApi.logout(); } catch { /* ignore */ }
    api.setUser(null);
    goto('/login');
  }

  function handleWindowClick() {
    if (notifOpen) notifOpen = false;
  }

  $: path = $page.url.pathname;

  // Re-read user info on every navigation so isAdmin updates after login.
  $: if (path) {
    const u = api.getUser();
    if (u) {
      currentUsername = u.username;
      isAdmin = u.is_admin;
    } else {
      isAdmin = false;
    }
  }
</script>

<svelte:window on:click={handleWindowClick} />
<ToastContainer />

{#if checking}
  <div class="splash">
    <Logo size="lg" wordmark={false} />
  </div>
{:else if $isAuthPage}
  <slot />
{:else}
  <div class="shell">
    <aside class="sidebar">
      <a href="/" class="brand">
        <Logo size="sm" />
      </a>

      <nav>
        <a href="/" class="nav-link" class:active={path === '/' || path.startsWith('/libraries')}>
          <svg viewBox="0 0 20 20" fill="currentColor" width="16" height="16">
            <path d="M10.707 2.293a1 1 0 00-1.414 0l-7 7a1 1 0 001.414 1.414L4 10.414V17a1 1 0 001 1h2a1 1 0 001-1v-2a1 1 0 011-1h2a1 1 0 011 1v2a1 1 0 001 1h2a1 1 0 001-1v-6.586l.293.293a1 1 0 001.414-1.414l-7-7z"/>
          </svg>
          Home
        </a>
        <a href="/search" class="nav-link" class:active={path.startsWith('/search')}>
          <svg viewBox="0 0 20 20" fill="currentColor" width="16" height="16">
            <path fill-rule="evenodd" d="M9 3.5a5.5 5.5 0 100 11 5.5 5.5 0 000-11zM2 9a7 7 0 1112.452 4.391l3.328 3.329a.75.75 0 11-1.06 1.06l-3.329-3.328A7 7 0 012 9z" clip-rule="evenodd"/>
          </svg>
          Search
        </a>
        {#if isAdmin}
          <a href="/analytics" class="nav-link" class:active={path.startsWith('/analytics')}>
            <svg viewBox="0 0 20 20" fill="currentColor" width="16" height="16">
              <path d="M15.5 2A1.5 1.5 0 0014 3.5v13a1.5 1.5 0 003 0v-13A1.5 1.5 0 0015.5 2zM9.5 6A1.5 1.5 0 008 7.5v9a1.5 1.5 0 003 0v-9A1.5 1.5 0 009.5 6zM3.5 10A1.5 1.5 0 002 11.5v5a1.5 1.5 0 003 0v-5A1.5 1.5 0 003.5 10z"/>
            </svg>
            Analytics
          </a>
        {/if}
        <a href="/collections" class="nav-link" class:active={path.startsWith('/collections')}>
          <svg viewBox="0 0 20 20" fill="currentColor" width="16" height="16">
            <path d="M2 4.75A.75.75 0 012.75 4h14.5a.75.75 0 010 1.5H2.75A.75.75 0 012 4.75zm0 10.5a.75.75 0 01.75-.75h7.5a.75.75 0 010 1.5h-7.5a.75.75 0 01-.75-.75zM2 10a.75.75 0 01.75-.75h14.5a.75.75 0 010 1.5H2.75A.75.75 0 012 10z"/>
          </svg>
          Collections
        </a>
        <a href="/history" class="nav-link" class:active={path.startsWith('/history')}>
          <svg viewBox="0 0 20 20" fill="currentColor" width="16" height="16">
            <path fill-rule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zm.75-13a.75.75 0 00-1.5 0v5c0 .414.336.75.75.75h3a.75.75 0 000-1.5h-2.25V5z" clip-rule="evenodd"/>
          </svg>
          History
        </a>
        <a href="/requests" class="nav-link" class:active={path.startsWith('/requests')}>
          <svg viewBox="0 0 20 20" fill="currentColor" width="16" height="16">
            <path fill-rule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM10 7a1 1 0 011 1v2h2a1 1 0 110 2h-2v2a1 1 0 11-2 0v-2H7a1 1 0 110-2h2V8a1 1 0 011-1z" clip-rule="evenodd"/>
          </svg>
          Requests
        </a>
        <a href="/favorites" class="nav-link" class:active={path.startsWith('/favorites')}>
          <svg viewBox="0 0 20 20" fill="currentColor" width="16" height="16">
            <path d="M9.653 16.915l-.005-.003-.019-.01a20.759 20.759 0 01-1.162-.682 22.045 22.045 0 01-2.582-1.9C4.045 12.733 2 10.352 2 7.5a4.5 4.5 0 018-2.828A4.5 4.5 0 0118 7.5c0 2.852-2.044 5.233-3.885 6.82a22.049 22.049 0 01-3.744 2.582l-.019.01-.005.003h-.002a.739.739 0 01-.69.001l-.002-.001z"/>
          </svg>
          Favorites
        </a>
        <a href="/tv/guide" class="nav-link" class:active={path.startsWith('/tv')}>
          <svg viewBox="0 0 20 20" fill="currentColor" width="16" height="16">
            <path fill-rule="evenodd" d="M2 4.25A2.25 2.25 0 014.25 2h11.5A2.25 2.25 0 0118 4.25v8.5A2.25 2.25 0 0115.75 15h-3.105a3.501 3.501 0 001.1 1.677A.75.75 0 0113.26 18H6.74a.75.75 0 01-.484-1.323A3.501 3.501 0 007.355 15H4.25A2.25 2.25 0 012 12.75v-8.5z" clip-rule="evenodd"/>
          </svg>
          Live TV
        </a>
        {#if isAdmin}
          <a href="/profiles" class="nav-link" class:active={path.startsWith('/profiles')}>
            <svg viewBox="0 0 20 20" fill="currentColor" width="16" height="16">
              <path d="M7 8a3 3 0 100-6 3 3 0 000 6zM14.5 9a2.5 2.5 0 100-5 2.5 2.5 0 000 5zM1.615 16.428a1.224 1.224 0 01-.569-1.175 6.002 6.002 0 0111.908 0c.058.467-.172.92-.57 1.174A9.953 9.953 0 017 18a9.953 9.953 0 01-5.385-1.572zM14.5 16h-.106c.07-.297.088-.611.048-.933a7.47 7.47 0 00-1.588-3.755 4.502 4.502 0 015.874 2.636.818.818 0 01-.36.98A7.465 7.465 0 0114.5 16z"/>
            </svg>
            Profiles
          </a>
        {/if}
        {#if isAdmin}
          <a href="/settings" class="nav-link" class:active={path.startsWith('/settings')}>
            <svg viewBox="0 0 20 20" fill="currentColor" width="16" height="16">
              <path fill-rule="evenodd" d="M7.84 1.804A1 1 0 018.82 1h2.36a1 1 0 01.98.804l.331 1.652a6.993 6.993 0 011.929 1.115l1.598-.54a1 1 0 011.186.447l1.18 2.044a1 1 0 01-.205 1.251l-1.267 1.113a7.047 7.047 0 010 2.228l1.267 1.113a1 1 0 01.205 1.251l-1.18 2.044a1 1 0 01-1.186.447l-1.598-.54a6.993 6.993 0 01-1.929 1.115l-.33 1.652a1 1 0 01-.98.804H8.82a1 1 0 01-.98-.804l-.331-1.652a6.993 6.993 0 01-1.929-1.115l-1.598.54a1 1 0 01-1.186-.447l-1.18-2.044a1 1 0 01.205-1.251l1.267-1.113a7.047 7.047 0 010-2.228L1.808 8.465a1 1 0 01-.205-1.251l1.18-2.044a1 1 0 011.186-.447l1.598.54A6.993 6.993 0 017.51 3.456l.33-1.652zM10 13a3 3 0 100-6 3 3 0 000 6z" clip-rule="evenodd"/>
            </svg>
            Settings
          </a>
        {/if}
      </nav>

      <div class="sidebar-foot">
        <div class="notif-wrapper">
          <NotificationBell bind:open={notifOpen} />
          {#if notifOpen}
            <NotificationPanel on:close={() => { notifOpen = false; }} />
          {/if}
        </div>
        <button class="theme-toggle" aria-label="Toggle theme" on:click={() => theme.toggle()}>
          {#if currentTheme === 'light' || (currentTheme === 'system' && typeof window !== 'undefined' && window.matchMedia('(prefers-color-scheme: light)').matches)}
            <svg viewBox="0 0 20 20" fill="currentColor" width="15" height="15"><path d="M17.293 13.293A8 8 0 016.707 2.707a8.001 8.001 0 1010.586 10.586z"/></svg>
            Dark mode
          {:else}
            <svg viewBox="0 0 20 20" fill="currentColor" width="15" height="15"><path fill-rule="evenodd" d="M10 2a1 1 0 011 1v1a1 1 0 11-2 0V3a1 1 0 011-1zm4 8a4 4 0 11-8 0 4 4 0 018 0zm-.464 4.95l.707.707a1 1 0 001.414-1.414l-.707-.707a1 1 0 00-1.414 1.414zm2.12-10.607a1 1 0 010 1.414l-.706.707a1 1 0 11-1.414-1.414l.707-.707a1 1 0 011.414 0zM17 11a1 1 0 100-2h-1a1 1 0 100 2h1zm-7 4a1 1 0 011 1v1a1 1 0 11-2 0v-1a1 1 0 011-1zM5.05 6.464A1 1 0 106.465 5.05l-.708-.707a1 1 0 00-1.414 1.414l.707.707zm1.414 8.486l-.707.707a1 1 0 01-1.414-1.414l.707-.707a1 1 0 011.414 1.414zM4 11a1 1 0 100-2H3a1 1 0 000 2h1z" clip-rule="evenodd"/></svg>
            Light mode
          {/if}
        </button>
        <button class="user-switch-btn" aria-label="Switch user" on:click={openSwitcher}>
          <svg viewBox="0 0 20 20" fill="currentColor" width="15" height="15">
            <path fill-rule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-5.5-2.5a2.5 2.5 0 11-5 0 2.5 2.5 0 015 0zM10 12a4.978 4.978 0 00-3.462 1.393A5.99 5.99 0 0010 15a5.99 5.99 0 003.462-1.607A4.978 4.978 0 0010 12z" clip-rule="evenodd"/>
          </svg>
          {currentUsername || 'User'}
          <svg viewBox="0 0 20 20" fill="currentColor" width="12" height="12" class="chevron">
            <path fill-rule="evenodd" d="M5.23 7.21a.75.75 0 011.06.02L10 11.168l3.71-3.938a.75.75 0 111.08 1.04l-4.25 4.5a.75.75 0 01-1.08 0l-4.25-4.5a.75.75 0 01.02-1.06z" clip-rule="evenodd"/>
          </svg>
        </button>
        <button class="signout" aria-label="Sign out" on:click={logout}>
          <svg viewBox="0 0 20 20" fill="currentColor" width="15" height="15">
            <path fill-rule="evenodd" d="M3 4.25A2.25 2.25 0 015.25 2h5.5A2.25 2.25 0 0113 4.25v2a.75.75 0 01-1.5 0v-2a.75.75 0 00-.75-.75h-5.5a.75.75 0 00-.75.75v11.5c0 .414.336.75.75.75h5.5a.75.75 0 00.75-.75v-2a.75.75 0 011.5 0v2A2.25 2.25 0 0110.75 18h-5.5A2.25 2.25 0 013 15.75V4.25z" clip-rule="evenodd"/>
            <path fill-rule="evenodd" d="M6 10a.75.75 0 01.75-.75h9.546l-1.048-.943a.75.75 0 111.004-1.114l2.5 2.25a.75.75 0 010 1.114l-2.5 2.25a.75.75 0 11-1.004-1.114l1.048-.943H6.75A.75.75 0 016 10z" clip-rule="evenodd"/>
          </svg>
          Sign out
        </button>
      </div>
    </aside>

    <main class="main" class:has-audio={hasAudio}>
      <slot />
    </main>
    <AudioPlayer />
  </div>

  {#if switcherOpen}
    <!-- svelte-ignore a11y-click-events-have-key-events -- backdrop dismiss does not need keyboard equivalent; Escape is handled elsewhere -->
    <!-- svelte-ignore a11y-no-static-element-interactions -- backdrop is a click-to-dismiss overlay, not interactive content -->
    <div class="switcher-backdrop" on:click={closeSwitcher}>
      <!-- svelte-ignore a11y-click-events-have-key-events -- stopPropagation only, not a real click handler -->
      <!-- svelte-ignore a11y-no-static-element-interactions -- panel container, not an interactive element -->
      <div class="switcher-panel" on:click|stopPropagation>
        <div class="switcher-header">
          <span>Switch User</span>
          <button class="switcher-close" aria-label="Close user switcher" on:click={closeSwitcher}>
            <svg viewBox="0 0 20 20" fill="currentColor" width="16" height="16">
              <path d="M6.28 5.22a.75.75 0 00-1.06 1.06L8.94 10l-3.72 3.72a.75.75 0 101.06 1.06L10 11.06l3.72 3.72a.75.75 0 101.06-1.06L11.06 10l3.72-3.72a.75.75 0 00-1.06-1.06L10 8.94 6.28 5.22z"/>
            </svg>
          </button>
        </div>

        {#if !switchTarget}
          <div class="switcher-list">
            {#each switchableUsers as user}
              <button
                class="switcher-user"
                class:disabled={!user.has_pin}
                disabled={!user.has_pin}
                on:click={() => selectUser(user)}
              >
                <div class="switcher-avatar">
                  {user.username.charAt(0).toUpperCase()}
                </div>
                <div class="switcher-info">
                  <span class="switcher-name">{user.username}</span>
                  {#if user.is_admin}
                    <span class="switcher-badge">Admin</span>
                  {/if}
                </div>
                {#if !user.has_pin}
                  <span class="switcher-no-pin">No PIN set</span>
                {/if}
              </button>
            {/each}
          </div>
        {:else}
          <div class="switcher-pin">
            <button class="switcher-back" on:click={() => { switchTarget = null; pinDigits = ''; pinError = ''; }}>
              <svg viewBox="0 0 20 20" fill="currentColor" width="14" height="14">
                <path fill-rule="evenodd" d="M17 10a.75.75 0 01-.75.75H5.612l4.158 3.96a.75.75 0 11-1.04 1.08l-5.5-5.25a.75.75 0 010-1.08l5.5-5.25a.75.75 0 011.04 1.08L5.612 9.25H16.25A.75.75 0 0117 10z" clip-rule="evenodd"/>
              </svg>
              Back
            </button>
            <div class="switcher-pin-user">
              <div class="switcher-avatar lg">
                {switchTarget.username.charAt(0).toUpperCase()}
              </div>
              <span>{switchTarget.username}</span>
            </div>
            <label class="switcher-pin-label" for="switcher-pin-input">Enter 4-digit PIN</label>
            <input
              id="switcher-pin-input"
              class="switcher-pin-input"
              type="password"
              inputmode="numeric"
              pattern="\d{4}"
              maxlength="4"
              value={pinDigits}
              on:input={handleSwitchPinInput}
              placeholder="----"
              autocomplete="off"
              disabled={switching}
            />
            {#if pinError}
              <div class="switcher-pin-error">{pinError}</div>
            {/if}
            {#if switching}
              <div class="switcher-pin-loading">Switching...</div>
            {/if}
          </div>
        {/if}
      </div>
    </div>
  {/if}
{/if}

<style>
  :global(:root), :global([data-theme="dark"]) {
    /* Hint native form controls (select option lists, date/time pickers,
     * scrollbars, file input buttons) to render with the dark system
     * palette instead of the light default. Without this the options
     * popup on <select> kept coming back as white-on-dark-text, which
     * looked like a bug. */
    color-scheme: dark;

    --bg-primary: #07070d;
    --bg-secondary: #0c0c15;
    --bg-elevated: #111118;
    --bg-hover: rgba(255,255,255,0.05);
    --text-primary: #eeeef8;
    --text-secondary: #8888aa;
    --text-muted: #55556a;
    --accent: #7c6af7;
    --accent-hover: #8f7ef9;
    --accent-bg: rgba(124,106,247,0.12);
    --accent-text: #a89ffa;
    --border: rgba(255,255,255,0.055);
    --border-strong: rgba(255,255,255,0.12);
    --error: #fca5a5;
    --error-bg: rgba(248,113,113,0.12);
    --success: #86efac;
    --success-bg: rgba(52,211,153,0.12);
    --info: #93c5fd;
    --info-bg: rgba(96,165,250,0.12);
    --shadow: rgba(0,0,0,0.5);
    --input-bg: rgba(255,255,255,0.04);
  }
  :global([data-theme="light"]) {
    color-scheme: light;

    --bg-primary: #f5f5f7;
    --bg-secondary: #ffffff;
    --bg-elevated: #ffffff;
    --bg-hover: rgba(0,0,0,0.04);
    --text-primary: #1a1a2e;
    --text-secondary: #666680;
    --text-muted: #9999aa;
    --accent: #6c5ce7;
    --accent-hover: #5a4bd6;
    --accent-bg: rgba(108,92,231,0.08);
    --accent-text: #6c5ce7;
    --border: rgba(0,0,0,0.08);
    --border-strong: rgba(0,0,0,0.15);
    --error: #ef4444;
    --error-bg: rgba(239,68,68,0.08);
    --success: #22c55e;
    --success-bg: rgba(34,197,94,0.08);
    --info: #3b82f6;
    --info-bg: rgba(59,130,246,0.08);
    --shadow: rgba(0,0,0,0.12);
    --input-bg: rgba(0,0,0,0.03);
  }

  /* Belt-and-suspenders for Firefox, which doesn't fully style native
   * <option> elements via color-scheme on older versions. Chrome 125+
   * respects color-scheme so these rules are a no-op there. */
  :global(select) {
    background-color: var(--input-bg);
    color: var(--text-primary);
  }
  :global(option) {
    background-color: var(--bg-elevated);
    color: var(--text-primary);
  }

  :global(*, *::before, *::after) { box-sizing: border-box; margin: 0; padding: 0; }
  :global(body) {
    font-family: -apple-system, BlinkMacSystemFont, 'Inter', 'Segoe UI', sans-serif;
    background: var(--bg-primary);
    color: var(--text-primary);
    -webkit-font-smoothing: antialiased;
  }
  :global(a) { color: inherit; }

  .splash {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 100vh;
    background: var(--bg-primary);
  }
  .splash :global(.logo-icon) {
    animation: pulse 1.8s ease-in-out infinite;
  }
  @keyframes pulse { 0%,100% { opacity: 1; } 50% { opacity: 0.35; } }
  .shell { display: flex; height: 100vh; overflow: hidden; }

  .sidebar {
    width: 216px;
    flex-shrink: 0;
    background: var(--bg-secondary);
    border-right: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    padding: 0;
  }

  .brand {
    display: flex;
    align-items: center;
    padding: 1.1rem 1.1rem 0.9rem;
    text-decoration: none;
    border-bottom: 1px solid var(--border);
    margin-bottom: 0.5rem;
  }

  nav { padding: 0 0.6rem; flex: 1; }

  .nav-link {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    padding: 0.5rem 0.65rem;
    border-radius: 7px;
    color: var(--text-muted);
    text-decoration: none;
    font-size: 0.82rem;
    font-weight: 500;
    transition: background 0.12s, color 0.12s;
  }
  .nav-link:hover { background: var(--bg-hover); color: var(--text-secondary); }
  .nav-link.active { background: var(--accent-bg); color: var(--accent-text); }

  .sidebar-foot {
    padding: 0.6rem;
    border-top: 1px solid var(--border);
  }
  .signout {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    width: 100%;
    padding: 0.45rem 0.65rem;
    background: none;
    border: none;
    border-radius: 7px;
    color: var(--text-muted);
    font-size: 0.78rem;
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .signout:hover { background: var(--bg-hover); color: var(--text-secondary); }

  .notif-wrapper {
    position: relative;
    margin-bottom: 0.25rem;
  }

  .theme-toggle {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    width: 100%;
    padding: 0.45rem 0.65rem;
    background: none;
    border: none;
    border-radius: 7px;
    color: var(--text-muted);
    font-size: 0.78rem;
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
    margin-bottom: 0.25rem;
  }
  .theme-toggle:hover { background: var(--bg-hover); color: var(--text-secondary); }

  .user-switch-btn {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    width: 100%;
    padding: 0.45rem 0.65rem;
    background: none;
    border: none;
    border-radius: 7px;
    color: var(--text-secondary);
    font-size: 0.78rem;
    font-weight: 500;
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
    margin-bottom: 0.25rem;
  }
  .user-switch-btn:hover { background: var(--bg-hover); color: var(--text-primary); }
  .user-switch-btn .chevron { margin-left: auto; opacity: 0.5; }

  .main {
    flex: 1;
    overflow-y: auto;
    background: var(--bg-primary);
  }
  .main.has-audio { padding-bottom: 76px; }

  /* User switcher overlay */
  .switcher-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0,0,0,0.6);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
    animation: fadeIn 0.12s ease-out;
  }
  @keyframes fadeIn { from { opacity: 0; } to { opacity: 1; } }

  .switcher-panel {
    background: var(--bg-elevated);
    border: 1px solid var(--border-strong);
    border-radius: 14px;
    width: 320px;
    max-height: 420px;
    overflow-y: auto;
    box-shadow: 0 20px 60px var(--shadow);
    animation: slideUp 0.15s ease-out;
  }
  @keyframes slideUp { from { opacity: 0; transform: translateY(10px); } to { opacity: 1; transform: none; } }

  .switcher-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.9rem 1rem 0.6rem;
    border-bottom: 1px solid var(--border);
    font-size: 0.82rem;
    font-weight: 600;
    color: var(--text-primary);
  }
  .switcher-close {
    background: none; border: none; color: var(--text-muted); cursor: pointer;
    padding: 0.2rem; border-radius: 5px; transition: color 0.12s;
  }
  .switcher-close:hover { color: var(--text-secondary); }

  .switcher-list {
    padding: 0.4rem;
  }
  .switcher-user {
    display: flex;
    align-items: center;
    gap: 0.7rem;
    width: 100%;
    padding: 0.55rem 0.7rem;
    background: none;
    border: none;
    border-radius: 8px;
    color: var(--text-primary);
    font-size: 0.82rem;
    cursor: pointer;
    transition: background 0.12s;
    text-align: left;
  }
  .switcher-user:hover:not(.disabled) { background: var(--bg-hover); }
  .switcher-user.disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  .switcher-avatar {
    width: 32px; height: 32px;
    border-radius: 50%;
    background: var(--accent-bg);
    color: var(--accent-text);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 0.78rem;
    font-weight: 700;
    flex-shrink: 0;
  }
  .switcher-avatar.lg {
    width: 48px; height: 48px;
    font-size: 1.1rem;
  }

  .switcher-info {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    flex: 1;
    min-width: 0;
  }
  .switcher-name {
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .switcher-badge {
    font-size: 0.62rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--accent);
    background: var(--accent-bg);
    padding: 0.12rem 0.4rem;
    border-radius: 4px;
  }
  .switcher-no-pin {
    font-size: 0.7rem;
    color: var(--text-muted);
    margin-left: auto;
    white-space: nowrap;
  }

  .switcher-pin {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.8rem;
  }
  .switcher-back {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    align-self: flex-start;
    background: none;
    border: none;
    color: var(--text-muted);
    font-size: 0.75rem;
    cursor: pointer;
    padding: 0.2rem 0;
    transition: color 0.12s;
  }
  .switcher-back:hover { color: var(--text-secondary); }

  .switcher-pin-user {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.4rem;
    color: var(--text-primary);
    font-size: 0.88rem;
    font-weight: 500;
    margin: 0.3rem 0;
  }
  .switcher-pin-label {
    font-size: 0.72rem;
    color: var(--text-muted);
  }
  .switcher-pin-input {
    width: 140px;
    text-align: center;
    letter-spacing: 0.5em;
    font-size: 1.4rem;
    padding: 0.6rem;
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 10px;
    color: var(--text-primary);
    font-family: monospace;
    outline: none;
    transition: border-color 0.15s;
  }
  .switcher-pin-input:focus {
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }
  .switcher-pin-input::placeholder { color: #2a2a3d; letter-spacing: 0.4em; }
  .switcher-pin-error {
    font-size: 0.75rem;
    color: var(--error);
  }
  .switcher-pin-loading {
    font-size: 0.75rem;
    color: var(--accent);
  }

  /* ── Mobile: bottom tab bar ────────────────────────────────────────────── */
  @media (max-width: 768px) {
    .shell { flex-direction: column; }

    .sidebar {
      position: fixed;
      bottom: 0;
      left: 0;
      right: 0;
      width: 100%;
      height: auto;
      flex-direction: row;
      border-right: none;
      border-top: 1px solid var(--border);
      z-index: 900;
      padding: 0;
    }

    .brand { display: none; }
    .sidebar-foot { display: none; }

    nav {
      display: flex;
      flex-direction: row;
      justify-content: space-around;
      align-items: center;
      width: 100%;
      padding: 0;
      flex: unset;
    }

    .nav-link {
      flex-direction: column;
      gap: 0.2rem;
      padding: 0.5rem 0.3rem 0.45rem;
      font-size: 0.6rem;
      border-radius: 0;
      flex: 1;
      justify-content: center;
      text-align: center;
    }
    .nav-link svg { width: 20px; height: 20px; }

    .main {
      padding-bottom: 60px;
      width: 100%;
    }
    .main.has-audio { padding-bottom: 160px; }

    .switcher-panel {
      width: calc(100% - 2rem);
      max-width: 320px;
    }
  }
</style>
