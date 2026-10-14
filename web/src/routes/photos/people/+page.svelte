<script lang="ts">
  // People in a photo library: everyone named so far (most photos first),
  // and the faces nobody has named yet. Faces are found by the opt-in
  // "Photo face detection" task; telling who is who is manual — name a face
  // here or in the photo viewer, and a name the library already has joins
  // that person. The server shows only people and faces on photos this
  // account may see.
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import {
    libraryApi,
    photoPeopleApi,
    type Library,
    type PhotoFaceThumb,
    type PhotoPerson,
  } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { faceCropStyle, serverImageUrl } from '$lib/photos';
  import { photoViewerHref } from '$lib/photoViewerContext';

  const UNNAMED_PAGE = 60;

  let libraries = $state.raw<Library[]>([]);
  let libraryId = $state('');
  let people = $state.raw<PhotoPerson[]>([]);
  let unnamed = $state.raw<PhotoFaceThumb[]>([]);
  let moreUnnamed = $state(false);
  let canEdit = $state(false);
  let loading = $state(true);
  let error = $state('');
  let naming = $state<string | null>(null);
  let nameDraft = $state('');
  let saving = $state(false);
  let seq = 0;

  const library = $derived(libraries.find((l) => l.id === libraryId));
  const names = $derived(people.map((p) => p.name));

  function errText(e: unknown, fallback: string): string {
    return e instanceof Error && e.message ? e.message : fallback;
  }

  function faceStyle(f: PhotoFaceThumb): string {
    return faceCropStyle(serverImageUrl(f.item_id, { w: 800 }), f);
  }

  async function load(id: string) {
    const mine = ++seq;
    loading = true;
    error = '';
    try {
      const [ppl, faces] = await Promise.all([
        photoPeopleApi.people(id),
        photoPeopleApi.unnamedFaces(id, UNNAMED_PAGE, 0),
      ]);
      if (mine !== seq) return;
      people = ppl;
      unnamed = faces;
      moreUnnamed = faces.length === UNNAMED_PAGE;
      // The people list carries the caller's naming right; with nobody
      // named yet, ask a photo that has a face.
      if (ppl.length > 0) {
        canEdit = ppl[0].can_edit;
      } else if (faces.length > 0) {
        canEdit = !!(await photoPeopleApi.itemFaces(faces[0].item_id).catch(() => null))?.can_edit;
      }
    } catch (e) {
      if (mine !== seq) return;
      error = errText(e, 'Could not load people');
    } finally {
      if (mine === seq) loading = false;
    }
  }

  async function loadMoreUnnamed() {
    try {
      const more = await photoPeopleApi.unnamedFaces(libraryId, UNNAMED_PAGE, unnamed.length);
      unnamed = [...unnamed, ...more];
      moreUnnamed = more.length === UNNAMED_PAGE;
    } catch (e) {
      toast.error(errText(e, 'Could not load more faces'));
    }
  }

  $effect(() => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    let cancelled = false;
    (async () => {
      try {
        const all = await libraryApi.list();
        if (cancelled) return;
        libraries = all.filter((l) => l.type === 'photo');
        const wanted = $page.url.searchParams.get('library');
        libraryId = wanted && libraries.some((l) => l.id === wanted) ? wanted : (libraries[0]?.id ?? '');
        if (libraryId) await load(libraryId);
        else loading = false;
      } catch (e) {
        error = errText(e, 'Could not load libraries');
        loading = false;
      }
    })();
    return () => { cancelled = true; };
  });

  function pickLibrary(id: string) {
    libraryId = id;
    goto(`/photos/people?library=${encodeURIComponent(id)}`, { replaceState: true, noScroll: true });
    load(id);
  }

  function startNaming(f: PhotoFaceThumb) {
    naming = f.id;
    nameDraft = '';
  }

  async function saveName(e: SubmitEvent, f: PhotoFaceThumb) {
    e.preventDefault();
    const name = nameDraft.trim();
    if (!name || saving) return;
    saving = true;
    try {
      const updated = await photoPeopleApi.nameFace(f.id, name);
      unnamed = unnamed.filter((u) => u.id !== f.id);
      naming = null;
      const existing = people.find((p) => p.id === updated.person_id);
      if (existing) {
        people = people.map((p) => (p.id === existing.id ? { ...p, photo_count: p.photo_count + 1 } : p));
      } else if (updated.person_id && updated.person_name) {
        people = [...people, {
          id: updated.person_id, library_id: libraryId, name: updated.person_name,
          photo_count: 1, cover: f, can_edit: true,
        }];
      }
    } catch (err) {
      toast.error(errText(err, 'Could not name the face'));
    } finally {
      saving = false;
    }
  }

  async function notAFace(f: PhotoFaceThumb) {
    try {
      await photoPeopleApi.deleteFace(f.id);
      unnamed = unnamed.filter((u) => u.id !== f.id);
      if (naming === f.id) naming = null;
    } catch (err) {
      toast.error(errText(err, 'Could not remove the face'));
    }
  }

  function onNameKey(e: KeyboardEvent) {
    if (e.key === 'Escape') { e.preventDefault(); naming = null; }
  }

  function autofocus(node: HTMLInputElement) {
    node.focus();
  }
