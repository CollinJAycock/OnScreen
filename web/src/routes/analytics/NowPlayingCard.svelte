<script lang="ts">
  // One Now Playing card: what's playing, who is watching on which device,
  // LAN or remote, how it's delivered (direct play … transcode) with the
  // source → output shape and, for a remux/transcode, why.
  import { assetUrl, type ActiveSession } from '$lib/api';
  import {
    canStop, decisionLabel, decisionTone, fmtClock, locationLabel, sourceOutput,
    viewerInitial, viewerLabel,
  } from './now-playing';

  interface Props {
    session: ActiveSession;
    /** A stop for this card is in flight. */
    stopping?: boolean;
    onstop?: (s: ActiveSession) => void;
  }

  let { session: s, stopping = false, onstop }: Props = $props();

  const pct = $derived(s.duration_ms && s.duration_ms > 0 ? Math.min(100, (s.position_ms / s.duration_ms) * 100) : 0);
  const viewer = $derived(viewerLabel(s));
  const location = $derived(locationLabel(s.location));
  const tone = $derived(decisionTone(s.decision));
  const formats = $derived(sourceOutput(s));
  const reasons = $derived(s.decision !== 'directPlay' ? (s.transcode_reasons ?? []) : []);

  // Artwork paths are filesystem-derived: encode each segment (handles # ? in
  // filenames) while keeping the / separators.
  function artworkSrc(path: string, w: number): string {
    const encoded = path.split('/').map(encodeURIComponent).join('/');
    return assetUrl(`/artwork/${encoded}?w=${w}`);
  }
</script>

<div class="stream-card" data-testid="stream-card">
  {#if s.poster_path}
    <img class="stream-poster" src={artworkSrc(s.poster_path, 150)}
         srcset="{artworkSrc(s.poster_path, 75)} 75w, {artworkSrc(s.poster_path, 150)} 150w, {artworkSrc(s.poster_path, 300)} 300w"
         sizes="80px"
         alt={s.title} />
  {:else}
    <div class="stream-poster placeholder"></div>
  {/if}
  <div class="stream-info">
    <div class="stream-title">
      {#if s.parent_title}<span class="muted">{s.parent_title} · </span>{/if}{s.title}{#if s.year} <span class="muted">({s.year})</span>{/if}
    </div>
    <div class="stream-who">
      {#if viewer}
        <span class="avatar" aria-hidden="true">{viewerInitial(s)}</span>
        <span class="viewer">{viewer}</span>
      {/if}
      {#if s.client_name}<span class="muted device">{viewer ? 'on ' : ''}{s.client_name}</span>{/if}
      {#if location}
        <span class="chip location" class:remote={s.location === 'remote'}
              title={s.client_ip ? `Client address ${s.client_ip}` : undefined}>{location}</span>
      {/if}
      {#if s.client_ip}<span class="muted ip">{s.client_ip}</span>{/if}
    </div>
    <div class="stream-meta">
      <span class="chip decision {tone}">{decisionLabel(s.decision)}</span>
      {#if s.selected_rendition}<span class="muted">· {s.selected_rendition}</span>{/if}
      {#if formats.source || formats.output}
        <span class="formats" title="Source → what the viewer receives">
          {formats.source || 'Unknown source'}{#if formats.output} <span class="arrow">→</span> {formats.output}{/if}
        </span>
      {:else if s.bitrate_kbps}
        <span class="muted">· {(s.bitrate_kbps / 1000).toFixed(1)} Mbps</span>
      {/if}
    </div>
    {#if reasons.length > 0}
      <ul class="reasons" aria-label="Why this isn't a direct play">
        {#each reasons as r}<li>{r}</li>{/each}
      </ul>
    {/if}
    <div class="stream-progress-track">
      <div class="stream-progress-fill" style="width:{pct}%"></div>
    </div>
    <div class="stream-times muted">
      {fmtClock(s.position_ms)}{#if s.duration_ms} / {fmtClock(s.duration_ms)}{/if}
    </div>
  </div>
  {#if canStop(s)}
    <button class="stream-stop" title="Stop this stream"
            disabled={stopping}
            onclick={() => onstop?.(s)}>
      {stopping ? 'Stopping…' : 'Stop'}
    </button>
  {/if}
</div>

<style>
  .stream-card { display: flex; align-items: center; gap: 0.9rem; }
  .stream-poster {
    width: 44px; height: 64px; border-radius: 4px;
    object-fit: cover; flex-shrink: 0; background: var(--bg-secondary);
  }
  .stream-poster.placeholder { background: var(--bg-secondary); }
  .stream-info { flex: 1; min-width: 0; }
  .muted { color: var(--text-muted); }
  .stream-title {
    font-size: 0.85rem; font-weight: 600; color: var(--text-primary);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
    margin-bottom: 0.2rem;
  }
  .stream-who {
    display: flex; align-items: center; flex-wrap: wrap; gap: 0.35rem;
    font-size: 0.74rem; color: var(--text-secondary); margin-bottom: 0.25rem;
  }
  .avatar {
    width: 18px; height: 18px; border-radius: 50%;
    display: inline-flex; align-items: center; justify-content: center;
    background: var(--accent-bg); color: var(--accent-text);
    font-size: 0.62rem; font-weight: 700; flex-shrink: 0;
  }
  .viewer { color: var(--text-primary); font-weight: 500; }
  .ip { font-variant-numeric: tabular-nums; font-size: 0.68rem; }
  .chip {
    display: inline-block; padding: 1px 6px; border-radius: 999px;
    font-size: 0.64rem; font-weight: 600; letter-spacing: 0.02em;
    border: 1px solid currentColor; line-height: 1.4;
  }
  .chip.location { color: #3af7a0; }
  .chip.location.remote { color: #f7c948; }
  .chip.decision.direct { color: #3ab8f7; }
  .chip.decision.stream { color: #7c6af7; }
  .chip.decision.transcode { color: #f7a03a; }
  .stream-meta {
    font-size: 0.72rem; color: var(--text-muted); margin-bottom: 0.3rem;
    display: flex; align-items: center; flex-wrap: wrap; gap: 0.4rem;
  }
  .formats { color: var(--text-secondary); }
  .arrow { color: var(--text-muted); }
  .reasons {
    margin: 0 0 0.35rem; padding-left: 1rem;
    font-size: 0.7rem; color: #f7a03a; line-height: 1.35;
  }
  .stream-stop {
    flex-shrink: 0; padding: 0.3rem 0.7rem;
    background: transparent; color: var(--text-muted);
    border: 1px solid var(--border); border-radius: 6px;
    font-size: 0.75rem; cursor: pointer;
    transition: color 0.15s, border-color 0.15s;
  }
  .stream-stop:hover:not(:disabled) { color: #f87171; border-color: #f87171; }
  .stream-stop:disabled { opacity: 0.6; cursor: default; }
  .stream-progress-track {
    height: 3px; background: var(--border-strong);
    border-radius: 2px; overflow: hidden; margin-bottom: 0.25rem;
  }
  .stream-progress-fill {
    height: 100%; background: var(--accent);
    border-radius: 2px; transition: width 1s linear;
  }
  .stream-times { font-size: 0.68rem; }
  @media (max-width: 640px) {
    .stream-card { align-items: flex-start; }
    .ip { display: none; }
  }
</style>
