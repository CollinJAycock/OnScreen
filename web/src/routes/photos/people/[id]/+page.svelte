<script lang="ts">
  // One person in a photo library: their photos (newest first, opening in
  // the viewer with Previous / Next walking them), rename and delete, and
  // suggested faces — unnamed faces taken around the same time as their
  // photos. Matching is manual; suggestions only narrow the search.
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { photoPeopleApi, type PhotoFaceThumb, type PhotoListItem, type PhotoPerson } from '$lib/api';
  import { confirmAction } from '$lib/native';
  import { toast } from '$lib/stores/toast';
  import { faceCropStyle, photoImageUrl, serverImageUrl } from '$lib/photos';
  import { photoViewerHref } from '$lib/photoViewerContext';

  const PAGE = 200;

  const personId = $derived($page.params.id ?? '');
  let person = $state<PhotoPerson | null>(null);
  let items = $state.raw<PhotoListItem[]>([]);
  let total = $state(0);
  let suggestions = $state.raw<PhotoFaceThumb[]>([]);
  let loading = $state(true);
  let error = $state('');
  let notFound = $state(false);
  let renaming = $state(false);
  let renameDraft = $state('');
  let saving = $state(false);
  let seq = 0;

  function errText(e: unknown, fallback: string): string {
    return e instanceof Error && e.message ? e.message : fallback;
  }

  function isNotFound(e: unknown): boolean {
    return !!e && typeof e === 'object' && 'status' in e && (e as { status: number }).status === 404;
  }

  async function load(id: string) {
    const mine = ++seq;
    loading = true;
    error = '';
    notFound = false;
    try {
      const [p, list] = await Promise.all([photoPeopleApi.person(id), photoPeopleApi.personItems(id, PAGE)]);
      if (mine !== seq) return;
      person = p;
      items = list.items ?? [];
      total = list.total;
      if (p.can_edit) {
        suggestions = await photoPeopleApi.suggestions(id).catch(() => []);
      }
    } catch (e) {
      if (mine !== seq) return;
      person = null;
      if (isNotFound(e)) notFound = true;
      else error = errText(e, 'Could not load this person');
    } finally {
      if (mine === seq) loading = false;
    }
  }

  $effect(() => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    load(personId);
  });

  async function loadMore() {
    try {
      const more = await photoPeopleApi.personItems(personId, PAGE, items.length);
      items = [...items, ...(more.items ?? [])];
    } catch (e) {
      toast.error(errText(e, 'Could not load more photos'));
    }
  }

  async function saveRename(e: SubmitEvent) {
    e.preventDefault();
    const name = renameDraft.trim();
    if (!person || !name || saving) return;
    saving = true;
    try {
      const updated = await photoPeopleApi.renamePerson(person.id, name);
      person = { ...person, name: updated.name };
      renaming = false;
    } catch (err) {
      toast.error(errText(err, 'Could not rename'));
    } finally {
      saving = false;
    }
  }

  async function deletePerson() {
    if (!person) return;
    if (!(await confirmAction(`Remove "${person.name}"? Their faces stay on the photos, unnamed.`))) return;
    try {
      await photoPeopleApi.deletePerson(person.id);
      toast.success(`Removed "${person.name}"`);
      goto(`/photos/people?library=${encodeURIComponent(person.library_id)}`);
    } catch (err) {
      toast.error(errText(err, 'Could not remove this person'));
    }
  }

  async function confirmSuggestion(f: PhotoFaceThumb) {
    if (!person) return;
    try {
      await photoPeopleApi.assignFace(f.id, person.id);
      suggestions = suggestions.filter((s) => s.id !== f.id);
      // Show the photo straight away unless it's already listed.
      if (!items.some((it) => it.id === f.item_id)) {
        const fresh = await photoPeopleApi.personItems(person.id, Math.max(PAGE, items.length + 1));
        items = fresh.items ?? items;
        total = fresh.total;
      }
    } catch (err) {
      toast.error(errText(err, 'Could not name the face'));
    }
  }

  function dismissSuggestion(f: PhotoFaceThumb) {
    suggestions = suggestions.filter((s) => s.id !== f.id);
  }

  function onRenameKey(e: KeyboardEvent) {
    if (e.key === 'Escape') { e.preventDefault(); renaming = false; }
  }

  function autofocus(node: HTMLInputElement) {
    node.focus();
    node.select();
  }
