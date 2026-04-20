<script lang="ts">
  import { onMount } from 'svelte';
  import { api, userApi, inviteApi, type User, type UserMeta, type UserLibraryAccess } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  let loading = true;
  let error = '';
  let users: User[] = [];
  let currentUser: UserMeta | null = null;

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
    await Promise.all([loadUsers(), loadInvites()]);
  });

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
      toast.success('Invite created');
      await loadInvites();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to create invite');
    } finally {
      inviting = false;
    }
  }

  async function deleteInvite(id: string) {
    try {
      await inviteApi.del(id);
      toast.success('Invite deleted');
      await loadInvites();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to delete invite');
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
    if (d < now) return 'Expired';
    return formatDate(dateStr);
  }

  async function loadUsers() {
    loading = true;
    error = '';
    try {
      const result = await userApi.list();
      users = result.items;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load users';
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
      toast.success(`User "${name}" created`);
      await loadUsers();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to create user');
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
      toast.success(`User "${name}" deleted`);
      await loadUsers();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to delete user');
    } finally {
      deleting = false;
    }
  }

  async function toggleAdmin(user: User) {
    error = '';
    try {
      await userApi.setAdmin(user.id, !user.is_admin);
      toast.success(`${user.username} is now ${!user.is_admin ? 'an admin' : 'a regular user'}`);
      await loadUsers();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to update user');
    }
  }

  async function handleResetPassword() {
    if (!resetTarget || !resetPassword) return;
    resetting = true;
    try {
      const name = resetTarget.username;
      await userApi.resetPassword(resetTarget.id, resetPassword);
      toast.success(`Password reset for "${name}"`);
      resetTarget = null;
      resetPassword = '';
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to reset password');
    } finally {
      resetting = false;
    }
  }

  function isSelf(user: User): boolean {
    return currentUser?.user_id === user.id;
  }

  async function openLibraries(user: User) {
    librariesTarget = user;
    librariesLoading = true;
    libraryAccess = [];
    try {
      libraryAccess = await userApi.getLibraries(user.id) ?? [];
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to load library access');
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
      toast.success(`Library access updated for "${librariesTarget.username}"`);
      librariesTarget = null;
      libraryAccess = [];
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Failed to update library access');
    } finally {
      librariesSaving = false;
    }
  }

  function toggleLibrary(libraryId: string) {
    libraryAccess = libraryAccess.map(l =>
      l.library_id === libraryId ? { ...l, enabled: !l.enabled } : l
    );
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

<svelte:head><title>Users — OnScreen</title></svelte:head>

<div class="page">
  <div class="header">
    <div class="header-actions">
      <button class="btn-invite" on:click={() => { showInvite = !showInvite; inviteURL = ''; inviteEmail = ''; copied = false; }}>
        {showInvite ? 'Cancel' : 'Invite User'}
      </button>
      <button class="btn-create" on:click={() => showCreate = !showCreate}>
        {showCreate ? 'Cancel' : '+ Create User'}
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
            <label for="invite-email">Email (optional)</label>
            <input
              id="invite-email"
              type="email"
              bind:value={inviteEmail}
              placeholder="user@example.com"
              autocomplete="off"
            />
          </div>
          <button type="submit" class="btn-save" disabled={inviting}>
            {inviting ? 'Creating...' : 'Create Invite'}
          </button>
        </div>
      </form>
      {#if inviteURL}
        <div class="invite-url-box">
          <label>Invite Link</label>
          <div class="url-row">
            <input type="text" readonly value={inviteURL} class="url-input" />
            <button class="btn-copy" on:click={copyInviteURL}>
              {copied ? 'Copied!' : 'Copy'}
            </button>
          </div>
          <p class="url-hint">Share this link with the user. It expires in 7 days.</p>
        </div>
      {/if}
    </div>
  {/if}

  {#if showCreate}
    <form class="create-form" on:submit|preventDefault={createUser}>
      <div class="field-row">
        <div class="field">
          <label for="new-username">Username</label>
          <input
            id="new-username"
            type="text"
            bind:value={createUsername}
            placeholder="Username"
            autocomplete="off"
            required
          />
        </div>
        <div class="field">
          <label for="new-email">Email</label>
          <input
            id="new-email"
            type="email"
            bind:value={createEmail}
            placeholder="Optional"
            autocomplete="off"
          />
        </div>
        <div class="field">
          <label for="new-password">Password</label>
          <input
            id="new-password"
            type="password"
            bind:value={createPassword}
            placeholder="Password"
            autocomplete="new-password"
            required
          />
        </div>
        <button type="submit" class="btn-save" disabled={creating || !createUsername.trim() || !createPassword}>
          {creating ? 'Creating...' : 'Create'}
        </button>
      </div>
    </form>
  {/if}

  {#if loading}
    <div class="skeleton-block"></div>
  {:else if users.length === 0}
    <div class="empty">No users found.</div>
  {:else}
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>Username</th>
            <th>Role</th>
            <th>Created</th>
            <th class="actions-col"></th>
          </tr>
        </thead>
        <tbody>
          {#each users as user (user.id)}
            <tr>
              <td class="username-cell">
                {user.username}
                {#if isSelf(user)}<span class="you-badge">you</span>{/if}
              </td>
              <td>
                <button
                  class="admin-toggle"
                  class:active={user.is_admin}
                  disabled={isSelf(user)}
                  title={isSelf(user) ? 'Cannot change your own admin status' : (user.is_admin ? 'Remove admin' : 'Make admin')}
                  on:click={() => toggleAdmin(user)}
                >
                  <span class="toggle-track">
                    <span class="toggle-thumb"></span>
                  </span>
                  <span class="toggle-label">{user.is_admin ? 'Admin' : 'User'}</span>
                </button>
              </td>
              <td class="date-cell">{formatDate(user.created_at)}</td>
              <td class="actions-cell">
                {#if !user.is_admin}
                  <button
                    class="btn-libraries"
                    on:click={() => openLibraries(user)}
                    title="Manage library access"
                  >Libraries</button>
                {/if}
                <button
                  class="btn-reset"
                  on:click={() => { resetTarget = user; resetPassword = ''; }}
                  title="Reset password"
                >Reset PW</button>
                {#if !isSelf(user)}
                  <button
                    class="btn-delete"
                    on:click={() => deleteTarget = user}
                    title="Delete user"
                  >Delete</button>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}

  <!-- Pending invites -->
  {#if invites.length > 0}
    <div class="invites-section">
      <h2>Invites</h2>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Email</th>
              <th>Status</th>
              <th>Created</th>
              <th class="actions-col"></th>
            </tr>
          </thead>
          <tbody>
            {#each invites as invite (invite.id)}
              <tr>
                <td class="username-cell">{invite.email ?? '(no email)'}</td>
                <td>
                  {#if invite.used_at}
                    <span class="status-used">Used</span>
                  {:else if new Date(invite.expires_at) < new Date()}
                    <span class="status-expired">Expired</span>
                  {:else}
                    <span class="status-pending">Pending</span>
                  {/if}
                </td>
                <td class="date-cell">{formatDate(invite.created_at)}</td>
                <td class="actions-cell">
                  {#if !invite.used_at}
                    <button class="btn-delete" on:click={() => deleteInvite(invite.id)}>Revoke</button>
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
        <h2>Delete user</h2>
        <p>Are you sure you want to delete <strong>{deleteTarget.username}</strong>? This cannot be undone.</p>
        <div class="dialog-actions">
          <button class="btn-cancel" on:click={() => deleteTarget = null}>Cancel</button>
          <button class="btn-confirm-delete" disabled={deleting} on:click={deleteUser}>
            {deleting ? 'Deleting...' : 'Delete'}
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
        <h2>Library access</h2>
        <p>Choose which libraries <strong>{librariesTarget.username}</strong> can see.</p>
        {#if librariesLoading}
          <div class="skeleton-block" style="height:120px;margin-bottom:1rem;"></div>
        {:else if libraryAccess.length === 0}
          <div class="empty" style="padding:1rem 0;">No libraries configured.</div>
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
          <button class="btn-cancel" on:click={() => librariesTarget = null}>Cancel</button>
          <button class="btn-save" disabled={librariesLoading || librariesSaving} on:click={saveLibraries}>
            {librariesSaving ? 'Saving...' : 'Save'}
          </button>
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
        <h2>Reset password</h2>
        <p>Set a new password for <strong>{resetTarget.username}</strong>.</p>
        <form on:submit|preventDefault={handleResetPassword}>
          <input
            type="password"
            bind:value={resetPassword}
            placeholder="New password (min 8 chars)"
            autocomplete="new-password"
            required
            minlength="8"
            style="width:100%;padding:0.5rem 0.7rem;border:1px solid #333;border-radius:6px;background:#0d0d14;color:#eee;font-size:0.9rem;margin:0.75rem 0;"
          />
          <div class="dialog-actions">
            <button type="button" class="btn-cancel" on:click={() => resetTarget = null}>Cancel</button>
            <button type="submit" class="btn-save" disabled={resetting || resetPassword.length < 8}>
              {resetting ? 'Resetting...' : 'Reset Password'}
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

  h1 { font-size: 1.25rem; font-weight: 700; color: var(--text-primary); letter-spacing: -0.02em; margin: 0; }

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
  .actions-col { width: 80px; }
  .actions-cell { text-align: right; }

  /* Admin toggle */
  .admin-toggle {
    display: inline-flex; align-items: center; gap: 0.5rem;
    background: none; border: none; cursor: pointer; padding: 0.2rem 0;
    color: var(--text-muted); font-size: 0.8rem;
  }
  .admin-toggle:disabled { opacity: 0.4; cursor: not-allowed; }
  .toggle-track {
    display: inline-block; width: 32px; height: 18px;
    background: rgba(255,255,255,0.1); border-radius: 9px;
    position: relative; transition: background 0.2s;
  }
  .admin-toggle.active .toggle-track { background: var(--accent); }
  .toggle-thumb {
    position: absolute; top: 2px; left: 2px;
    width: 14px; height: 14px;
    background: #fff; border-radius: 50%;
    transition: transform 0.2s;
  }
  .admin-toggle.active .toggle-thumb { transform: translateX(14px); }
  .toggle-label { font-size: 0.78rem; }
  .admin-toggle.active .toggle-label { color: var(--accent-text); }

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
  .btn-reset {
    padding: 0.3rem 0.6rem; background: none;
    border: 1px solid rgba(167,139,250,0.25); border-radius: 6px;
    color: #a78bfa; font-size: 0.72rem; font-weight: 500;
    cursor: pointer; transition: all 0.15s;
  }
  .btn-reset:hover { background: rgba(167,139,250,0.1); border-color: rgba(167,139,250,0.4); }
  .btn-libraries {
    padding: 0.3rem 0.6rem; background: none;
    border: 1px solid rgba(94,189,255,0.25); border-radius: 6px;
    color: #60a5fa; font-size: 0.72rem; font-weight: 500;
    cursor: pointer; transition: all 0.15s;
  }
  .btn-libraries:hover { background: rgba(94,189,255,0.1); border-color: rgba(94,189,255,0.4); }

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
    background: #16161f; border: 1px solid var(--border-strong);
    border-radius: 12px; padding: 1.5rem; max-width: 400px; width: 90%;
  }
  .dialog h2 { font-size: 1rem; font-weight: 600; color: var(--text-primary); margin: 0 0 0.6rem 0; }
  .dialog p { font-size: 0.85rem; color: #88889a; margin: 0 0 1.25rem 0; line-height: 1.5; }
  .dialog strong { color: var(--text-primary); }
  .dialog-actions { display: flex; justify-content: flex-end; gap: 0.5rem; }
  .btn-cancel {
    padding: 0.4rem 0.8rem; background: var(--border);
    border: 1px solid var(--border-strong); border-radius: 7px;
    color: #ccccd8; font-size: 0.8rem; cursor: pointer; transition: background 0.15s;
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
  .invites-section h2 {
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
