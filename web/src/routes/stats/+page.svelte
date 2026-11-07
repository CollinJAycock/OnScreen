<script lang="ts">
  // My stats: the signed-in user's own viewing and listening numbers. Only
  // ever the caller's (GET /users/me/stats); admins have the server-wide
  // Analytics page for everyone else. Charts are plain HTML/CSS bars and a
  // CSS grid heatmap, like the Analytics page — no chart library.
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { itemHref } from '$lib/itemHref';
  import {
    userStatsApi, fmtHours, monthLabel, heatGrid, foldTypes, reviewYears,
    artworkSrc, type UserStats, type StatsRange, type StatsTitle,
  } from '$lib/userStats';
  import { locale, t, i18n, type MessageKey, type PluralKey, type Translator } from '$lib/i18n/index.svelte';

  const RANGES: { key: StatsRange; labelKey: MessageKey }[] = [
    { key: '30d', labelKey: 'stats.range30d' },
    { key: '90d', labelKey: 'stats.range90d' },
    { key: '365d', labelKey: 'stats.range12m' },
    { key: 'all', labelKey: 'stats.rangeAll' },
  ];
  const years = reviewYears();

  let range: StatsRange = '30d';
  let data: UserStats | null = null;
  let loading = true;
  let error = '';
  let seq = 0;

  onMount(() => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    load();
  });

  async function load() {
    const mine = ++seq;
    loading = true;
    error = '';
    try {
      const res = await userStatsApi.get(range);
      if (mine === seq) data = res;
    } catch (e: unknown) {
      if (mine === seq) error = e instanceof Error ? e.message : t('stats.loadFailed');
    } finally {
      if (mine === seq) loading = false;
    }
  }

  function setRange(r: StatsRange) {
    if (r === range) return;
    range = r;
    load();
  }

  function onYear(e: Event) {
    const v = (e.currentTarget as HTMLSelectElement).value;
    if (v) setRange(`year:${Number(v)}` as StatsRange);
  }

  $: months = data?.by_month ?? [];
  $: maxMonth = Math.max(1, ...months.map((m) => m.ms));
  $: heat = heatGrid(data?.heatmap ?? []);
  $: types = foldTypes(data?.by_type ?? []);
  $: maxType = Math.max(1, ...types.map((t) => t.ms));
  $: genres = data?.top_genres ?? [];
  $: maxGenre = Math.max(1, ...genres.map((g) => g.ms));
  $: devices = data?.devices ?? [];
  $: maxDevice = Math.max(1, ...devices.map((d) => d.plays));
  $: yearValue = range.startsWith('year:') ? range.slice(5) : '';
  $: showMonthYear = months.length > 12;

  $: topLists = data
    ? ([
        { titleKey: 'stats.topMovies', type: 'movie', items: data.top_titles.movies, unit: 'stats.plays' },
        { titleKey: 'stats.topShows', type: 'show', items: data.top_titles.shows, unit: 'stats.episodes' },
        { titleKey: 'stats.topArtists', type: 'artist', items: data.top_titles.artists, unit: 'stats.plays' },
        { titleKey: 'stats.topAlbums', type: 'album', items: data.top_titles.albums, unit: 'stats.plays' },
      ] as { titleKey: MessageKey; type: string; items: StatsTitle[]; unit: PluralKey }[]).filter((l) => l.items.length > 0)
    : [];

  function pct(v: number, max: number): number {
    return max > 0 ? Math.max(2, (v / max) * 100) : 0;
  }
  // Helpers used in the template take the translator, so the text
  // re-renders when the language changes.
  function hourLabel(h: number, tx: Translator): string {
    if (h === 0) return tx('stats.hourAm', { hour: '12' });
    if (h === 12) return tx('stats.hourPm', { hour: '12' });
    return h < 12 ? tx('stats.hourAm', { hour: String(h) }) : tx('stats.hourPm', { hour: String(h - 12) });
  }
  /** "12.5 h" / "45 min" (fmtDuration in $lib/userStats, translated). */
  function dur(ms: number, tx: Translator): string {
    if (!ms || ms < 0) return tx('stats.minutes', { n: 0 });
    const minutes = Math.round(ms / 60000);
    if (minutes < 60) return tx('stats.minutes', { n: minutes });
    const hours = ms / 3_600_000;
    return tx('stats.hours', { n: hours >= 100 ? Math.round(hours) : Math.round(hours * 10) / 10 });
  }
  // foldTypes() labels each row with an English name; these are its keys.
  const TYPE_KEYS: Record<string, MessageKey> = {
    Movies: 'stats.typeMovies', 'TV episodes': 'stats.typeEpisodes', Music: 'stats.typeMusic',
    Audiobooks: 'stats.typeAudiobooks', Podcasts: 'stats.typePodcasts', 'Music videos': 'stats.typeMusicVideos',
    'Home videos': 'stats.typeHomeVideos', Books: 'stats.typeBooks', Photos: 'stats.typePhotos',
  };
  // Mon..Sun, short, in the interface language. Reads $i18n so it re-runs
  // when the language changes. 2024-01-01 was a Monday.
  let weekdays: string[] = [];
  $: {
    void $i18n;
    const f = new Intl.DateTimeFormat(locale(), { weekday: 'short', timeZone: 'UTC' });
    weekdays = Array.from({ length: 7 }, (_, i) => f.format(new Date(Date.UTC(2024, 0, 1 + i))));
  }