</script>

<svelte:head><title>People — OnScreen</title></svelte:head>

<div class="page">
  <nav class="crumb">
    {#if library}
      <a href="/libraries/{library.id}">{library.name}</a>
      <span>/</span>
    {/if}
    <span>People</span>
  </nav>

  <div class="head">
    <h1>People</h1>
    {#if libraries.length > 1}
      <select class="lib-select" value={libraryId} onchange={(e) => pickLibrary((e.currentTarget as HTMLSelectElement).value)} aria-label="Photo library">
        {#each libraries as l (l.id)}<option value={l.id}>{l.name}</option>{/each}
      </select>
    {/if}
  </div>

  {#if loading}
    <p class="muted" role="status">Loading people…</p>
  {:else if error}
    <div class="error-bar" role="alert">
      {error}
      <button type="button" class="retry" onclick={() => load(libraryId)}>Retry</button>
    </div>
  {:else if !libraryId}
    <div class="empty"><p class="empty-t">No photo libraries</p></div>
  {:else}
    {#if people.length === 0 && unnamed.length === 0}
      <div class="empty">
        <p class="empty-t">No faces yet</p>
        <p class="empty-s">
          Faces are found by the "Photo face detection" task. An admin can turn it on under
          Settings ▸ Tasks; it works through the library a batch at a time.
        </p>
      </div>
    {/if}

    {#if people.length > 0}
      <section>
        <h2>Named <span class="n">{people.length}</span></h2>
        <div class="people">
          {#each people as p (p.id)}
            <a class="person" href="/photos/people/{encodeURIComponent(p.id)}">
              {#if p.cover}
                <span class="face-crop" style={faceStyle(p.cover)} aria-hidden="true"></span>
              {:else}
                <span class="face-crop blank" aria-hidden="true">{p.name[0]?.toUpperCase()}</span>
              {/if}
              <span class="pname">{p.name}</span>
              <span class="pcount">{p.photo_count.toLocaleString()} {p.photo_count === 1 ? 'photo' : 'photos'}</span>
            </a>
          {/each}
        </div>
      </section>
    {/if}

    {#if unnamed.length > 0}
      <section>
        <h2>Who is this?</h2>
        <p class="hint">Matching is manual: name a face and it joins that person's photos.</p>
        <datalist id="people-names">
          {#each names as n}<option value={n}></option>{/each}
        </datalist>
        <div class="faces">
          {#each unnamed as f (f.id)}
            <div class="face-card">
              <a class="face-crop" style={faceStyle(f)} href={photoViewerHref(f.item_id, { kind: 'library' })} title="Open the photo" aria-label="Open the photo"></a>
              {#if canEdit}
                {#if naming === f.id}
                  <form class="name-form" onsubmit={(e) => saveName(e, f)}>
                    <input
                      type="text"
                      list="people-names"
                      maxlength="100"
                      placeholder="Name"
                      autocomplete="off"
                      bind:value={nameDraft}
                      onkeydown={onNameKey}
                      use:autofocus
                      aria-label="Name"
                    />
                    <button type="submit" class="btn-save" disabled={saving || !nameDraft.trim()}>Save</button>
                  </form>
                {:else}
                  <div class="face-actions">
                    <button type="button" class="link" onclick={() => startNaming(f)}>Name</button>
                    <button type="button" class="link muted-link" onclick={() => notAFace(f)} title="Remove this box: it isn't a face">Not a face</button>
                  </div>
                {/if}
              {/if}
            </div>
          {/each}
        </div>
        {#if moreUnnamed}
          <button type="button" class="btn more" onclick={loadMoreUnnamed}>Show more</button>
        {/if}
      </section>
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
    display: flex; align-items: center; justify-content: space-between; gap: 1rem; flex-wrap: wrap;
    margin-bottom: 1.5rem; padding-bottom: 1.25rem; border-bottom: 1px solid var(--border);
  }
  h1 { font-size: 1.4rem; font-weight: 800; color: var(--text-primary); letter-spacing: -0.025em; }
  h2 { font-size: 0.95rem; font-weight: 700; color: var(--text-primary); margin: 1.5rem 0 0.75rem; }
  h2 .n { color: var(--text-muted); font-weight: 600; margin-left: 0.3rem; }
  .hint { font-size: 0.75rem; color: var(--text-muted); margin: -0.4rem 0 0.9rem; }
  .lib-select {
    background: var(--input-bg); color: var(--text-primary);
    border: 1px solid var(--border-strong); border-radius: 6px; padding: 0.35rem 0.6rem; font-size: 0.8rem;
  }

  .people {
    display: grid; grid-template-columns: repeat(auto-fill, minmax(120px, 1fr)); gap: 1rem;
  }
  .person {
    display: flex; flex-direction: column; align-items: center; gap: 0.35rem;
    text-decoration: none; color: var(--text-primary); text-align: center;
  }
  .person:hover .pname { color: var(--accent-text); }
  .person .face-crop { width: 100%; max-width: 120px; border-radius: 50%; }
  .pname { font-size: 0.82rem; font-weight: 600; word-break: break-word; }
  .pcount { font-size: 0.7rem; color: var(--text-muted); }

  .face-crop {
    display: block; aspect-ratio: 1 / 1; width: 100%;
    background-color: var(--bg-elevated); background-repeat: no-repeat;
    border-radius: 10px; border: 1px solid var(--border);
  }
  .face-crop.blank {
    display: flex; align-items: center; justify-content: center;
    font-size: 2rem; font-weight: 800; color: var(--text-muted);
  }
  .faces { display: grid; grid-template-columns: repeat(auto-fill, minmax(110px, 1fr)); gap: 0.8rem; }
  .face-card { display: flex; flex-direction: column; gap: 0.35rem; }
  .face-card .face-crop:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .face-actions { display: flex; gap: 0.6rem; justify-content: center; }
  .link {
    background: none; border: none; padding: 0; cursor: pointer;
    color: var(--accent-text); font-size: 0.75rem;
  }
  .muted-link { color: var(--text-muted); }
  .link:hover { text-decoration: underline; }
  .name-form { display: flex; gap: 0.3rem; }
  .name-form input {
    flex: 1; min-width: 0; width: auto; background: var(--input-bg);
    border: 1px solid var(--border-strong); border-radius: 6px;
    padding: 0.3rem 0.45rem; color: var(--text-primary); font-size: 0.75rem;
  }
  .name-form input:focus { outline: none; border-color: var(--accent); }
  .btn-save {
    padding: 0.3rem 0.5rem; background: var(--accent); border: none; border-radius: 6px;
    color: #fff; font-size: 0.72rem; font-weight: 600; cursor: pointer;
  }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn {
    padding: 0.42rem 0.85rem; background: var(--bg-hover);
    border: 1px solid var(--border-strong); border-radius: 7px;
    color: var(--text-secondary); font-size: 0.78rem; cursor: pointer;
  }
  .btn:hover { border-color: var(--accent); color: var(--text-primary); }
  .more { margin-top: 1rem; }

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
  .empty-s { font-size: 0.8rem; margin-top: 0.4rem; max-width: 460px; margin-inline: auto; }

  @media (max-width: 768px) {
    .page { padding: 1.25rem 1rem 8rem; }
    .people { grid-template-columns: repeat(auto-fill, minmax(90px, 1fr)); }
    .faces { grid-template-columns: repeat(auto-fill, minmax(90px, 1fr)); }
  }
</style>
