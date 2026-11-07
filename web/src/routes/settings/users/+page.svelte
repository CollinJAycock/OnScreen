<script lang="ts">
  import { onMount } from 'svelte';
  import LabelEditor from '$lib/components/LabelEditor.svelte';
  import {
    api, userApi, inviteApi, profileApi, settingsApi, labelApi,
    type User, type UserMeta, type UserLibraryAccess, type WatchLimitInfo,
    type RequestPermissions, type RequestSettings
  } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import SignedInDevices from '$lib/components/SignedInDevices.svelte';
  // Aliased: `t` is a local name in a few functions below.
  import { t as translate, i18n } from '$lib/i18n/index.svelte';
  import {
    parseUserQuota, parseDefaultQuota, parseWindowDays, quotaDefaultsFrom,
    quotaPlaceholder, quotaInputValue, limitsSummary, windowHint,
    type QuotaDefaults
  } from './requestLimits';

  let loading = true;
  let error = '';
  let users: User[] = [];
  let currentUser: UserMeta | null = null;

  // Per-row actions overflow menu (kebab). Holds the id of the user whose
  // menu is open, plus fixed-position anchor coords so the dropdown escapes
  // the table-wrap's overflow:hidden clipping and isn't cut off on the last row.
  let menuOpen: string | null = null;
  let menuTop = 0;
  let menuRight = 0;

  // Create user form
  let showCreate = false;
  let createUsername = '';
  let createEmail = '';
  let createPassword = '';
  let creating = false;

  // Delete confirmation
  let deleteTarget: User | null = null;
  let deleting = false;

  // Password reset
  let resetTarget: User | null = null;
  let resetPassword = '';
  let resetting = false;

  // Library access
  let librariesTarget: User | null = null;
  let libraryAccess: UserLibraryAccess[] = [];
  let librariesLoading = false;
  let librariesSaving = false;

  // Watch limits (parental controls)
  let limitsTarget: User | null = null;
  let limitInfo: WatchLimitInfo | null = null;
  let limitsLoading = false;
  let limitsSaving = false;
  let capEnabled = false;
  let capMinutes = 120;
  let hoursEnabled = false;
  let startTime = '08:00';
  let endTime = '20:00';

  // Label restriction (blocked / allowed labels). Applies to the user and
  // every managed profile beneath it; saving signs them out.
  let labelsTarget: User | null = null;
  let labelsSaving = false;
  let blockedLabels: string[] = [];
  let allowedLabels: string[] = [];
  let labelSuggestions: string[] = [];

  // Streaming caps (admin-enforced per-user)
  let capsTarget: User | null = null;
  let capsLoading = false;
  let capsSaving = false;
  let concurrentEnabled = false;
  let concurrentVal = 2;
  let bitrateEnabled = false;
  let bitrateMbps = 8;
  // Internet-only cap: held to streams from outside the home network.
  let remoteBitrateEnabled = false;
  let remoteBitrateMbps = 4;

  // Signed-in devices dialog.
  let devicesTarget: User | null = null;

  // Request auto-approval. Per-user toggles save on click like the admin
  // toggle; ids of rows mid-save block a second click racing the first PUT.
  let permsSaving = new Set<string>();
  // Managed profiles' content-rating ceilings by user id. A rated profile's
  // requests always go to the queue whatever its toggles say, so the row
  // needs the ceiling to explain that.
  let profileRatings: Record<string, string> = {};

  // Auto-approve defaults for accounts created from now on (server settings).
  let requestDefaults: RequestSettings | null = null;
  let defaultsError = '';
  let defaultsSaving = false;

  // Request limits. Per user: can-request + movie/TV quota overrides, edited
  // in a small expander under the row's auto-approve switches (one open at a
  // time). Server-wide: the quota defaults + window, in the New users block.
  let reqLimitsOpen: string | null = null;
  let reqLimitsMovies = '';
  let reqLimitsTV = '';
  let quotaDefaults: QuotaDefaults | null = null;
  let quotaDraft = { movies: '', tv: '', window: '' };
  let quotaSaving = false;

  // Invite flow
  let showInvite = false;
  let inviteEmail = '';
  let inviting = false;
  let inviteURL = '';
  let invites: Array<{ id: string; email: string | null; expires_at: string; used_at: string | null; created_at: string }> = [];
  let invitesLoading = false;
  let copied = false;

  onMount(async () => {
    currentUser = api.getUser();
    if (!currentUser) return;
    await Promise.all([loadUsers(), loadInvites(), loadProfileRatings(), loadRequestDefaults()]);
  });

  async function loadProfileRatings() {
    try {
      const next: Record<string, string> = {};
      for (const p of await profileApi.list() ?? []) {
        if (p.max_content_rating) next[p.id] = p.max_content_rating;
      }
      profileRatings = next;
    } catch {
      profileRatings = {};
    }
  }

  function ratingLimit(user: User): string | null {
    return user.max_content_rating || profileRatings[user.id] || null;
  }

  async function toggleAutoApprove(user: User, kind: 'movies' | 'tv') {
    if (permsSaving.has(user.id)) return;
    const perms: RequestPermissions = {
      auto_approve_movies: kind === 'movies' ? !user.auto_approve_movies : !!user.auto_approve_movies,
      auto_approve_tv: kind === 'tv' ? !user.auto_approve_tv : !!user.auto_approve_tv
    };
    const on = kind === 'movies' ? perms.auto_approve_movies : perms.auto_approve_tv;
    permsSaving = new Set(permsSaving).add(user.id);
    try {
      await userApi.setRequestPermissions(user.id, perms);
      users = users.map(u => (u.id === user.id ? { ...u, ...perms } : u));
      if (!on) {
        toast.success(kind === 'movies' ? translate('settings.users.moviesNeedApproval', { name: user.username }) : translate('settings.users.tvNeedApproval', { name: user.username }));
      } else if (ratingLimit(user)) {
        // Stored, but the server ignores it while the ceiling is set.
        toast.success(translate('settings.users.savedRatingLimited', { name: user.username }));
      } else {
        toast.success(kind === 'movies' ? translate('settings.users.moviesAutoApproved', { name: user.username }) : translate('settings.users.tvAutoApproved', { name: user.username }));
      }
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.users.permsFailed'));
    } finally {
      const next = new Set(permsSaving);
      next.delete(user.id);
      permsSaving = next;
    }
  }

  async function loadRequestDefaults() {
    defaultsError = '';
    try {
      const s = await settingsApi.get();
      requestDefaults = {
        default_auto_approve_movies: !!s.requests?.default_auto_approve_movies,
        default_auto_approve_tv: !!s.requests?.default_auto_approve_tv
      };
      setQuotaDefaults(quotaDefaultsFrom(s.requests));
    } catch (e: unknown) {
      requestDefaults = null;
      quotaDefaults = null;
      defaultsError = e instanceof Error ? e.message : translate('settings.users.defaultsLoadFailed');
    }
  }

  async function toggleDefault(key: keyof RequestSettings) {
    if (!requestDefaults || defaultsSaving) return;
    const next: RequestSettings = { ...requestDefaults, [key]: !requestDefaults[key] };
    defaultsSaving = true;
    try {
      await settingsApi.update({ requests: next });
      requestDefaults = next;
      toast.success(translate('settings.users.defaultsSaved'));
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.users.defaultsSaveFailed'));
    } finally {
      defaultsSaving = false;
    }
  }

  function setQuotaDefaults(d: QuotaDefaults) {
    quotaDefaults = d;
    quotaDraft = { movies: String(d.quota_movies), tv: String(d.quota_tv), window: String(d.quota_window_days) };
  }

  async function saveQuotaDefaults() {
    if (!requestDefaults || quotaSaving) return;
    const m = parseDefaultQuota(quotaDraft.movies, translate('settings.users.movies'));
    const t = parseDefaultQuota(quotaDraft.tv, translate('settings.users.tv'));
    const w = parseWindowDays(quotaDraft.window);
    for (const r of [m, t, w]) {
      if (!r.ok) { toast.error(r.error); return; }
    }
    if (!m.ok || !t.ok || !w.ok) return;
    const next: QuotaDefaults = { quota_movies: m.value, quota_tv: t.value, quota_window_days: w.value };
    quotaSaving = true;
    try {
      await settingsApi.update({ requests: { ...requestDefaults, ...next } });
      setQuotaDefaults(next);
      toast.success(translate('settings.users.quotaSaved'));
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.users.quotaSaveFailed'));
    } finally {
      quotaSaving = false;
    }
  }

  function toggleRequestLimits(user: User) {
    if (reqLimitsOpen === user.id) { reqLimitsOpen = null; return; }
    reqLimitsOpen = user.id;
    reqLimitsMovies = quotaInputValue(user.request_quota_movies);
    reqLimitsTV = quotaInputValue(user.request_quota_tv);
  }

  async function savePolicy(user: User, perms: RequestPermissions, patch: Partial<User>, done: string): Promise<boolean> {
    if (permsSaving.has(user.id)) return false;
    permsSaving = new Set(permsSaving).add(user.id);
    try {
      await userApi.setRequestPermissions(user.id, perms);
      users = users.map(u => (u.id === user.id ? { ...u, ...patch } : u));
      toast.success(done);
      return true;
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.users.permsFailed'));
      return false;
    } finally {
      const next = new Set(permsSaving);
      next.delete(user.id);
      permsSaving = next;
    }
  }

  async function toggleCanRequest(user: User) {
    const on = user.can_request === false; // flipping: off → on, on → off
    await savePolicy(user, { can_request: on }, { can_request: on },
      on ? translate('settings.users.canRequestAgain', { name: user.username }) : translate('settings.users.canRequestNoMore', { name: user.username }));
  }

  async function saveRequestLimits(user: User) {
    const m = parseUserQuota(reqLimitsMovies, translate('settings.users.movies'));
    const t = parseUserQuota(reqLimitsTV, translate('settings.users.tv'));
    if (!m.ok) { toast.error(m.error); return; }
    if (!t.ok) { toast.error(t.error); return; }
    const ok = await savePolicy(user,
      { quota_movies: m.value, quota_tv: t.value },
      { request_quota_movies: m.value, request_quota_tv: t.value },
      translate('settings.users.userQuotaSaved', { name: user.username }));
    if (ok) reqLimitsOpen = null;
  }

  async function loadInvites() {
    invitesLoading = true;
    try {
      invites = await inviteApi.list() ?? [];
    } catch {
      invites = [];
    } finally {
      invitesLoading = false;
    }
  }

  async function createInvite() {
    inviting = true;
    inviteURL = '';
    copied = false;
    try {
      const result = await inviteApi.create(inviteEmail || undefined);
      inviteURL = result.invite_url;
      toast.success(translate('settings.users.inviteCreated'));
      await loadInvites();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.users.inviteCreateFailed'));
    } finally {
      inviting = false;
    }
  }

  async function deleteInvite(id: string) {
    try {
      await inviteApi.del(id);
      toast.success(translate('settings.users.inviteDeleted'));
      await loadInvites();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.users.inviteDeleteFailed'));
    }
  }

  function copyInviteURL() {
    navigator.clipboard.writeText(inviteURL);
    copied = true;
    setTimeout(() => copied = false, 2000);
  }

  function formatExpiry(dateStr: string): string {
    const d = new Date(dateStr);
    const now = new Date();
    if (d < now) return translate('settings.users.expired');
    return formatDate(dateStr);
  }

  async function loadUsers() {
    loading = true;
    error = '';
    try {
      const result = await userApi.list();
      users = result.items;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : translate('settings.users.loadFailed');
    } finally {
      loading = false;
    }
  }

  async function createUser() {
    if (!createUsername.trim() || !createPassword) return;
    creating = true;
    error = '';
    try {
      const name = createUsername.trim();
      await userApi.create(name, createPassword, createEmail || undefined);
      createUsername = '';
      createEmail = '';
      createPassword = '';
      showCreate = false;
      toast.success(translate('settings.users.userCreated', { name }));
      await loadUsers();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.users.createFailed'));
    } finally {
      creating = false;
    }
  }

  async function deleteUser() {
    if (!deleteTarget) return;
    deleting = true;
    error = '';
    try {
      const name = deleteTarget.username;
      await userApi.del(deleteTarget.id);
      deleteTarget = null;
      toast.success(translate('settings.users.userDeleted', { name }));
      await loadUsers();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.users.deleteFailed'));
    } finally {
      deleting = false;
    }
  }

  async function toggleAdmin(user: User) {
    error = '';
    try {
      await userApi.setAdmin(user.id, !user.is_admin);
      toast.success(!user.is_admin ? translate('settings.users.nowAdmin', { name: user.username }) : translate('settings.users.nowRegular', { name: user.username }));
      await loadUsers();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.users.updateFailed'));
    }
  }

  async function handleResetPassword() {
    if (!resetTarget || !resetPassword) return;
    resetting = true;
    try {
      const name = resetTarget.username;
      await userApi.resetPassword(resetTarget.id, resetPassword);
      toast.success(translate('settings.users.passwordReset', { name }));
      resetTarget = null;
      resetPassword = '';
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.users.resetFailed'));
    } finally {
      resetting = false;
    }
  }

  function isSelf(user: User): boolean {
    return currentUser?.user_id === user.id;
  }

  function viewAsUser(user: User) {
    // Set the per-tab impersonation flag, then full-reload to home so
    // every cached page state re-fetches under the target's claims and
    // the layout's banner picks up the new sessionStorage value on
    // mount.
    api.setViewAs({ id: user.id, username: user.username });
    window.location.href = '/';
  }

  function toggleRowMenu(e: MouseEvent, user: User) {
    if (menuOpen === user.id) { menuOpen = null; return; }
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    menuTop = r.bottom + 6;
    menuRight = window.innerWidth - r.right;
    menuOpen = user.id;
  }

  function runRowAction(fn: (u: User) => void, user: User) {
    menuOpen = null;
    fn(user);
  }

  async function openLibraries(user: User) {
    librariesTarget = user;
    librariesLoading = true;
    libraryAccess = [];
    try {
      libraryAccess = await userApi.getLibraries(user.id) ?? [];
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.users.librariesLoadFailed'));
      librariesTarget = null;
    } finally {
      librariesLoading = false;
    }
  }

  async function saveLibraries() {
    if (!librariesTarget) return;
    librariesSaving = true;
    try {
      const ids = libraryAccess.filter(l => l.enabled).map(l => l.library_id);
      await userApi.setLibraries(librariesTarget.id, ids);
      toast.success(translate('settings.users.librariesSaved', { name: librariesTarget.username }));
      librariesTarget = null;
      libraryAccess = [];
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.users.librariesSaveFailed'));
    } finally {
      librariesSaving = false;
    }
  }

  function toggleLibrary(libraryId: string) {
    libraryAccess = libraryAccess.map(l =>
      l.library_id === libraryId ? { ...l, enabled: !l.enabled } : l
    );
  }

  // Minutes-from-midnight ⇄ "HH:MM" for the allowed-hours time inputs.
  function minutesToTime(min: number): string {
    const h = Math.floor(min / 60);
    const m = min % 60;
    return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`;
  }
  function timeToMinutes(t: string): number {
    const [h, m] = t.split(':').map(Number);
    return (h || 0) * 60 + (m || 0);
  }

  async function openLimits(user: User) {
    limitsTarget = user;
    limitsLoading = true;
    limitInfo = null;
    try {
      const info = await userApi.getWatchLimit(user.id);
      limitInfo = info;
      capEnabled = info.daily_limit_minutes !== null;
      capMinutes = info.daily_limit_minutes ?? 120;
      hoursEnabled = info.allowed_start_minute !== null && info.allowed_end_minute !== null;
      startTime = minutesToTime(info.allowed_start_minute ?? 480); // 08:00
      endTime = minutesToTime(info.allowed_end_minute ?? 1200);    // 20:00
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.users.watchLoadFailed'));
      limitsTarget = null;
    } finally {
      limitsLoading = false;
    }
  }

  async function saveLimits() {
    if (!limitsTarget) return;
    if (capEnabled && (!Number.isFinite(capMinutes) || capMinutes < 0)) {
      toast.error(translate('settings.users.dailyLimitInvalid'));
      return;
    }
    limitsSaving = true;
    try {
      await userApi.setWatchLimit(limitsTarget.id, {
        daily_limit_minutes: capEnabled ? Math.round(capMinutes) : null,
        allowed_start_minute: hoursEnabled ? timeToMinutes(startTime) : null,
        allowed_end_minute: hoursEnabled ? timeToMinutes(endTime) : null
      });
      toast.success(translate('settings.users.watchSaved', { name: limitsTarget.username }));
      limitsTarget = null;
      limitInfo = null;
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.users.watchSaveFailed'));
    } finally {
      limitsSaving = false;
    }
  }

  async function openLabels(user: User) {
    labelsTarget = user;
    blockedLabels = [...(user.blocked_labels ?? [])];
    allowedLabels = [...(user.allowed_labels ?? [])];
    try {
      labelSuggestions = (await labelApi.list()).map((l) => l.label);
    } catch {
      labelSuggestions = [];
    }
  }

  async function saveLabels() {
    if (!labelsTarget) return;
    const target = labelsTarget;
    labelsSaving = true;
    try {
      const saved = await userApi.setLabelRestrictions(target.id, {
        blocked_labels: blockedLabels,
        allowed_labels: allowedLabels
      });
      users = users.map((u) => u.id === target.id
        ? { ...u, blocked_labels: saved.blocked_labels, allowed_labels: saved.allowed_labels }
        : u);
      toast.success(translate('settings.users.labelsSaved', { name: target.username }));
      labelsTarget = null;
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.users.labelsSaveFailed'));
    } finally {
      labelsSaving = false;
    }
  }

  async function openCaps(user: User) {
    capsTarget = user;
    capsLoading = true;
    try {
      const c = await userApi.getStreamingLimits(user.id);
      concurrentEnabled = c.max_concurrent_streams !== null;
      concurrentVal = c.max_concurrent_streams ?? 2;
      bitrateEnabled = c.max_stream_bitrate_kbps !== null;
      bitrateMbps = c.max_stream_bitrate_kbps != null
        ? Math.round((c.max_stream_bitrate_kbps / 1000) * 10) / 10
        : 8;
      remoteBitrateEnabled = c.max_remote_stream_bitrate_kbps != null;
      remoteBitrateMbps = c.max_remote_stream_bitrate_kbps != null
        ? Math.round((c.max_remote_stream_bitrate_kbps / 1000) * 10) / 10
        : 4;
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.users.capsLoadFailed'));
      capsTarget = null;
    } finally {
      capsLoading = false;
    }
  }

  async function saveCaps() {
    if (!capsTarget) return;
    capsSaving = true;
    try {
      await userApi.setStreamingLimits(capsTarget.id, {
        max_concurrent_streams: concurrentEnabled ? Math.max(1, Math.round(concurrentVal)) : null,
        max_stream_bitrate_kbps: bitrateEnabled ? Math.max(1, Math.round(bitrateMbps * 1000)) : null,
        max_remote_stream_bitrate_kbps: remoteBitrateEnabled ? Math.max(1, Math.round(remoteBitrateMbps * 1000)) : null
      });
      toast.success(translate('settings.users.capsSaved', { name: capsTarget.username }));
      capsTarget = null;
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : translate('settings.users.capsSaveFailed'));
    } finally {
      capsSaving = false;
    }
  }

  function formatDate(dateStr: string): string {
    try {
      return new Date(dateStr).toLocaleDateString(undefined, {
        year: 'numeric', month: 'short', day: 'numeric'
      });
    } catch {
      return dateStr;
    }
  }
</script>

<svelte:head><title>{$i18n('settings.users.pageTitle')}</title></svelte:head>

<svelte:window
  on:click={() => (menuOpen = null)}
  on:scroll={() => (menuOpen = null)}
  on:resize={() => (menuOpen = null)}
  on:keydown={(e) => { if (e.key === 'Escape') menuOpen = null; }}
/>

<div class="page">
  <div class="header">
    <div class="header-actions">
      <button class="btn-invite" on:click={() => { showInvite = !showInvite; inviteURL = ''; inviteEmail = ''; copied = false; }}>
        {showInvite ? $i18n('settings.users.cancel') : $i18n('settings.users.inviteUser')}
      </button>
      <button class="btn-create" on:click={() => showCreate = !showCreate}>
        {showCreate ? $i18n('settings.users.cancel') : $i18n('settings.users.createUser')}
      </button>
    </div>
  </div>

  {#if error}
    <div class="banner error">{error}</div>
  {/if}

  {#if showInvite}
    <div class="create-form">
      <form on:submit|preventDefault={createInvite}>
        <div class="field-row">
          <div class="field" style="flex:2">
            <label for="invite-email">{$i18n('settings.users.emailOptional')}</label>
            <input
              id="invite-email"
              type="email"
              bind:value={inviteEmail}
              placeholder="user@example.com"
              autocomplete="off"
            />
          </div>
          <button type="submit" class="btn-save" disabled={inviting}>
            {inviting ? $i18n('settings.users.creating') : $i18n('settings.users.createInvite')}
          </button>
        </div>
      </form>
      {#if inviteURL}
        <div class="invite-url-box">
          <label for="invite-url-input">{$i18n('settings.users.inviteLink')}</label>
          <div class="url-row">
            <input id="invite-url-input" type="text" readonly value={inviteURL} class="url-input" />
            <button class="btn-copy" on:click={copyInviteURL}>
              {copied ? $i18n('settings.users.copied') : $i18n('settings.users.copy')}
            </button>
          </div>
          <p class="url-hint">{$i18n('settings.users.inviteHint', { days: 7 })}</p>
        </div>
      {/if}
    </div>
  {/if}

  {#if showCreate}
    <form class="create-form" on:submit|preventDefault={createUser}>
      <div class="field-row">
        <div class="field">
          <label for="new-username">{$i18n('settings.users.username')}</label>
          <input
            id="new-username"
            type="text"
            bind:value={createUsername}
            placeholder={$i18n('settings.users.username')}
            autocomplete="off"
            required
          />
        </div>
        <div class="field">
          <label for="new-email">{$i18n('settings.users.email')}</label>
          <input
            id="new-email"
            type="email"
            bind:value={createEmail}
            placeholder={$i18n('settings.users.optional')}
            autocomplete="off"
          />
        </div>
        <div class="field">
          <label for="new-password">{$i18n('settings.users.password')}</label>
          <input
            id="new-password"
            type="password"
            bind:value={createPassword}
            placeholder={$i18n('settings.users.password')}
            autocomplete="new-password"
            required
          />
        </div>
        <button type="submit" class="btn-save" disabled={creating || !createUsername.trim() || !createPassword}>
          {creating ? $i18n('settings.users.creating') : $i18n('settings.users.create')}
        </button>
      </div>
    </form>
  {/if}

  {#if loading}
    <div class="skeleton-block"></div>
  {:else if users.length === 0}
    <div class="empty">{$i18n('settings.users.noUsers')}</div>
  {:else}
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>{$i18n('settings.users.username')}</th>
            <th>{$i18n('settings.users.role')}</th>
            <th>{$i18n('settings.users.requests')}</th>
            <th>{$i18n('settings.users.created')}</th>
            <th class="actions-col"></th>
          </tr>
        </thead>
        <tbody>
          {#each users as user (user.id)}
            <tr>
              <td class="username-cell">
                {user.username}
                {#if isSelf(user)}<span class="you-badge">{$i18n('settings.users.you')}</span>{/if}
              </td>
              <td>
                <button
                  class="admin-toggle"
                  class:active={user.is_admin}
                  disabled={isSelf(user)}
                  title={isSelf(user) ? $i18n('settings.users.cannotChangeSelf') : (user.is_admin ? $i18n('settings.users.removeAdmin') : $i18n('settings.users.makeAdmin'))}
                  on:click={() => toggleAdmin(user)}
                >
                  <span class="toggle-track">
                    <span class="toggle-thumb"></span>
                  </span>
                  <span class="toggle-label">{user.is_admin ? $i18n('common.admin') : $i18n('settings.users.roleUser')}</span>
                </button>
              </td>
              <td>
                {#if user.is_admin}
                  <span class="perm-note">{$i18n('settings.users.adminsAlwaysApproved')}</span>
                {:else}
                  {@const limited = ratingLimit(user)}
                  <!-- A rating-limited user's toggles stay clickable: the
                       server stores them, and they apply once the ceiling
                       is lifted. Dimmed + the note say they do nothing now. -->
                  <div class="perm-toggles" class:limited={!!limited}>
                    <button
                      class="perm-toggle"
                      class:active={user.auto_approve_movies}
                      role="switch"
                      aria-checked={!!user.auto_approve_movies}
                      aria-label={$i18n('settings.users.autoMoviesFor', { name: user.username })}
                      aria-describedby={limited ? `perm-note-${user.id}` : undefined}
                      disabled={permsSaving.has(user.id)}
                      on:click={() => toggleAutoApprove(user, 'movies')}
                    >
                      <span class="toggle-track"><span class="toggle-thumb"></span></span>
                      <span class="toggle-label">{$i18n('settings.users.movies')}</span>
                    </button>
                    <button
                      class="perm-toggle"
                      class:active={user.auto_approve_tv}
                      role="switch"
                      aria-checked={!!user.auto_approve_tv}
                      aria-label={$i18n('settings.users.autoTvFor', { name: user.username })}
                      aria-describedby={limited ? `perm-note-${user.id}` : undefined}
                      disabled={permsSaving.has(user.id)}
                      on:click={() => toggleAutoApprove(user, 'tv')}
                    >
                      <span class="toggle-track"><span class="toggle-thumb"></span></span>
                      <span class="toggle-label">{$i18n('settings.users.tv')}</span>
                    </button>
                  </div>
                  {#if limited}
                    <span class="perm-note" id="perm-note-{user.id}" title={$i18n('settings.users.ratingLimitTitle', { rating: limited })}>{$i18n('settings.users.ratingLimited')}</span>
                  {/if}
                  <!-- Can-request + quota overrides, collapsed to a one-line
                       summary so the table stays scannable. -->
                  <button
                    class="limits-btn"
                    class:off={user.can_request === false}
                    aria-expanded={reqLimitsOpen === user.id}
                    aria-controls="limits-{user.id}"
                    aria-label={$i18n('settings.users.requestLimitsFor', { name: user.username })}
                    on:click={() => toggleRequestLimits(user)}
                  >{limitsSummary(user, $i18n)} <span class="chev" aria-hidden="true">{reqLimitsOpen === user.id ? '▴' : '▾'}</span></button>
                  {#if reqLimitsOpen === user.id}
                    <div class="limits-panel" id="limits-{user.id}">
                      <button
                        class="perm-toggle"
                        class:active={user.can_request !== false}
                        role="switch"
                        aria-checked={user.can_request !== false}
                        aria-label={$i18n('settings.users.canRequestFor', { name: user.username })}
                        disabled={permsSaving.has(user.id)}
                        on:click={() => toggleCanRequest(user)}
                      >
                        <span class="toggle-track"><span class="toggle-thumb"></span></span>
                        <span class="toggle-label">{$i18n('settings.users.canRequest')}</span>
                      </button>
                      <div class="quota-row">
                        <label class="quota-field">
                          <span>{$i18n('settings.users.movies')}</span>
                          <input
                            type="text"
                            inputmode="numeric"
                            bind:value={reqLimitsMovies}
                            placeholder={quotaPlaceholder(quotaDefaults?.quota_movies, $i18n)}
                            aria-label={$i18n('settings.users.movieLimitFor', { name: user.username })}
                          />
                        </label>
                        <label class="quota-field">
                          <span>{$i18n('settings.users.tv')}</span>
                          <input
                            type="text"
                            inputmode="numeric"
                            bind:value={reqLimitsTV}
                            placeholder={quotaPlaceholder(quotaDefaults?.quota_tv, $i18n)}
                            aria-label={$i18n('settings.users.tvLimitFor', { name: user.username })}
                          />
                        </label>
                      </div>
                      <p class="limits-hint">{windowHint(quotaDefaults?.quota_window_days ?? 7, $i18n)}</p>
                      <button class="btn-limits-save" disabled={permsSaving.has(user.id)} on:click={() => saveRequestLimits(user)}>{$i18n('settings.users.saveLimits')}</button>
                    </div>
                  {/if}
                {/if}
              </td>
              <td class="date-cell">{formatDate(user.created_at)}</td>
              <td class="actions-cell">
                <button
                  class="kebab"
                  class:open={menuOpen === user.id}
                  aria-label={$i18n('settings.users.actionsFor', { name: user.username })}
                  aria-haspopup="menu"
                  aria-expanded={menuOpen === user.id}
                  on:click|stopPropagation={(e) => toggleRowMenu(e, user)}
                >⋯</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}

  <!-- Per-row actions menu (fixed-positioned to escape table clipping) -->
  {#if menuOpen}
    {@const mu = users.find((u) => u.id === menuOpen)}
    {#if mu}
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="actions-menu" role="menu" tabindex="-1" style="top:{menuTop}px; right:{menuRight}px;" on:click|stopPropagation>
        {#if !mu.is_admin}
          <button class="menu-item" role="menuitem" on:click={() => runRowAction(openLibraries, mu)}>{$i18n('settings.users.menuLibraries')}</button>
          <button class="menu-item" role="menuitem" on:click={() => runRowAction(openLimits, mu)}>{$i18n('settings.users.menuWatch')}</button>
          <button class="menu-item" role="menuitem" on:click={() => runRowAction(openCaps, mu)}>{$i18n('settings.users.menuCaps')}</button>
          <button class="menu-item" role="menuitem" on:click={() => runRowAction(openLabels, mu)}>{$i18n('settings.users.menuLabels')}</button>
          <div class="menu-sep"></div>
        {/if}
        {#if !isSelf(mu)}
          <button class="menu-item" role="menuitem" on:click={() => runRowAction(viewAsUser, mu)}>{$i18n('settings.users.menuViewAs')}</button>
        {/if}
        <button class="menu-item" role="menuitem" on:click={() => runRowAction((u) => { devicesTarget = u; }, mu)}>{$i18n('settings.users.menuDevices')}</button>
        <button class="menu-item" role="menuitem" on:click={() => runRowAction((u) => { resetTarget = u; resetPassword = ''; }, mu)}>{$i18n('settings.users.menuReset')}</button>
        {#if !isSelf(mu)}
          <div class="menu-sep"></div>
          <button class="menu-item danger" role="menuitem" on:click={() => runRowAction((u) => { deleteTarget = u; }, mu)}>{$i18n('settings.users.menuDelete')}</button>
        {/if}
      </div>
    {/if}
  {/if}

  <!-- Auto-approve defaults for new accounts -->
  <section class="defaults-section">
    <h2>{$i18n('settings.users.newUsers')}</h2>
    {#if requestDefaults}
      <div class="defaults-card">
        <button
          class="perm-toggle"
          class:active={requestDefaults.default_auto_approve_movies}
          role="switch"
          aria-checked={requestDefaults.default_auto_approve_movies}
          disabled={defaultsSaving}
          on:click={() => toggleDefault('default_auto_approve_movies')}
        >
          <span class="toggle-track"><span class="toggle-thumb"></span></span>
          <span class="toggle-label">{$i18n('settings.users.autoMovies')}</span>
        </button>
        <button
          class="perm-toggle"
          class:active={requestDefaults.default_auto_approve_tv}
          role="switch"
          aria-checked={requestDefaults.default_auto_approve_tv}
          disabled={defaultsSaving}
          on:click={() => toggleDefault('default_auto_approve_tv')}
        >
          <span class="toggle-track"><span class="toggle-thumb"></span></span>
          <span class="toggle-label">{$i18n('settings.users.autoTv')}</span>
        </button>
        <p class="defaults-hint">{$i18n('settings.users.defaultsHint')}</p>
      </div>
      {#if quotaDefaults}
        <div class="defaults-card quota-defaults">
          <h3 class="defaults-sub">{$i18n('settings.users.requestLimits')}</h3>
          <div class="quota-row">
            <label class="quota-field">
              <span>{$i18n('settings.users.movies')}</span>
              <input type="text" inputmode="numeric" bind:value={quotaDraft.movies} aria-label={$i18n('settings.users.defaultMovieLimit')} />
            </label>
            <label class="quota-field">
              <span>{$i18n('settings.users.tv')}</span>
              <input type="text" inputmode="numeric" bind:value={quotaDraft.tv} aria-label={$i18n('settings.users.defaultTvLimit')} />
            </label>
            <label class="quota-field">
              <span>{$i18n('settings.users.windowDays')}</span>
              <input type="text" inputmode="numeric" bind:value={quotaDraft.window} aria-label={$i18n('settings.users.windowLabel')} />
            </label>
          </div>
          <p class="defaults-hint">{$i18n('settings.users.quotaHint')}</p>
          <button class="btn-limits-save" disabled={quotaSaving} on:click={saveQuotaDefaults}>{$i18n('settings.users.saveLimits')}</button>
        </div>
      {/if}
    {:else if defaultsError}
      <div class="banner error">{defaultsError}</div>
    {/if}
  </section>

  <!-- Pending invites -->
  {#if invites.length > 0}
    <div class="invites-section">
      <h2>{$i18n('settings.users.invites')}</h2>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>{$i18n('settings.users.email')}</th>
              <th>{$i18n('settings.users.status')}</th>
              <th>{$i18n('settings.users.created')}</th>
              <th class="actions-col"></th>
            </tr>
          </thead>
          <tbody>
            {#each invites as invite (invite.id)}
              <tr>
                <td class="username-cell">{invite.email ?? $i18n('settings.users.noEmail')}</td>
                <td>
                  {#if invite.used_at}
                    <span class="status-used">{$i18n('settings.users.used')}</span>
                  {:else if new Date(invite.expires_at) < new Date()}
                    <span class="status-expired">{$i18n('settings.users.expired')}</span>
                  {:else}
                    <span class="status-pending">{$i18n('settings.users.pending')}</span>
                  {/if}
                </td>
                <td class="date-cell">{formatDate(invite.created_at)}</td>
                <td class="actions-cell">
                  {#if !invite.used_at}
                    <button class="btn-delete" on:click={() => deleteInvite(invite.id)}>{$i18n('settings.users.revoke')}</button>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>
  {/if}

  <!-- Delete confirmation dialog -->
  {#if deleteTarget}
    <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
    <div class="overlay" on:click={() => deleteTarget = null}>
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="dialog" on:click|stopPropagation>
        <h2>{$i18n('settings.users.deleteTitle')}</h2>
        <p>{#each $i18n.rich('settings.users.deleteConfirm', { name: deleteTarget.username }) as seg}{#if seg.param === 'name'}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}</p>
        <div class="dialog-actions">
          <button class="btn-cancel" on:click={() => deleteTarget = null}>{$i18n('settings.users.cancel')}</button>
          <button class="btn-confirm-delete" disabled={deleting} on:click={deleteUser}>
            {deleting ? $i18n('settings.users.deleting') : $i18n('settings.users.delete')}
          </button>
        </div>
      </div>
    </div>
  {/if}

  <!-- Library access dialog -->
  {#if librariesTarget}
    <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
    <div class="overlay" on:click={() => librariesTarget = null}>
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="dialog" on:click|stopPropagation>
        <h2>{$i18n('settings.users.librariesTitle')}</h2>
        <p>{#each $i18n.rich('settings.users.librariesBody', { name: librariesTarget.username }) as seg}{#if seg.param === 'name'}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}</p>
        {#if librariesLoading}
          <div class="skeleton-block" style="height:120px;margin-bottom:1rem;"></div>
        {:else if libraryAccess.length === 0}
          <div class="empty" style="padding:1rem 0;">{$i18n('settings.users.noLibraries')}</div>
        {:else}
          <ul class="library-list">
            {#each libraryAccess as lib (lib.library_id)}
              <li>
                <label class="library-row">
                  <input
                    type="checkbox"
                    checked={lib.enabled}
                    on:change={() => toggleLibrary(lib.library_id)}
                  />
                  <span class="library-name">{lib.name}</span>
                  <span class="library-type">{lib.type}</span>
                </label>
              </li>
            {/each}
          </ul>
        {/if}
        <div class="dialog-actions">
          <button class="btn-cancel" on:click={() => librariesTarget = null}>{$i18n('settings.users.cancel')}</button>
          <button class="btn-save" disabled={librariesLoading || librariesSaving} on:click={saveLibraries}>
            {librariesSaving ? $i18n('settings.users.saving') : $i18n('settings.users.save')}
          </button>
        </div>
      </div>
    </div>
  {/if}

  <!-- Watch limits (parental) dialog -->
  {#if limitsTarget}
    <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
    <div class="overlay" on:click={() => limitsTarget = null}>
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="dialog" on:click|stopPropagation>
        <h2>{$i18n('settings.users.watchTitle')}</h2>
        <p>{#each $i18n.rich('settings.users.watchBody', { name: limitsTarget.username }) as seg}{#if seg.param === 'name'}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}</p>
        {#if limitsLoading}
          <div class="skeleton-block" style="height:140px;margin-bottom:1rem;"></div>
        {:else}
          {#if limitInfo}
            <div class="usage-row">
              <span>{$i18n('settings.users.usedToday')}</span>
              <strong>{limitInfo.remaining_minutes != null ? $i18n('settings.users.usedLeft', { used: limitInfo.used_minutes_today, left: limitInfo.remaining_minutes }) : $i18n('settings.users.usedMinutes', { used: limitInfo.used_minutes_today })}</strong>
            </div>
          {/if}

          <div class="limit-section">
            <label class="limit-toggle">
              <input type="checkbox" bind:checked={capEnabled} />
              <span>{$i18n('settings.users.limitDaily')}</span>
            </label>
            {#if capEnabled}
              <div class="limit-field">
                <input type="number" min="0" max="1440" step="15" bind:value={capMinutes} />
                <span class="unit">{$i18n('settings.users.minutesPerDay')}</span>
              </div>
            {/if}
          </div>

          <div class="limit-section">
            <label class="limit-toggle">
              <input type="checkbox" bind:checked={hoursEnabled} />
              <span>{$i18n('settings.users.restrictHours')}</span>
            </label>
            {#if hoursEnabled}
              <div class="limit-field">
                <input type="time" bind:value={startTime} />
                <span class="unit">{$i18n('settings.users.timeTo')}</span>
                <input type="time" bind:value={endTime} />
              </div>
              <p class="limit-hint">{$i18n('settings.users.overnightHint')}</p>
            {/if}
          </div>
        {/if}
        <div class="dialog-actions">
          <button class="btn-cancel" on:click={() => limitsTarget = null}>{$i18n('settings.users.cancel')}</button>
          <button class="btn-save" disabled={limitsLoading || limitsSaving} on:click={saveLimits}>
            {limitsSaving ? $i18n('settings.users.saving') : $i18n('settings.users.save')}
          </button>
        </div>
      </div>
    </div>
  {/if}

  <!-- Label restriction (admin) dialog -->
  {#if labelsTarget}
    <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
    <div class="overlay" on:click={() => labelsTarget = null}>
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="dialog" role="dialog" aria-label={$i18n('settings.users.labelsAria')} tabindex="-1" on:click|stopPropagation>
        <h2>{$i18n('settings.users.labelsTitle')}</h2>
        <p>{#each $i18n.rich('settings.users.labelsBody', { name: labelsTarget.username }) as seg}{#if seg.param === 'name'}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}</p>
        <div class="limit-section">
          <span class="limit-label">{$i18n('settings.users.neverShow')}</span>
          <LabelEditor
            labels={blockedLabels}
            suggestions={labelSuggestions}
            disabled={labelsSaving}
            label={$i18n('settings.users.blockedLabels')}
            idSuffix="blocked"
            on:change={(e) => blockedLabels = e.detail.labels}
          />
        </div>
        <div class="limit-section">
          <span class="limit-label">{$i18n('settings.users.onlyShow')}</span>
          <LabelEditor
            labels={allowedLabels}
            suggestions={labelSuggestions}
            disabled={labelsSaving}
            label={$i18n('settings.users.allowedLabels')}
            idSuffix="allowed"
            on:change={(e) => allowedLabels = e.detail.labels}
          />
          <p class="limit-hint">{$i18n('settings.users.allowedHint')}</p>
        </div>
        <div class="dialog-actions">
          <button class="btn-cancel" on:click={() => labelsTarget = null}>{$i18n('settings.users.cancel')}</button>
          <button class="btn-save" disabled={labelsSaving} on:click={saveLabels}>
            {labelsSaving ? $i18n('settings.users.saving') : $i18n('settings.users.save')}
          </button>
        </div>
      </div>
    </div>
  {/if}

  <!-- Streaming caps (admin) dialog -->
  {#if capsTarget}
    <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
    <div class="overlay" on:click={() => capsTarget = null}>
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="dialog" on:click|stopPropagation>
        <h2>{$i18n('settings.users.capsTitle')}</h2>
        <p>{#each $i18n.rich('settings.users.capsBody', { name: capsTarget.username }) as seg}{#if seg.param === 'name'}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}</p>
        {#if capsLoading}
          <div class="skeleton-block" style="height:120px;margin-bottom:1rem;"></div>
        {:else}
          <div class="limit-section">
            <label class="limit-toggle">
              <input type="checkbox" bind:checked={concurrentEnabled} />
              <span>{$i18n('settings.users.limitConcurrent')}</span>
            </label>
            {#if concurrentEnabled}
              <div class="limit-field">
                <input type="number" min="1" max="50" step="1" bind:value={concurrentVal} />
                <span class="unit">{$i18n('settings.users.streamsAtOnce')}</span>
              </div>
            {/if}
          </div>

          <div class="limit-section">
            <label class="limit-toggle">
              <input type="checkbox" bind:checked={bitrateEnabled} />
              <span>{$i18n('settings.users.limitBitrate')}</span>
            </label>
            {#if bitrateEnabled}
              <div class="limit-field">
                <input type="number" min="0.5" max="120" step="0.5" bind:value={bitrateMbps} />
                <span class="unit">{$i18n('settings.users.mbpsMax')}</span>
              </div>
              <p class="limit-hint">{$i18n('settings.users.bitrateHint')}</p>
            {/if}
          </div>

          <div class="limit-section">
            <label class="limit-toggle">
              <input type="checkbox" bind:checked={remoteBitrateEnabled} />
              <span>{$i18n('settings.users.limitRemote')}</span>
            </label>
            {#if remoteBitrateEnabled}
              <div class="limit-field">
                <input type="number" min="0.5" max="120" step="0.5" bind:value={remoteBitrateMbps} />
                <span class="unit">{$i18n('settings.users.mbpsMaxAway')}</span>
              </div>
            {/if}
            <p class="limit-hint">{$i18n('settings.users.remoteHint')}</p>
          </div>
        {/if}
        <div class="dialog-actions">
          <button class="btn-cancel" on:click={() => capsTarget = null}>{$i18n('settings.users.cancel')}</button>
          <button class="btn-save" disabled={capsLoading || capsSaving} on:click={saveCaps}>
            {capsSaving ? $i18n('settings.users.saving') : $i18n('settings.users.save')}
          </button>
        </div>
      </div>
    </div>
  {/if}

  <!-- Signed-in devices (admin) dialog -->
  {#if devicesTarget}
    {@const target = devicesTarget}
    <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
    <div class="overlay" on:click={() => devicesTarget = null}>
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="dialog" on:click|stopPropagation>
        <h2>{$i18n('settings.users.devicesTitle')}</h2>
        <p>{#each $i18n.rich('settings.users.devicesBody', { name: target.username }) as seg}{#if seg.param === 'name'}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}</p>
        <SignedInDevices
          load={() => userApi.listSessions(target.id)}
          revoke={(sid) => userApi.revokeSession(target.id, sid)}
          revokeAll={() => isSelf(target) ? userApi.revokeMyOtherSessions() : userApi.revokeAllSessions(target.id)}
          revokeAllLabel={isSelf(target) ? $i18n('settings.users.signOutElsewhere') : $i18n('settings.users.signOutEverywhere')}
          revokeAllConfirm={isSelf(target)
            ? $i18n('settings.users.signOutElsewhereConfirm')
            : $i18n('settings.users.signOutEverywhereConfirm', { name: target.username })}
          keepCurrent={isSelf(target)}
          emptyText={$i18n('settings.users.notSignedIn')}
        />
        <div class="dialog-actions">
          <button class="btn-cancel" on:click={() => devicesTarget = null}>{$i18n('common.close')}</button>
        </div>
      </div>
    </div>
  {/if}

  <!-- Reset password dialog -->
  {#if resetTarget}
    <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
    <div class="overlay" on:click={() => resetTarget = null}>
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="dialog" on:click|stopPropagation>
        <h2>{$i18n('settings.users.resetTitle')}</h2>
        <p>{#each $i18n.rich('settings.users.resetBody', { name: resetTarget.username }) as seg}{#if seg.param === 'name'}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}</p>
        <form on:submit|preventDefault={handleResetPassword}>
          <input
            type="password"
            bind:value={resetPassword}
            placeholder={$i18n('settings.users.newPasswordPlaceholder', { min: 8 })}
            autocomplete="new-password"
            required
            minlength="8"
            style="width:100%;padding:0.5rem 0.7rem;border:1px solid #333;border-radius:6px;background:#0d0d14;color:#eee;font-size:0.9rem;margin:0.75rem 0;"
          />
          <div class="dialog-actions">
            <button type="button" class="btn-cancel" on:click={() => resetTarget = null}>{$i18n('settings.users.cancel')}</button>
            <button type="submit" class="btn-save" disabled={resetting || resetPassword.length < 8}>
              {resetting ? $i18n('settings.users.resetting') : $i18n('settings.users.resetButton')}
            </button>
          </div>
        </form>
      </div>
    </div>
  {/if}
</div>

<style>
  .page { max-width: 680px; }

  .header {
    display: flex; align-items: center; justify-content: space-between;
    margin-bottom: 1.75rem;
  }

  .banner {
    padding: 0.6rem 0.9rem;
    border-radius: 8px;
    font-size: 0.8rem;
    margin-bottom: 1.25rem;
  }
  .banner.error { background: rgba(248,113,113,0.1); border: 1px solid rgba(248,113,113,0.2); color: #fca5a5; }

  .skeleton-block {
    height: 120px; border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  .empty {
    color: var(--text-muted); font-size: 0.85rem;
    text-align: center; padding: 3rem 0;
  }

  /* Create form */
  .create-form {
    background: rgba(255,255,255,0.03);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1rem 1.25rem;
    margin-bottom: 1.25rem;
  }
  .field-row {
    display: flex; gap: 0.75rem; align-items: flex-end;
  }
  .field { display: flex; flex-direction: column; gap: 0.3rem; flex: 1; }
  label { font-size: 0.72rem; font-weight: 500; color: var(--text-muted); }
  input {
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.48rem 0.7rem;
    font-size: 0.85rem;
    color: var(--text-primary);
    font-family: inherit;
    transition: border-color 0.15s;
    width: 100%;
  }
  input:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
  ::placeholder { color: #2a2a3d; }

  /* Table */
  .table-wrap {
    border: 1px solid var(--border);
    border-radius: 10px;
    overflow: hidden;
  }
  table { width: 100%; border-collapse: collapse; }
  thead { background: rgba(255,255,255,0.03); }
  th {
    text-align: left; padding: 0.6rem 1rem;
    font-size: 0.68rem; font-weight: 600;
    text-transform: uppercase; letter-spacing: 0.07em;
    color: var(--text-muted); border-bottom: 1px solid var(--border);
  }
  td {
    padding: 0.7rem 1rem; font-size: 0.85rem; color: #ccccd8;
    border-bottom: 1px solid var(--input-bg);
  }
  tr:last-child td { border-bottom: none; }

  .username-cell { color: var(--text-primary); font-weight: 500; }
  .you-badge {
    display: inline-block; margin-left: 0.5rem;
    font-size: 0.62rem; font-weight: 600;
    text-transform: uppercase; letter-spacing: 0.05em;
    color: var(--accent); background: var(--accent-bg);
    border-radius: 4px; padding: 0.1rem 0.35rem;
    vertical-align: middle;
  }
  .date-cell { color: var(--text-muted); font-size: 0.8rem; }
  .actions-col { width: 44px; }
  .actions-cell { text-align: right; }

  /* Per-row overflow (kebab) menu */
  .kebab {
    width: 30px; height: 30px; padding: 0;
    display: inline-flex; align-items: center; justify-content: center;
    background: none; border: 1px solid var(--border-strong); border-radius: 7px;
    color: var(--text-muted); font-size: 1.05rem; line-height: 1; cursor: pointer;
    transition: background 0.15s, border-color 0.15s, color 0.15s;
  }
  .kebab:hover, .kebab.open {
    background: rgba(255,255,255,0.05); color: var(--text-primary); border-color: var(--accent);
  }
  .actions-menu {
    position: fixed; z-index: 200;
    min-width: 180px; padding: 0.35rem;
    background: #16161f; border: 1px solid var(--border-strong);
    border-radius: 10px; box-shadow: 0 12px 32px rgba(0,0,0,0.55);
    display: flex; flex-direction: column; gap: 0.05rem;
  }
  .menu-item {
    display: block; width: 100%; text-align: left;
    padding: 0.5rem 0.7rem; background: none; border: none; border-radius: 6px;
    color: #ccccd8; font-size: 0.82rem; font-family: inherit; cursor: pointer;
    white-space: nowrap; transition: background 0.12s, color 0.12s;
  }
  .menu-item:hover { background: rgba(255,255,255,0.06); color: var(--text-primary); }
  .menu-item.danger { color: #f87171; }
  .menu-item.danger:hover { background: rgba(248,113,113,0.12); }
  .menu-sep { height: 1px; background: var(--border); margin: 0.25rem 0.3rem; }

  /* Admin toggle (and the request auto-approve switches, which share it) */
  .admin-toggle, .perm-toggle {
    display: inline-flex; align-items: center; gap: 0.5rem;
    background: none; border: none; cursor: pointer; padding: 0.2rem 0;
    color: var(--text-muted); font-size: 0.8rem; font-family: inherit;
  }
  .admin-toggle:disabled, .perm-toggle:disabled { opacity: 0.4; cursor: not-allowed; }
  .toggle-track {
    display: inline-block; width: 32px; height: 18px;
    background: rgba(255,255,255,0.1); border-radius: 9px;
    position: relative; transition: background 0.2s;
  }
  .admin-toggle.active .toggle-track, .perm-toggle.active .toggle-track { background: var(--accent); }
  .toggle-thumb {
    position: absolute; top: 2px; left: 2px;
    width: 14px; height: 14px;
    background: #fff; border-radius: 50%;
    transition: transform 0.2s;
  }
  .admin-toggle.active .toggle-thumb, .perm-toggle.active .toggle-thumb { transform: translateX(14px); }
  .toggle-label { font-size: 0.78rem; }
  .admin-toggle.active .toggle-label, .perm-toggle.active .toggle-label { color: var(--accent-text); }

  /* Request auto-approve column */
  .perm-toggles { display: flex; flex-direction: column; align-items: flex-start; gap: 0.15rem; }
  .perm-toggles.limited { opacity: 0.45; }
  .perm-note {
    display: block; max-width: 170px;
    font-size: 0.7rem; line-height: 1.35; color: var(--text-muted);
  }
  .perm-toggles + .perm-note { margin-top: 0.25rem; }

  /* New-user defaults */
  .defaults-section { margin-top: 2rem; }
  .defaults-card {
    display: flex; flex-direction: column; align-items: flex-start; gap: 0.35rem;
    background: rgba(255,255,255,0.03);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 0.9rem 1.25rem;
  }
  .defaults-hint { font-size: 0.72rem; color: var(--text-muted); margin: 0.35rem 0 0; line-height: 1.45; }
  .quota-defaults { margin-top: 0.75rem; }
  .defaults-sub { font-size: 0.8rem; font-weight: 600; color: var(--text-secondary); margin: 0 0 0.2rem; }

  /* Request limits (per-user expander + server defaults) */
  .limits-btn {
    display: inline-flex; align-items: center; gap: 0.3rem;
    margin-top: 0.3rem; padding: 0.15rem 0.45rem;
    background: none; border: 1px solid var(--border); border-radius: 6px;
    color: var(--text-muted); font-size: 0.7rem; font-family: inherit; cursor: pointer;
    transition: color 0.12s, border-color 0.12s;
  }
  .limits-btn:hover { color: var(--text-secondary); border-color: var(--border-strong); }
  .limits-btn.off { color: #f87171; border-color: rgba(248,113,113,0.35); }
  .limits-btn .chev { font-size: 0.6rem; }
  .limits-panel {
    display: flex; flex-direction: column; align-items: flex-start; gap: 0.4rem;
    margin-top: 0.4rem; padding: 0.55rem 0.65rem; max-width: 240px;
    background: rgba(255,255,255,0.03); border: 1px solid var(--border); border-radius: 8px;
  }
  .quota-row { display: flex; flex-wrap: wrap; gap: 0.5rem; }
  .quota-field { display: flex; flex-direction: column; gap: 0.15rem; font-size: 0.68rem; color: var(--text-muted); }
  .quota-field input {
    width: 92px; padding: 0.3rem 0.45rem;
    background: var(--bg-elevated, rgba(255,255,255,0.05)); border: 1px solid var(--border-strong);
    border-radius: 6px; color: var(--text-primary); font-size: 0.78rem; font-family: inherit;
  }
  .quota-field input:focus { outline: none; border-color: var(--accent); }
  .limits-hint { font-size: 0.66rem; color: var(--text-muted); margin: 0; line-height: 1.4; }
  .btn-limits-save {
    padding: 0.3rem 0.65rem; background: var(--accent); border: none; border-radius: 6px;
    color: #fff; font-size: 0.72rem; font-weight: 600; cursor: pointer;
  }
  .btn-limits-save:disabled { opacity: 0.5; cursor: not-allowed; }

  /* Buttons */
  .btn-create {
    padding: 0.4rem 0.8rem; background: var(--accent);
    border: none; border-radius: 7px; color: #fff;
    font-size: 0.78rem; font-weight: 600; cursor: pointer; transition: background 0.15s;
  }
  .btn-create:hover { background: var(--accent-hover); }
  .btn-save {
    padding: 0.48rem 1rem; background: var(--accent);
    border: none; border-radius: 7px; color: #fff;
    font-size: 0.8rem; font-weight: 600; cursor: pointer; transition: background 0.15s;
    white-space: nowrap; align-self: flex-end;
  }
  .btn-save:hover { background: var(--accent-hover); }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }

  /* Watch-limit dialog */
  .usage-row {
    display: flex; justify-content: space-between; align-items: center;
    font-size: 0.8rem; color: var(--text-muted);
    background: rgba(255,255,255,0.03); border: 1px solid var(--border);
    border-radius: 8px; padding: 0.5rem 0.75rem; margin-bottom: 1rem;
  }
  .usage-row strong { color: var(--text-primary); font-weight: 600; }
  .limit-section { margin-bottom: 1rem; }
  .limit-toggle {
    display: flex; align-items: center; gap: 0.5rem;
    font-size: 0.85rem; color: var(--text-primary); cursor: pointer;
  }
  .limit-toggle input[type="checkbox"] { width: 16px; height: 16px; accent-color: var(--accent); margin: 0; }
  .limit-field {
    display: flex; align-items: center; gap: 0.5rem;
    margin: 0.6rem 0 0 1.6rem;
  }
  .limit-field input[type="number"] { width: 80px; }
  .limit-field input[type="time"] { width: auto; }
  .limit-field .unit { font-size: 0.78rem; color: var(--text-muted); }
  .limit-label { display: block; font-size: 0.8rem; color: var(--text-secondary); margin-bottom: 0.4rem; }
  .dialog .limit-hint { font-size: 0.7rem; color: var(--text-muted); margin: 0.5rem 0 0 1.6rem; }

  .library-list {
    list-style: none; margin: 0 0 1rem; padding: 0;
    max-height: 280px; overflow-y: auto;
    border: 1px solid var(--border); border-radius: 8px;
  }
  .library-list li + li { border-top: 1px solid var(--input-bg); }
  .library-row {
    display: flex; align-items: center; gap: 0.6rem;
    padding: 0.55rem 0.75rem; cursor: pointer;
    font-size: 0.85rem; color: var(--text-primary);
  }
  .library-row:hover { background: rgba(255,255,255,0.03); }
  .library-row input[type="checkbox"] { margin: 0; width: 16px; height: 16px; accent-color: var(--accent); }
  .library-name { flex: 1; }
  .library-type {
    font-size: 0.68rem; text-transform: uppercase;
    color: var(--text-muted); letter-spacing: 0.05em;
    background: rgba(255,255,255,0.04);
    border-radius: 4px; padding: 0.1rem 0.4rem;
  }
  .btn-delete {
    padding: 0.3rem 0.6rem; background: none;
    border: 1px solid rgba(248,113,113,0.25); border-radius: 6px;
    color: #f87171; font-size: 0.72rem; font-weight: 500;
    cursor: pointer; transition: all 0.15s;
  }
  .btn-delete:hover { background: rgba(248,113,113,0.1); border-color: rgba(248,113,113,0.4); }

  /* Overlay / dialog */
  .overlay {
    position: fixed; inset: 0; z-index: 100;
    background: var(--shadow); backdrop-filter: blur(4px);
    display: flex; align-items: center; justify-content: center;
  }
  .dialog {
    /* Theme surface: the text inside uses theme colours, so a fixed dark
       background hid it in light mode. */
    background: var(--bg-elevated); border: 1px solid var(--border-strong);
    border-radius: 12px; padding: 1.5rem; max-width: 400px; width: 90%;
  }
  .dialog h2 { font-size: 1rem; font-weight: 600; color: var(--text-primary); margin: 0 0 0.6rem 0; }
  .dialog p { font-size: 0.85rem; color: var(--text-muted); margin: 0 0 1.25rem 0; line-height: 1.5; }
  .dialog strong { color: var(--text-primary); }
  .dialog-actions { display: flex; justify-content: flex-end; gap: 0.5rem; }
  .btn-cancel {
    padding: 0.4rem 0.8rem; background: var(--border);
    border: 1px solid var(--border-strong); border-radius: 7px;
    color: var(--text-secondary); font-size: 0.8rem; cursor: pointer; transition: background 0.15s;
  }
  .btn-cancel:hover { background: var(--border-strong); }
  .btn-confirm-delete {
    padding: 0.4rem 0.8rem; background: #dc2626;
    border: none; border-radius: 7px; color: #fff;
    font-size: 0.8rem; font-weight: 600; cursor: pointer; transition: background 0.15s;
  }
  .btn-confirm-delete:hover { background: #ef4444; }
  .btn-confirm-delete:disabled { opacity: 0.5; cursor: not-allowed; }

  /* Header actions row */
  .header-actions { display: flex; gap: 0.5rem; }

  /* Invite button */
  .btn-invite {
    padding: 0.4rem 0.8rem; background: none;
    border: 1px solid rgba(124,106,247,0.3); border-radius: 7px;
    color: var(--accent-text); font-size: 0.78rem; font-weight: 600;
    cursor: pointer; transition: all 0.15s;
  }
  .btn-invite:hover { background: rgba(124,106,247,0.1); border-color: rgba(124,106,247,0.5); }

  /* Invite URL box */
  .invite-url-box { margin-top: 0.75rem; }
  .url-row { display: flex; gap: 0.5rem; }
  .url-input {
    flex: 1; font-size: 0.78rem; color: var(--accent-text);
    background: rgba(124,106,247,0.06); border: 1px solid rgba(124,106,247,0.2);
    border-radius: 7px; padding: 0.45rem 0.7rem; cursor: text;
  }
  .btn-copy {
    padding: 0.45rem 0.8rem; background: var(--accent);
    border: none; border-radius: 7px; color: #fff;
    font-size: 0.76rem; font-weight: 600; cursor: pointer;
    transition: background 0.15s; white-space: nowrap;
  }
  .btn-copy:hover { background: var(--accent-hover); }
  .url-hint {
    font-size: 0.7rem; color: var(--text-muted); margin: 0.4rem 0 0;
  }

  /* Invites section */
  .invites-section { margin-top: 2rem; }
  .invites-section h2, .defaults-section h2 {
    font-size: 1rem; font-weight: 600; color: #8888a0;
    margin: 0 0 0.75rem;
  }
  .status-pending {
    font-size: 0.72rem; font-weight: 600; color: #facc15;
    background: rgba(250,204,21,0.1); border-radius: 4px;
    padding: 0.1rem 0.4rem;
  }
  .status-used {
    font-size: 0.72rem; font-weight: 600; color: #34d399;
    background: rgba(52,211,153,0.1); border-radius: 4px;
    padding: 0.1rem 0.4rem;
  }
  .status-expired {
    font-size: 0.72rem; font-weight: 600; color: var(--text-muted);
    background: var(--input-bg); border-radius: 4px;
    padding: 0.1rem 0.4rem;
  }
</style>