</script>

<svelte:head><title>{$i18n('stats.pageTitle')}</title></svelte:head>

<div class="page">
  <header class="head">
    <div>
      <h1>{$i18n('nav.myStats')}</h1>
      <p class="sub">{$i18n('stats.subtitle')}</p>
    </div>
    <a class="review-link" href="/year/{years[0]}">{$i18n('stats.reviewLink', { year: String(years[0]) })}</a>
  </header>

  <div class="ranges" role="group" aria-label={$i18n('stats.timeRange')}>
    {#each RANGES as r}
      <button class="range-btn" class:active={range === r.key} aria-pressed={range === r.key} on:click={() => setRange(r.key)}>{$i18n(r.labelKey)}</button>
    {/each}
    <select class="year-select" class:active={yearValue !== ''} value={yearValue} on:change={onYear} aria-label={$i18n('stats.pickYear')}>
      <option value="" disabled>{$i18n('stats.yearOption')}</option>
      {#each years as y}<option value={String(y)}>{y}</option>{/each}
    </select>
  </div>

  {#if loading && !data}
    <div class="skeleton"></div>
  {:else if error && !data}
    <p class="error">{error}</p>
  {:else if data}
    <div class="tiles" class:dim={loading}>
      <div class="tile"><div class="tile-value">{fmtHours(data.totals.hours_watched)}</div><div class="tile-label">{$i18n('stats.hoursWatched')}</div></div>
      <div class="tile"><div class="tile-value">{fmtHours(data.totals.hours_listened)}</div><div class="tile-label">{$i18n('stats.hoursListened')}</div></div>
      <div class="tile"><div class="tile-value">{data.totals.plays.toLocaleString()}</div><div class="tile-label">{$i18n('stats.playsLabel')}</div></div>
      <div class="tile"><div class="tile-value">{data.totals.items_finished.toLocaleString()}</div><div class="tile-label">{$i18n('stats.finished')}</div></div>
      <div class="tile"><div class="tile-value">{data.totals.books_read.toLocaleString()}</div><div class="tile-label">{$i18n('stats.booksRead')}</div></div>
      <div class="tile">
        <div class="tile-value">{data.longest_streak.days}</div>
        <div class="tile-label">{$i18n('stats.longestStreak')}</div>
      </div>
    </div>

    {#if data.totals.plays === 0 && data.totals.books_read === 0}
      <p class="empty">{$i18n('stats.empty')}</p>
    {:else}
      <div class="grid" class:dim={loading}>
        <section class="panel wide">
          <h2>{$i18n('stats.activityByMonth')}</h2>
          {#if months.length === 0}
            <p class="muted small">{$i18n('stats.noActivity')}</p>
          {:else}
            <div class="bars" role="img" aria-label={$i18n('stats.timePerMonth')}>
              {#each months as m}
                <div class="bar-col" title={$i18n('stats.monthTip', { month: monthLabel(m.month, true), duration: dur(m.ms, $i18n), plays: $i18n.n('stats.plays', m.plays) })}>
                  <div class="bar-fill" style="height:{m.ms ? pct(m.ms, maxMonth) : 0}%"></div>
                </div>
              {/each}
            </div>
            <div class="bar-labels" style="grid-template-columns:repeat({months.length}, 1fr)">
              {#each months as m, i}
                <span>{months.length <= 13 || i % Math.ceil(months.length / 12) === 0 ? monthLabel(m.month, showMonthYear) : ''}</span>
              {/each}
            </div>
          {/if}
        </section>

        <section class="panel wide">
          <h2>{$i18n('stats.whenYouPlay')}</h2>
          <div class="heat" role="img" aria-label={$i18n('stats.playsByHour')}>
            <span></span>
            {#each Array(24) as _, h}<span class="heat-hour">{h % 3 === 0 ? hourLabel(h, $i18n) : ''}</span>{/each}
            {#each heat.grid as row, d}
              <span class="heat-day">{weekdays[d]}</span>
              {#each row as n, h}
                <span class="heat-cell" title={$i18n('stats.heatTip', { day: weekdays[d], hour: hourLabel(h, $i18n), plays: $i18n.n('stats.plays', n) })}>
                  {#if n > 0}<span class="heat-fill" style="opacity:{0.2 + 0.8 * (n / heat.max)}"></span>{/if}
                </span>
              {/each}
            {/each}
          </div>
        </section>

        {#if types.length > 0}
          <section class="panel">
            <h2>{$i18n('stats.byType')}</h2>
            <div class="hbars">
              {#each types as t}
                <div class="hbar-row">
                  <span class="hbar-label">{TYPE_KEYS[t.label] ? $i18n(TYPE_KEYS[t.label]) : t.label}</span>
                  <span class="hbar-track"><span class="hbar-fill" style="width:{pct(t.ms, maxType)}%"></span></span>
                  <span class="hbar-value">{dur(t.ms, $i18n)}</span>
                </div>
              {/each}
            </div>
          </section>
        {/if}

        {#if genres.length > 0}
          <section class="panel">
            <h2>{$i18n('stats.topGenres')}</h2>
            <div class="hbars">
              {#each genres as g}
                <div class="hbar-row">
                  <span class="hbar-label">{g.genre}</span>
                  <span class="hbar-track"><span class="hbar-fill" style="width:{pct(g.ms, maxGenre)}%"></span></span>
                  <span class="hbar-value">{dur(g.ms, $i18n)}</span>
                </div>
              {/each}
            </div>
          </section>
        {/if}

        {#each topLists as list}
          <section class="panel">
            <h2>{$i18n(list.titleKey)}</h2>
            <ol class="top-list">
              {#each list.items as t, i}
                <li>
                  <a class="top-row" href={itemHref(list.type, t.id)}>
                    <span class="top-rank">{i + 1}</span>
                    {#if t.poster_path}
                      <img class="top-thumb" class:round={list.type === 'artist'} src={artworkSrc(t.poster_path, 150)} alt="" loading="lazy" />
                    {:else}
                      <span class="top-thumb placeholder" class:round={list.type === 'artist'}></span>
                    {/if}
                    <span class="top-info">
                      <span class="top-title">{t.title}</span>
                      {#if t.parent_title}<span class="top-meta">{t.parent_title}</span>{:else if t.year}<span class="top-meta">{t.year}</span>{/if}
                    </span>
                    <span class="top-count">{$i18n.n(list.unit, t.plays)}</span>
                  </a>
                </li>
              {/each}
            </ol>
          </section>
        {/each}

        {#if devices.length > 0}
          <section class="panel">
            <h2>{$i18n('stats.devices')}</h2>
            <div class="hbars">
              {#each devices as d}
                <div class="hbar-row">
                  <span class="hbar-label" title={d.device}>{d.device}</span>
                  <span class="hbar-track"><span class="hbar-fill alt" style="width:{pct(d.plays, maxDevice)}%"></span></span>
                  <span class="hbar-value">{$i18n.n('stats.plays', d.plays)}</span>
                </div>
              {/each}
            </div>
          </section>
        {/if}
      </div>
    {/if}

    <p class="foot">
      {#each $i18n.rich('stats.foot', { accountPage: $i18n('stats.accountPage') }) as seg}{#if seg.param}<a href="/account">{seg.text}</a>{:else}{seg.text}{/if}{/each}
    </p>
  {/if}
</div>

<style>
  .page { padding: 2rem 2.5rem; max-width: 1200px; }
  .head { display: flex; justify-content: space-between; align-items: flex-start; gap: 1rem; flex-wrap: wrap; margin-bottom: 1.1rem; }
  h1 { font-size: 1.4rem; font-weight: 700; color: var(--text-primary); margin: 0 0 0.25rem; letter-spacing: -0.02em; }
  .sub { color: var(--text-muted); font-size: 0.85rem; margin: 0; }
  .review-link {
    color: var(--accent-text); background: var(--accent-bg); text-decoration: none;
    padding: 0.5rem 0.9rem; border-radius: 8px; font-size: 0.82rem; font-weight: 600; white-space: nowrap;
  }
  .review-link:hover { background: var(--bg-hover); }

  .ranges { display: flex; flex-wrap: wrap; gap: 0.4rem; margin-bottom: 1.4rem; }
  .range-btn, .year-select {
    padding: 0.38rem 0.8rem; border-radius: 7px; font-size: 0.78rem; font-weight: 500; font-family: inherit;
    border: 1px solid var(--border-strong); background: transparent; color: var(--text-secondary); cursor: pointer;
  }
  .year-select { width: auto; background: var(--input-bg); }
  .range-btn:hover, .year-select:hover { background: var(--bg-hover); }
  .range-btn.active, .year-select.active { background: var(--accent-bg); color: var(--accent-text); border-color: transparent; }

  .skeleton {
    height: 220px; border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%; animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }
  .error { color: var(--error); }
  .muted { color: var(--text-muted); }
  .small { font-size: 0.82rem; }
  .dim { opacity: 0.6; transition: opacity 0.15s; }
  .empty { color: var(--text-secondary); font-size: 0.9rem; padding: 1.5rem 0; }

  .tiles { display: grid; grid-template-columns: repeat(6, minmax(0, 1fr)); gap: 0.8rem; margin-bottom: 1.4rem; }
  .tile { background: var(--bg-elevated); border: 1px solid var(--border); border-radius: 10px; padding: 1rem 1.1rem; }
  .tile-value { font-size: 1.6rem; font-weight: 700; letter-spacing: -0.03em; color: var(--text-primary); }
  .tile-label { font-size: 0.72rem; color: var(--text-muted); margin-top: 0.2rem; text-transform: uppercase; letter-spacing: 0.06em; }

  .grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 1.1rem; }
  .panel { background: var(--bg-elevated); border: 1px solid var(--border); border-radius: 10px; padding: 1.1rem 1.3rem; min-width: 0; }
  .panel.wide { grid-column: 1 / -1; }
  h2 { font-size: 0.92rem; font-weight: 600; color: var(--text-primary); margin: 0 0 0.9rem; }

  .bars { display: flex; align-items: flex-end; gap: 4px; height: 120px; }
  .bar-col { flex: 1; height: 100%; display: flex; align-items: flex-end; }
  .bar-fill { width: 100%; background: var(--accent); border-radius: 3px 3px 0 0; min-height: 0; }
  .bar-col:hover .bar-fill { background: var(--accent-text); }
  .bar-labels { display: grid; gap: 4px; margin-top: 0.35rem; font-size: 0.68rem; color: var(--text-muted); text-align: center; }
  .bar-labels span { overflow: hidden; white-space: nowrap; }

  .heat { display: grid; grid-template-columns: 2.4rem repeat(24, minmax(0, 1fr)); gap: 3px; align-items: center; }
  .heat-hour { font-size: 0.62rem; color: var(--text-muted); text-align: left; white-space: nowrap; }
  .heat-day { font-size: 0.7rem; color: var(--text-muted); }
  .heat-cell { position: relative; aspect-ratio: 1; border-radius: 3px; background: var(--bg-hover); overflow: hidden; }
  .heat-fill { position: absolute; inset: 0; background: var(--accent); }

  .hbars { display: flex; flex-direction: column; gap: 0.55rem; }
  .hbar-row { display: grid; grid-template-columns: minmax(0, 9rem) 1fr auto; gap: 0.7rem; align-items: center; font-size: 0.8rem; }
  .hbar-label { color: var(--text-secondary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .hbar-track { height: 8px; border-radius: 4px; background: var(--bg-hover); overflow: hidden; }
  .hbar-fill { display: block; height: 100%; background: var(--accent); border-radius: 4px; }
  .hbar-fill.alt { background: var(--info); }
  .hbar-value { color: var(--text-muted); font-size: 0.75rem; white-space: nowrap; }

  .top-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.35rem; }
  .top-row {
    display: grid; grid-template-columns: 1.2rem 40px minmax(0, 1fr) auto; gap: 0.7rem; align-items: center;
    padding: 0.3rem 0.4rem; border-radius: 7px; text-decoration: none; color: inherit;
  }
  .top-row:hover { background: var(--bg-hover); }
  .top-rank { font-size: 0.8rem; color: var(--text-muted); text-align: right; font-variant-numeric: tabular-nums; }
  .top-thumb { width: 40px; height: 60px; object-fit: cover; border-radius: 4px; background: var(--bg-hover); display: block; }
  .top-thumb.round { height: 40px; border-radius: 50%; }
  .top-info { display: flex; flex-direction: column; min-width: 0; }
  .top-title { font-size: 0.84rem; font-weight: 600; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .top-meta { font-size: 0.74rem; color: var(--text-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .top-count { font-size: 0.74rem; color: var(--text-secondary); white-space: nowrap; }

  .foot { margin-top: 1.6rem; font-size: 0.8rem; color: var(--text-muted); }
  .foot a { color: var(--accent-text); }

  @media (max-width: 1000px) {
    .tiles { grid-template-columns: repeat(3, minmax(0, 1fr)); }
  }
  @media (max-width: 720px) {
    .page { padding: 1.2rem 1rem; }
    .grid { grid-template-columns: minmax(0, 1fr); }
    .tiles { grid-template-columns: repeat(2, minmax(0, 1fr)); }
    .heat { grid-template-columns: 2rem repeat(24, minmax(0, 1fr)); gap: 2px; }
    .heat-hour { font-size: 0.55rem; }
  }
</style>
