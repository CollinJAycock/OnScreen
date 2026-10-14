<script lang="ts">
  // Year in review: a card layout meant to look good in a screenshot or on
  // paper. There's no share service — the user screenshots or prints it.
  // Only ever the signed-in user's own year (GET /users/me/year-in-review).
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { itemHref } from '$lib/itemHref';
  import {
    userStatsApi, fmtHours, dayLabel, monthLabel, pctChange, reviewYears, parseYearParam,
    artworkSrc, type YearInReview, type StatsTitle,
  } from '$lib/userStats';

  let data: YearInReview | null = null;
  let loading = true;
  let error = '';
  let loadedFor: number | null = null;
  let ready = false;
  const years = reviewYears();

  $: year = parseYearParam($page.params.year ?? '');
  $: if (ready && year !== loadedFor) load(year);

  onMount(() => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    ready = true;
  });

  async function load(y: number | null) {
    loadedFor = y;
    data = null;
    error = '';
    if (y === null) {
      error = 'That isn’t a year we can show.';
      loading = false;
      return;
    }
    loading = true;
    try {
      const res = await userStatsApi.yearInReview(y);
      if (loadedFor === y) data = res;
    } catch (e: unknown) {
      if (loadedFor === y) error = e instanceof Error ? e.message : 'Couldn’t load your year';
    } finally {
      if (loadedFor === y) loading = false;
    }
  }

  $: totalHours = data ? data.totals.hours_watched + data.totals.hours_listened : 0;
  $: prevHours = data?.previous_year
    ? data.previous_year.totals.hours_watched + data.previous_year.totals.hours_listened
    : 0;
  $: change = data?.previous_year ? pctChange(totalHours, prevHours) : null;
  $: maxMonth = Math.max(1, ...(data?.by_month ?? []).map((m) => m.ms));
  $: tops = data
    ? ([
        { title: 'Top movies', type: 'movie', items: data.top_movies, round: false },
        { title: 'Top shows', type: 'show', items: data.top_shows, round: false },
        { title: 'Top artists', type: 'artist', items: data.top_artists, round: true },
        { title: 'Top albums', type: 'album', items: data.top_albums, round: false },
      ] as { title: string; type: string; items: StatsTitle[]; round: boolean }[]).filter((t) => t.items.length > 0)
    : [];

  function firstPlayDate(iso: string): string {
    return new Date(iso).toLocaleDateString(undefined, { month: 'long', day: 'numeric' });
  }
  function printPage() {
    window.print();
  }
</script>

<svelte:head><title>{year ?? 'Year'} in review — OnScreen</title></svelte:head>

