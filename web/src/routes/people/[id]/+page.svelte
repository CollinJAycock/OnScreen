<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { peopleApi, assetUrl, type Person, type FilmographyEntry } from '$lib/api';

  let person: Person | null = null;
  let filmography: FilmographyEntry[] = [];
  let loading = true;
  let error = '';

  $: id = $page.params.id!;

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    try {
      [person, filmography] = await Promise.all([
        peopleApi.get(id),
        peopleApi.filmography(id).catch(() => [] as FilmographyEntry[])
      ]);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load person';
    } finally {
      loading = false;
    }
  });

  // Group filmography by role for display.
  $: grouped = groupByRole(filmography);

  function groupByRole(entries: FilmographyEntry[]): { role: string; label: string; items: FilmographyEntry[] }[] {
    const order: { role: string; label: string }[] = [
      { role: 'cast', label: 'Acting' },
      { role: 'director', label: 'Directing' },
      { role: 'writer', label: 'Writing' },
      { role: 'creator', label: 'Created' },
      { role: 'producer', label: 'Produced' }
    ];
    const buckets = new Map<string, FilmographyEntry[]>();
    for (const e of entries) {
      const list = buckets.get(e.role) ?? [];
      list.push(e);
      buckets.set(e.role, list);
    }
    const out: { role: string; label: string; items: FilmographyEntry[] }[] = [];
    for (const o of order) {
      const items = buckets.get(o.role);
      if (items?.length) out.push({ ...o, items: sortByYearDesc(items) });
    }
    // Catch-all for any unknown roles.
    for (const [role, items] of buckets) {
      if (!order.some(o => o.role === role)) {
        out.push({ role, label: role.charAt(0).toUpperCase() + role.slice(1), items: sortByYearDesc(items) });
      }
    }
    return out;
  }

  function sortByYearDesc(items: FilmographyEntry[]): FilmographyEntry[] {
    return [...items].sort((a, b) => (b.year ?? 0) - (a.year ?? 0));
  }

  function formatLifespan(p: Person): string {
    if (!p.birthday) return '';
    const born = p.birthday.slice(0, 10);
    if (p.deathday) return `${born} – ${p.deathday.slice(0, 10)}`;
    return `Born ${born}`;
  }
</script>

<div class="page">
  {#if loading}
    <p class="loading">Loading…</p>
  {:else if error}
    <p class="err">{error}</p>
  {:else if person}
    <header class="profile">
      {#if person.profile_path}
        <img class="profile-photo" src="https://image.tmdb.org/t/p/w300{person.profile_path}" alt={person.name} referrerpolicy="no-referrer" />
      {:else}
        <div class="profile-photo profile-placeholder">{person.name.charAt(0)}</div>
      {/if}
      <div class="profile-meta">
        <h1>{person.name}</h1>
        {#if formatLifespan(person)}
          <div class="lifespan">{formatLifespan(person)}</div>
        {/if}
        {#if person.place_of_birth}
          <div class="birthplace">{person.place_of_birth}</div>
        {/if}
        {#if person.bio}
          <p class="bio">{person.bio}</p>
        {/if}
      </div>
    </header>

    {#if grouped.length === 0}
      <p class="empty">No filmography in your library.</p>
    {:else}
      {#each grouped as group}
        <section class="role-section">
          <h2>{group.label} <span class="count">({group.items.length})</span></h2>
          <div class="grid">
            {#each group.items as f (f.item_id + group.role + (f.character ?? '') + (f.job ?? ''))}
              <a class="tile" href="/watch/{f.item_id}" title={f.title}>
                {#if f.poster_path}
                  <img class="tile-poster" src={assetUrl(`/artwork/${encodeURI(f.poster_path)}?w=200`)} alt={f.title} loading="lazy" />
                {:else}
                  <div class="tile-poster tile-placeholder">{f.title.charAt(0)}</div>
                {/if}
                <div class="tile-title">{f.title}</div>
                {#if f.year}<div class="tile-year">{f.year}</div>{/if}
                {#if f.character}
                  <div class="tile-role">as {f.character}</div>
                {:else if f.job}
                  <div class="tile-role">{f.job}</div>
                {/if}
              </a>
            {/each}
          </div>
        </section>
      {/each}
    {/if}
  {/if}
</div>

<style>
  .page { padding: 1.5rem 2rem; max-width: 1200px; margin: 0 auto; }
  .loading, .err, .empty { color: var(--text-secondary); padding: 2rem 0; text-align: center; }
  .err { color: var(--error); }

  .profile {
    display: flex; gap: 1.5rem;
    margin-bottom: 2.5rem;
  }
  .profile-photo {
    width: 200px; height: 300px;
    object-fit: cover; border-radius: 10px;
    background: var(--surface);
    flex-shrink: 0;
  }
  .profile-placeholder {
    display: flex; align-items: center; justify-content: center;
    font-size: 4rem; font-weight: 600;
    color: var(--text-muted);
  }
  .profile-meta { min-width: 0; }
  .profile-meta h1 { margin: 0 0 0.4rem; font-size: 1.8rem; }
  .lifespan, .birthplace {
    color: var(--text-secondary); font-size: 0.85rem;
    margin-bottom: 0.25rem;
  }
  .bio {
    margin: 1rem 0 0;
    font-size: 0.88rem;
    line-height: 1.6;
    color: var(--text-secondary);
    white-space: pre-line;
  }

  .role-section { margin-bottom: 2.5rem; }
  .role-section h2 {
    font-size: 1.05rem; margin: 0 0 0.85rem;
    font-weight: 600;
  }
  .count { color: var(--text-muted); font-weight: 400; font-size: 0.85em; }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
    gap: 1rem;
  }
  .tile {
    text-decoration: none; color: inherit;
    display: flex; flex-direction: column;
    transition: transform 0.15s ease;
  }
  .tile:hover { transform: translateY(-2px); }
  .tile-poster {
    width: 100%; aspect-ratio: 2 / 3;
    object-fit: cover; border-radius: 6px;
    background: var(--surface);
    margin-bottom: 0.4rem;
  }
  .tile-placeholder {
    display: flex; align-items: center; justify-content: center;
    font-size: 2.5rem; font-weight: 600; color: var(--text-muted);
  }
  .tile-title {
    font-size: 0.82rem; font-weight: 500;
    color: var(--text-primary);
    line-height: 1.3;
    overflow: hidden; text-overflow: ellipsis;
    display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical;
  }
  .tile-year { font-size: 0.75rem; color: var(--text-muted); margin-top: 0.15rem; }
  .tile-role {
    font-size: 0.72rem; color: var(--text-muted);
    margin-top: 0.1rem;
    overflow: hidden; text-overflow: ellipsis;
    display: -webkit-box; -webkit-line-clamp: 1; -webkit-box-orient: vertical;
  }

  @media (max-width: 768px) {
    .profile { flex-direction: column; gap: 1rem; }
    .profile-photo { width: 140px; height: 210px; }
    .profile-meta h1 { font-size: 1.4rem; }
  }
</style>