</script>

<svelte:head><title>{person?.name ?? 'Person'} — OnScreen</title></svelte:head>

<div class="page">
  <nav class="crumb">
    <a href="/photos/people{person ? `?library=${encodeURIComponent(person.library_id)}` : ''}">People</a>
    <span>/</span>
    <span>{person?.name ?? '…'}</span>
  </nav>

  {#if loading}
    <p class="muted" role="status">Loading…</p>
  {:else if notFound}
    <div class="empty">
      <p class="empty-t">Person not found</p>
      <p class="empty-s"><a href="/photos/people">Back to people</a></p>
    </div>
  {:else if error}
    <div class="error-bar" role="alert">
      {error}
      <button type="button" class="retry" onclick={() => load(personId)}>Retry</button>
    </div>
  {:else if person}
    <div class="head">
      {#if renaming}
        <form class="rename-form" onsubmit={saveRename}>
          <input bind:value={renameDraft} maxlength="100" aria-label="Name" onkeydown={onRenameKey} use:autofocus />
          <button type="submit" class="btn-save" disabled={saving || !renameDraft.trim()}>Save</button>
          <button type="button" class="btn-cancel" onclick={() => (renaming = false)}>Cancel</button>
        </form>
      {:else}
        <div class="title-block">
          <h1>{person.name}</h1>
          <div class="count">{total.toLocaleString()} {total === 1 ? 'photo' : 'photos'}</div>
        </div>
        {#if person.can_edit}
          <div class="actions">
            <button type="button" class="btn" onclick={() => { renameDraft = person?.name ?? ''; renaming = true; }}>Rename</button>
            <button type="button" class="btn danger" onclick={deletePerson}>Remove</button>
          </div>
        {/if}
      {/if}
    </div>

    {#if suggestions.length > 0}
      <section class="suggest">
        <h2>Is this {person.name}?</h2>
        <p class="hint">Unnamed faces from around the same time as {person.name}'s photos.</p>
        <div class="faces">
          {#each suggestions as f (f.id)}
            <div class="face-card">
              <a
                class="face-crop"
                style={faceCropStyle(serverImageUrl(f.item_id, { w: 800 }), f)}
                href={photoViewerHref(f.item_id, { kind: 'library' })}
                title="Open the photo"
                aria-label="Open the photo"
              ></a>
              <div class="face-actions">
                <button type="button" class="yes" onclick={() => confirmSuggestion(f)} aria-label="Yes, this is {person.name}">Yes</button>
                <button type="button" class="no" onclick={() => dismissSuggestion(f)} aria-label="No">No</button>
              </div>
            </div>
          {/each}
        </div>
      </section>
    {/if}

    {#if items.length === 0}
      <div class="empty"><p class="empty-t">No photos</p></div>
    {:else}
      <div class="grid">
        {#each items as it (it.id)}
          <a class="tile" href={photoViewerHref(it.id, { kind: 'person', personId: person.id })} title={it.title}>
            <img src={photoImageUrl(it, { w: 300 })} alt="" loading="lazy" />
          </a>
        {/each}
      </div>
      {#if items.length < total}
        <button type="button" class="btn more" onclick={loadMore}>Show more</button>
      {/if}
    {/if}
  {/if}
</div>

<style>
  .page { padding: 2.5rem 2.5rem 6rem; }
  .crumb {
    display: flex; align-items: center; gap: 0.4rem;
    font-size: 0.75rem; color: var(--text-muted); margin-bottom: 1.5rem;
  }
  .crumb a { color: var(--text-muted); text-decoration: none; }
  .crumb a:hover { color: var(--text-secondary); }
  .head {
    display: flex; align-items: flex-end; justify-content: space-between; gap: 1rem; flex-wrap: wrap;
    margin-bottom: 1.5rem; padding-bottom: 1.25rem; border-bottom: 1px solid var(--border);
  }
  .title-block { min-width: 0; }
  h1 { font-size: 1.4rem; font-weight: 800; color: var(--text-primary); letter-spacing: -0.025em; word-break: break-word; }
  h2 { font-size: 0.95rem; font-weight: 700; color: var(--text-primary); margin-bottom: 0.4rem; }
  .count { font-size: 0.75rem; color: var(--text-muted); margin-top: 0.25rem; }
  .hint { font-size: 0.75rem; color: var(--text-muted); margin-bottom: 0.8rem; }
  .actions { display: flex; gap: 0.5rem; }
  .btn {
    padding: 0.42rem 0.85rem; background: var(--bg-hover);
    border: 1px solid var(--border-strong); border-radius: 7px;
    color: var(--text-secondary); font-size: 0.78rem; cursor: pointer;
  }
  .btn:hover { border-color: var(--accent); color: var(--text-primary); }
  .btn.danger:hover { border-color: var(--error); color: var(--error); }
  .more { margin-top: 1rem; }
  .rename-form { display: flex; gap: 0.4rem; flex-wrap: wrap; flex: 1; max-width: 520px; }
  .rename-form input {
    flex: 1; min-width: 140px; background: var(--input-bg);
    border: 1px solid var(--border-strong); border-radius: 6px;
    padding: 0.45rem 0.65rem; color: var(--text-primary); font-size: 0.9rem;
  }
  .rename-form input:focus { outline: none; border-color: var(--accent); }
  .btn-save {
    padding: 0.35rem 0.7rem; background: var(--accent); border: none; border-radius: 6px;
    color: #fff; font-size: 0.75rem; font-weight: 600; cursor: pointer;
  }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-cancel {
    padding: 0.35rem 0.6rem; background: var(--input-bg); border: 1px solid var(--border);
    border-radius: 6px; color: var(--text-muted); font-size: 0.75rem; cursor: pointer;
  }

  .suggest {
    margin-bottom: 1.75rem; padding: 1rem; border: 1px solid var(--border);
    border-radius: 10px; background: var(--bg-elevated);
  }
  .faces { display: grid; grid-template-columns: repeat(auto-fill, minmax(100px, 1fr)); gap: 0.8rem; }
  .face-card { display: flex; flex-direction: column; gap: 0.35rem; }
  .face-crop {
    display: block; aspect-ratio: 1 / 1; width: 100%;
    background-color: var(--bg-hover); background-repeat: no-repeat;
    border-radius: 10px; border: 1px solid var(--border);
  }
  .face-crop:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .face-actions { display: flex; gap: 0.35rem; }
  .face-actions button {
    flex: 1; padding: 0.25rem 0; border-radius: 6px; font-size: 0.72rem; cursor: pointer;
    border: 1px solid var(--border-strong); background: var(--bg-hover); color: var(--text-secondary);
  }
  .face-actions .yes:hover { border-color: var(--accent); color: var(--accent-text); }
  .face-actions .no:hover { color: var(--text-primary); }

  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr)); gap: 0.5rem; }
  .tile {
    position: relative; display: block; aspect-ratio: 4 / 3;
    border-radius: 8px; overflow: hidden; background: var(--bg-elevated);
  }
  .tile img { width: 100%; height: 100%; object-fit: cover; display: block; transition: transform 0.3s; }
  .tile:hover img { transform: scale(1.04); }
  .tile:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }

  .muted { color: var(--text-muted); font-size: 0.85rem; }
  .error-bar {
    background: var(--error-bg); color: var(--error); padding: 0.6rem 0.9rem;
    border-radius: 8px; font-size: 0.8rem; display: flex; gap: 0.75rem; align-items: center;
  }
  .retry {
    background: none; border: 1px solid currentColor; border-radius: 6px; color: inherit;
    font-size: 0.72rem; padding: 0.15rem 0.5rem; cursor: pointer;
  }
  .empty { text-align: center; padding: 3.5rem 1rem; color: var(--text-muted); }
  .empty-t { font-weight: 700; color: var(--text-secondary); }
  .empty-s { font-size: 0.8rem; margin-top: 0.4rem; }
  .empty-s a { color: var(--accent-text); }

  @media (max-width: 768px) {
    .page { padding: 1.25rem 1rem 8rem; }
    .grid { grid-template-columns: repeat(auto-fill, minmax(110px, 1fr)); gap: 0.35rem; }
  }
</style>