<div class="page">
  <nav class="toolbar" aria-label="Year in review">
    <a class="back" href="/stats">← My stats</a>
    <div class="years">
      {#each years as y}
        <a class="year-link" class:active={y === year} href="/year/{y}">{y}</a>
      {/each}
    </div>
    <button class="print-btn" on:click={printPage}>Print</button>
  </nav>

  {#if loading}
    <div class="skeleton"></div>
  {:else if error}
    <p class="error">{error}</p>
  {:else if data}
    {#if !data.has_data}
      <section class="card hero">
        <p class="eyebrow">Your {data.year} on OnScreen</p>
        <h1>Nothing played yet</h1>
        <p class="hero-sub">Watch, listen or read something and your year will start filling in here.</p>
      </section>
    {:else}
      <div class="cards">
        <section class="card hero">
          <p class="eyebrow">Your {data.year} on OnScreen</p>
          <div class="hero-number">{fmtHours(totalHours)}<span>hours</span></div>
          <p class="hero-sub">
            {fmtHours(data.totals.hours_watched)} hours watched · {fmtHours(data.totals.hours_listened)} hours listened
          </p>
          {#if change !== null}
            <p class="change" class:up={change >= 0}>
              {change >= 0 ? '▲' : '▼'} {Math.abs(change)}% {change >= 0 ? 'more' : 'less'} than {data.previous_year?.year}
            </p>
          {/if}
          <div class="hero-stats">
            <div><strong>{data.totals.plays.toLocaleString()}</strong><span>plays</span></div>
            <div><strong>{data.totals.items_finished.toLocaleString()}</strong><span>finished</span></div>
            <div><strong>{data.totals.books_read.toLocaleString()}</strong><span>books read</span></div>
            <div><strong>{data.totals.active_days.toLocaleString()}</strong><span>active days</span></div>
          </div>
        </section>

        {#each tops as t}
          <section class="card">
            <h2>{t.title}</h2>
            <ol class="top5">
              {#each t.items as it, i}
                <li>
                  <a href={itemHref(t.type, it.id)}>
                    <span class="rank">{i + 1}</span>
                    {#if it.poster_path}
                      <img class="thumb" class:round={t.round} src={artworkSrc(it.poster_path, 150)} alt="" />
                    {:else}
                      <span class="thumb placeholder" class:round={t.round}></span>
                    {/if}
                    <span class="name">
                      <span class="title">{it.title}</span>
                      <span class="meta">{it.parent_title || (it.year ?? '')}</span>
                    </span>
                  </a>
                </li>
              {/each}
            </ol>
          </section>
        {/each}

        {#if data.first_play}
          <section class="card moment">
            <h2>Where it started</h2>
            <a class="moment-body" href={itemHref(data.first_play.type, data.first_play.id)}>
              {#if data.first_play.poster_path}
                <img class="moment-poster" src={artworkSrc(data.first_play.poster_path, 300)} alt="" />
              {/if}
              <span>
                <span class="moment-label">Your first play of {data.year}, on {firstPlayDate(data.first_play.occurred_at)}</span>
                <span class="moment-title">{data.first_play.title}</span>
                {#if data.first_play.parent_title}<span class="moment-meta">{data.first_play.parent_title}</span>{/if}
              </span>
            </a>
          </section>
        {/if}

        <section class="card facts">
          <h2>Highlights</h2>
          <dl>
            {#if data.busiest_day}
              <div><dt>Busiest day</dt><dd>{dayLabel(data.busiest_day.date)} <span>· {data.busiest_day.plays} plays</span></dd></div>
            {/if}
            {#if data.longest_streak.days > 1}
              <div><dt>Longest streak</dt><dd>{data.longest_streak.days} days in a row</dd></div>
            {/if}
            {#if data.top_genres.length > 0}
              <div><dt>Favourite genre</dt><dd>{data.top_genres[0].genre}</dd></div>
            {/if}
            {#if data.top_device}
              <div><dt>Most-used device</dt><dd>{data.top_device.device}</dd></div>
            {/if}
            {#if data.new_artists.count > 0}
              <div>
                <dt>New artists discovered</dt>
                <dd>{data.new_artists.count}{#if data.new_artists.top.length > 0} <span>· including {data.new_artists.top.slice(0, 3).map((a) => a.title).join(', ')}</span>{/if}</dd>
              </div>
            {/if}
          </dl>
        </section>

        {#if data.top_genres.length > 1}
          <section class="card">
            <h2>Top genres</h2>
            <ol class="genres">
              {#each data.top_genres as g, i}
                <li><span class="rank">{i + 1}</span>{g.genre}</li>
              {/each}
            </ol>
          </section>
        {/if}

        <section class="card wide">
          <h2>Your year, month by month</h2>
          <div class="months" role="img" aria-label="Time played per month">
            {#each data.by_month as m}
              <div class="month">
                <div class="month-bar"><span style="height:{m.ms ? Math.max(3, (m.ms / maxMonth) * 100) : 0}%"></span></div>
                <span class="month-label">{monthLabel(m.month)}</span>
              </div>
            {/each}
          </div>
        </section>
      </div>
    {/if}
  {/if}
</div>

<style>
  .page { padding: 2rem 2.5rem; max-width: 1100px; }
  .toolbar { display: flex; align-items: center; gap: 1rem; flex-wrap: wrap; margin-bottom: 1.4rem; }
  .back { color: var(--text-secondary); text-decoration: none; font-size: 0.82rem; }
  .back:hover { color: var(--text-primary); }
  .years { display: flex; gap: 0.3rem; flex-wrap: wrap; flex: 1; }
  .year-link {
    padding: 0.3rem 0.7rem; border-radius: 7px; font-size: 0.78rem; text-decoration: none;
    color: var(--text-secondary); border: 1px solid var(--border-strong);
  }
  .year-link:hover { background: var(--bg-hover); }
  .year-link.active { background: var(--accent-bg); color: var(--accent-text); border-color: transparent; }
  .print-btn {
    padding: 0.38rem 0.85rem; border-radius: 7px; font-size: 0.78rem; font-family: inherit; cursor: pointer;
    border: 1px solid var(--border-strong); background: transparent; color: var(--text-secondary);
  }
  .print-btn:hover { background: var(--bg-hover); }

  .skeleton {
    height: 320px; border-radius: 16px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%; animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }
  .error { color: var(--error); }

  .cards { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 1rem; }
  .card {
    background: var(--bg-elevated); border: 1px solid var(--border); border-radius: 16px;
    padding: 1.3rem 1.4rem; min-width: 0; break-inside: avoid;
  }
  .card.wide, .card.hero { grid-column: 1 / -1; }
  h2 { font-size: 0.8rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.08em; color: var(--text-muted); margin: 0 0 0.9rem; }

  .hero {
    background:
      radial-gradient(circle at 85% 15%, var(--accent-bg), transparent 55%),
      linear-gradient(135deg, var(--accent-bg), transparent 70%),
      var(--bg-elevated);
    padding: 2rem 2.2rem;
  }
  .eyebrow { margin: 0 0 0.6rem; font-size: 0.85rem; font-weight: 600; color: var(--accent-text); letter-spacing: 0.02em; }
  .hero h1 { margin: 0 0 0.4rem; font-size: 1.8rem; color: var(--text-primary); }
  .hero-number { font-size: 4.2rem; font-weight: 800; line-height: 1; letter-spacing: -0.04em; color: var(--text-primary); }
  .hero-number span { font-size: 1.2rem; font-weight: 600; margin-left: 0.5rem; color: var(--text-secondary); letter-spacing: 0; }
  .hero-sub { margin: 0.6rem 0 0; color: var(--text-secondary); font-size: 0.92rem; }
  .change { margin: 0.6rem 0 0; font-size: 0.85rem; font-weight: 600; color: var(--text-secondary); }
  .change.up { color: var(--success); }
  .hero-stats { display: flex; gap: 2.2rem; flex-wrap: wrap; margin-top: 1.6rem; }
  .hero-stats div { display: flex; flex-direction: column; }
  .hero-stats strong { font-size: 1.6rem; font-weight: 700; color: var(--text-primary); letter-spacing: -0.02em; }
  .hero-stats span { font-size: 0.75rem; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.06em; }

  .top5 { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.45rem; }
  .top5 a { display: grid; grid-template-columns: 1.3rem 44px minmax(0, 1fr); gap: 0.75rem; align-items: center; text-decoration: none; color: inherit; }
  .rank { font-size: 1rem; font-weight: 800; color: var(--accent-text); text-align: right; font-variant-numeric: tabular-nums; }
  .thumb { width: 44px; height: 66px; object-fit: cover; border-radius: 6px; background: var(--bg-hover); display: block; }
  .thumb.round { height: 44px; border-radius: 50%; }
  .name { display: flex; flex-direction: column; min-width: 0; }
  .title { font-size: 0.9rem; font-weight: 600; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .meta { font-size: 0.76rem; color: var(--text-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  .moment-body { display: flex; gap: 1rem; align-items: center; text-decoration: none; color: inherit; }
  .moment-poster { width: 84px; border-radius: 8px; display: block; }
  .moment-body > span { display: flex; flex-direction: column; gap: 0.2rem; min-width: 0; }
  .moment-label { font-size: 0.8rem; color: var(--text-muted); }
  .moment-title { font-size: 1.2rem; font-weight: 700; color: var(--text-primary); }
  .moment-meta { font-size: 0.85rem; color: var(--text-secondary); }

  .facts dl { margin: 0; display: flex; flex-direction: column; gap: 0.8rem; }
  .facts dt { font-size: 0.75rem; color: var(--text-muted); }
  .facts dd { margin: 0.1rem 0 0; font-size: 1rem; font-weight: 600; color: var(--text-primary); }
  .facts dd span { font-weight: 400; color: var(--text-secondary); font-size: 0.85rem; }

  .genres { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.5rem; }
  .genres li { display: flex; gap: 0.75rem; align-items: baseline; font-size: 0.95rem; font-weight: 600; color: var(--text-primary); }

  .months { display: grid; grid-template-columns: repeat(12, minmax(0, 1fr)); gap: 6px; }
  .month { display: flex; flex-direction: column; align-items: center; gap: 0.35rem; }
  .month-bar { height: 110px; width: 100%; display: flex; align-items: flex-end; border-radius: 6px; background: var(--bg-hover); overflow: hidden; }
  .month-bar span { display: block; width: 100%; background: var(--accent); border-radius: 6px 6px 0 0; }
  .month-label { font-size: 0.7rem; color: var(--text-muted); }

  @media (max-width: 720px) {
    .page { padding: 1.2rem 1rem; }
    .cards { grid-template-columns: minmax(0, 1fr); }
    .hero { padding: 1.5rem; }
    .hero-number { font-size: 3.2rem; }
    .hero-stats { gap: 1.2rem; }
    .months { gap: 3px; }
  }

  /* Printing: just the cards. The app chrome lives in the root layout, so it
     is hidden from here. */
  @media print {
    .toolbar { display: none; }
    :global(.sidebar), :global(.audio-player) { display: none !important; }
    .page { padding: 0; max-width: none; }
    .card { break-inside: avoid; }
  }
</style>
