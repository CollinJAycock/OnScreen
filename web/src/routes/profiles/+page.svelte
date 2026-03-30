<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, profileApi, type ManagedProfile } from '$lib/api';

  let profiles: ManagedProfile[] = [];
  let loading = true;
  let error = '';

  // Create form
  let showCreate = false;
  let newName = '';
  let newPin = '';
  let creating = false;

  // Edit state
  let editingId: string | null = null;
  let editName = '';

  const avatars = ['#7c6af7', '#f7836a', '#6af7a7', '#f7d76a', '#6ac5f7', '#f76adb'];

  onMount(async () => {
    const user = api.getUser();
    if (!user) { goto('/login'); return; }
    if (!user.is_admin) { goto('/'); return; }
    await load();
  });

  async function load() {
    loading = true;
    try { profiles = await profileApi.list(); }
    catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
    finally { loading = false; }
  }

  async function createProfile() {
    if (!newName.trim()) return;
    creating = true;
    try {
      await profileApi.create(newName.trim(), undefined, newPin || undefined);
      newName = '';
      newPin = '';
      showCreate = false;
      await load();
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
    finally { creating = false; }
  }

  async function saveEdit(id: string) {
    if (!editName.trim()) return;
    try {
      await profileApi.update(id, editName.trim());
      editingId = null;
      await load();
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
  }

  async function deleteProfile(id: string) {
    if (!confirm('Delete this profile? Watch history and progress will be lost.')) return;
    try {
      await profileApi.delete(id);
      profiles = profiles.filter(p => p.id !== id);
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
  }

  function avatarColor(id: string): string {
    let hash = 0;
    for (let i = 0; i < id.length; i++) hash = ((hash << 5) - hash + id.charCodeAt(i)) | 0;
    return avatars[Math.abs(hash) % avatars.length];
  }
</script>

<svelte:head><title>Profiles — OnScreen</title></svelte:head>

<div class="page">
  <div class="header">
    <h1>Managed Profiles</h1>
    <button class="btn-create" on:click={() => showCreate = !showCreate}>+ New Profile</button>
  </div>

  {#if error}
    <div class="error-bar">{error}</div>
  {/if}

  {#if showCreate}
    <form class="create-form" on:submit|preventDefault={createProfile}>
      <input bind:value={newName} placeholder="Profile name" autofocus />
      <input bind:value={newPin} placeholder="PIN (optional, 4 digits)" maxlength="4" pattern="[0-9]*" />
      <button type="submit" class="btn-save" disabled={creating || !newName.trim()}>Create</button>
      <button type="button" class="btn-cancel" on:click={() => showCreate = false}>Cancel</button>
    </form>
  {/if}

  {#if loading}
    <div class="loading">Loading...</div>
  {:else if profiles.length === 0}
    <div class="empty">
      <p>No managed profiles yet.</p>
      <p class="empty-sub">Create profiles for family members. Each profile has its own watch history and progress.</p>
    </div>
  {:else}
    <div class="grid">
      {#each profiles as profile (profile.id)}
        <div class="card">
          <div class="avatar" style="background:{avatarColor(profile.id)}">
            {profile.username.charAt(0).toUpperCase()}
          </div>
          {#if editingId === profile.id}
            <form class="edit-inline" on:submit|preventDefault={() => saveEdit(profile.id)}>
              <input bind:value={editName} autofocus />
              <div class="edit-actions">
                <button type="submit" class="btn-sm save">Save</button>
                <button type="button" class="btn-sm" on:click={() => editingId = null}>Cancel</button>
              </div>
            </form>
          {:else}
            <div class="name">{profile.username}</div>
            {#if profile.has_pin}<div class="badge">PIN</div>{/if}
            <div class="actions">
              <button class="btn-sm" on:click={() => { editingId = profile.id; editName = profile.username; }}>Edit</button>
              <button class="btn-sm danger" on:click={() => deleteProfile(profile.id)}>Delete</button>
            </div>
          {/if}
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .page { padding: 2.5rem; }
  .header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 2rem; }
  h1 { font-size: 1.4rem; font-weight: 800; color: #eeeef8; }

  .btn-create {
    padding: 0.42rem 0.85rem; background: rgba(124,106,247,0.12);
    border: 1px solid rgba(124,106,247,0.25); border-radius: 7px;
    color: #a89ffa; font-size: 0.78rem; font-weight: 600; cursor: pointer;
  }
  .btn-create:hover { background: rgba(124,106,247,0.2); }

  .create-form { display: flex; gap: 0.5rem; align-items: center; margin-bottom: 1.5rem; flex-wrap: wrap; }
  .create-form input {
    background: rgba(255,255,255,0.04); border: 1px solid rgba(255,255,255,0.1);
    border-radius: 7px; padding: 0.42rem 0.75rem; color: #eeeef8; font-size: 0.85rem;
  }
  .create-form input:first-child { flex: 1; max-width: 250px; }
  .create-form input:nth-child(2) { width: 170px; }
  .create-form input:focus { outline: none; border-color: #7c6af7; }
  .btn-save {
    padding: 0.42rem 0.85rem; background: #7c6af7; border: none; border-radius: 7px;
    color: #fff; font-size: 0.78rem; font-weight: 600; cursor: pointer;
  }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-cancel {
    padding: 0.42rem 0.85rem; background: rgba(255,255,255,0.04);
    border: 1px solid rgba(255,255,255,0.08); border-radius: 7px;
    color: #66667a; font-size: 0.78rem; cursor: pointer;
  }

  .grid {
    display: grid; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr)); gap: 1rem;
  }

  .card {
    display: flex; flex-direction: column; align-items: center; padding: 1.5rem 1rem;
    background: rgba(255,255,255,0.03); border: 1px solid rgba(255,255,255,0.07);
    border-radius: 10px; text-align: center;
  }

  .avatar {
    width: 3.5rem; height: 3.5rem; border-radius: 50%; display: flex;
    align-items: center; justify-content: center; font-size: 1.4rem;
    font-weight: 700; color: #fff; margin-bottom: 0.75rem;
  }

  .name { font-size: 0.88rem; font-weight: 600; color: #cccce0; margin-bottom: 0.3rem; }
  .badge {
    font-size: 0.6rem; background: rgba(124,106,247,0.15); color: #a89ffa;
    padding: 0.15rem 0.4rem; border-radius: 4px; font-weight: 600; margin-bottom: 0.5rem;
  }

  .actions { display: flex; gap: 0.4rem; margin-top: 0.5rem; }
  .btn-sm {
    padding: 0.25rem 0.55rem; background: rgba(255,255,255,0.04);
    border: 1px solid rgba(255,255,255,0.08); border-radius: 5px;
    color: #66667a; font-size: 0.68rem; cursor: pointer;
  }
  .btn-sm:hover { color: #aaaacc; border-color: rgba(255,255,255,0.15); }
  .btn-sm.save { background: #7c6af7; border-color: #7c6af7; color: #fff; }
  .btn-sm.danger:hover { color: #f87171; border-color: rgba(248,113,113,0.3); }

  .edit-inline { width: 100%; }
  .edit-inline input {
    width: 100%; background: rgba(255,255,255,0.04); border: 1px solid rgba(255,255,255,0.1);
    border-radius: 6px; padding: 0.35rem 0.6rem; color: #eeeef8; font-size: 0.82rem;
    text-align: center; margin-bottom: 0.4rem;
  }
  .edit-inline input:focus { outline: none; border-color: #7c6af7; }
  .edit-actions { display: flex; gap: 0.4rem; justify-content: center; }

  .error-bar {
    background: rgba(248,113,113,0.1); border: 1px solid rgba(248,113,113,0.2);
    color: #fca5a5; padding: 0.6rem 0.9rem; border-radius: 8px; font-size: 0.8rem; margin-bottom: 1.5rem;
  }
  .loading { color: #44445a; font-size: 0.85rem; }
  .empty { text-align: center; padding: 4rem 2rem; color: #44445a; }
  .empty-sub { font-size: 0.8rem; color: #33333d; margin-top: 0.5rem; }
</style>
