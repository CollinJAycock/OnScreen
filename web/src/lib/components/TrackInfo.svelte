<script lang="ts">
  import { createEventDispatcher, onMount } from 'svelte';
  import { lyricsApi, type ItemDetail, type ItemFile } from '$lib/api';

  export let track: ItemDetail;
  export let album: ItemDetail | null = null;

  const dispatch = createEventDispatcher<{ close: void }>();

  $: file = track.files[0] as ItemFile | undefined;

  // Lyrics load lazily on open so the modal doesn't block its own
  // paint while the server might be doing an LRCLIB fetch. "loading"
  // shows a spinner; empty plain + empty synced + !loading renders the
  // "No lyrics available" state so we don't pretend we checked when
  // we haven't.
  let lyricsPlain = '';
  let lyricsSynced = '';
  let lyricsLoading = true;
  let lyricsError = '';

  onMount(async () => {
    try {
      const res = await lyricsApi.get(track.id);
      lyricsPlain = res?.plain ?? '';
      lyricsSynced = res?.synced ?? '';
    } catch (e) {
      // Non-fatal: missing lyrics shouldn't block the info modal.
      // Log for devtools + render the empty state.
      lyricsError = e instanceof Error ? e.message : 'Failed to load lyrics';
    } finally {
      lyricsLoading = false;
    }
  });

  // Synced lyrics are LRC format: [mm:ss.xx]Line. Strip the timestamps
  // for the plain-text display fallback — we'll do karaoke-style
  // highlighting in a future pass, but "show readable text" is the
  // immediate win users care about.
  function stripLRCTimestamps(lrc: string): string {
    return lrc.replace(/\[\d{1,2}:\d{2}(?:[.:]\d{1,3})?\]/g, '').trim();
  }

  $: displayLyrics = lyricsPlain || (lyricsSynced ? stripLRCTimestamps(lyricsSynced) : '');

  function formatHz(hz?: number): string {
    if (!hz) return '';
    if (hz % 1000 === 0) return `${hz / 1000} kHz`;
    return `${(hz / 1000).toFixed(1)} kHz`;
  }

  function formatBitrate(bps?: number): string {
    if (!bps) return '';
    return `${Math.round(bps / 1000)} kbps`;
  }

  function formatGain(g?: number): string {
    if (g === undefined || g === null) return '';
    const sign = g > 0 ? '+' : '';
    return `${sign}${g.toFixed(2)} dB`;
  }

  function formatPeak(p?: number): string {
    if (p === undefined || p === null) return '';
    return p.toFixed(6);
  }

  function tier(f?: ItemFile): { label: string; cls: string } | null {
    if (!f?.lossless) {
      return f?.audio_codec ? { label: 'Lossy', cls: 'lossy' } : null;
    }
    const hiRes = (f.bit_depth ?? 0) > 16 || (f.sample_rate ?? 0) > 48000;
    if (hiRes) return { label: 'Hi-Res', cls: 'hires' };
    return { label: 'Lossless', cls: 'lossless' };
  }

  $: badge = tier(file);

  function close() { dispatch('close'); }

  function onBackdrop(e: MouseEvent) {
    if (e.target === e.currentTarget) close();
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') close();
  }
</script>

<svelte:window on:keydown={onKey} />

<div class="backdrop" on:click={onBackdrop} role="presentation">
  <div class="modal" role="dialog" aria-modal="true" aria-labelledby="ti-title">
    <header>
      <div class="title">
        <h2 id="ti-title">{track.title}</h2>
        {#if track.index !== undefined && track.index !== null}
          <div class="sub">Track {track.index}{album?.track_total ? ` of ${album.track_total}` : ''}</div>
        {/if}
      </div>
      <button class="x" on:click={close} aria-label="Close">×</button>
    </header>

    {#if file}
      {#if badge}
        <div class="badge-row">
          <span class="badge {badge.cls}">{badge.label}</span>
          {#if file.bit_depth && file.sample_rate}
            <span class="quality-line">{file.bit_depth}-bit · {formatHz(file.sample_rate)}</span>
          {:else if file.sample_rate}
            <span class="quality-line">{formatHz(file.sample_rate)}</span>
          {/if}
        </div>
      {/if}

      <section>
        <h3>File</h3>
        <dl>
          {#if file.container}<dt>Container</dt><dd>{file.container.toUpperCase()}</dd>{/if}
          {#if file.audio_codec}<dt>Codec</dt><dd>{file.audio_codec.toUpperCase()}</dd>{/if}
          {#if file.bit_depth}<dt>Bit depth</dt><dd>{file.bit_depth}-bit</dd>{/if}
          {#if file.sample_rate}<dt>Sample rate</dt><dd>{formatHz(file.sample_rate)}</dd>{/if}
          {#if file.channel_layout}<dt>Channels</dt><dd>{file.channel_layout}</dd>{/if}
          {#if file.bitrate}<dt>Bitrate</dt><dd>{formatBitrate(file.bitrate)}</dd>{/if}
          {#if file.duration_ms}<dt>Duration</dt><dd>{Math.round(file.duration_ms / 1000)}s</dd>{/if}
        </dl>
      </section>

      {#if file.replaygain_track_gain !== undefined || file.replaygain_album_gain !== undefined}
        <section>
          <h3>ReplayGain</h3>
          <dl>
            {#if file.replaygain_track_gain !== undefined}
              <dt>Track gain</dt><dd>{formatGain(file.replaygain_track_gain)}</dd>
            {/if}
            {#if file.replaygain_track_peak !== undefined}
              <dt>Track peak</dt><dd>{formatPeak(file.replaygain_track_peak)}</dd>
            {/if}
            {#if file.replaygain_album_gain !== undefined}
              <dt>Album gain</dt><dd>{formatGain(file.replaygain_album_gain)}</dd>
            {/if}
            {#if file.replaygain_album_peak !== undefined}
              <dt>Album peak</dt><dd>{formatPeak(file.replaygain_album_peak)}</dd>
            {/if}
          </dl>
        </section>
      {/if}
    {:else}
      <p class="empty">No file metadata available.</p>
    {/if}

    <section class="lyrics-section">
      <h3>Lyrics</h3>
      {#if lyricsLoading}
        <p class="empty">Loading…</p>
      {:else if displayLyrics}
        <pre class="lyrics">{displayLyrics}</pre>
      {:else if lyricsError}
        <p class="empty">Couldn't load lyrics.</p>
      {:else}
        <p class="empty">No lyrics available for this track.</p>
      {/if}
    </section>

    {#if track.musicbrainz_id || track.musicbrainz_release_id || track.musicbrainz_artist_id}
      <section>
        <h3>MusicBrainz</h3>
        <dl class="mb">
          {#if track.musicbrainz_id}
            <dt>Recording</dt>
            <dd><a href="https://musicbrainz.org/recording/{track.musicbrainz_id}" target="_blank" rel="noopener">{track.musicbrainz_id}</a></dd>
          {/if}
          {#if track.musicbrainz_release_id || album?.musicbrainz_release_id}
            <dt>Release</dt>
            <dd><a href="https://musicbrainz.org/release/{track.musicbrainz_release_id ?? album?.musicbrainz_release_id}" target="_blank" rel="noopener">{track.musicbrainz_release_id ?? album?.musicbrainz_release_id}</a></dd>
          {/if}
          {#if track.musicbrainz_release_group_id || album?.musicbrainz_release_group_id}
            <dt>Release group</dt>
            <dd><a href="https://musicbrainz.org/release-group/{track.musicbrainz_release_group_id ?? album?.musicbrainz_release_group_id}" target="_blank" rel="noopener">{track.musicbrainz_release_group_id ?? album?.musicbrainz_release_group_id}</a></dd>
          {/if}
          {#if track.musicbrainz_artist_id}
            <dt>Artist</dt>
            <dd><a href="https://musicbrainz.org/artist/{track.musicbrainz_artist_id}" target="_blank" rel="noopener">{track.musicbrainz_artist_id}</a></dd>
          {/if}
        </dl>
      </section>
    {/if}
  </div>
</div>

<style>
  .backdrop {
    position: fixed; inset: 0; background: rgba(0,0,0,0.6);
    display: flex; align-items: center; justify-content: center; z-index: 100;
    padding: 1rem;
  }
  .modal {
    background: var(--surface, #1a1a1a); color: var(--text, #eee);
    border-radius: 10px; padding: 1.5rem; max-width: 540px; width: 100%;
    max-height: 90vh; overflow-y: auto;
    box-shadow: 0 20px 60px rgba(0,0,0,0.5);
  }
  header {
    display: flex; align-items: flex-start; justify-content: space-between;
    gap: 1rem; margin-bottom: 1rem;
  }
  .title h2 { margin: 0 0 0.2rem; font-size: 1.2rem; line-height: 1.3; }
  .sub { color: var(--text-muted); font-size: 0.8rem; }
  .x {
    background: transparent; border: 0; color: var(--text-muted);
    font-size: 1.6rem; line-height: 1; cursor: pointer; padding: 0 0.5rem;
  }
  .x:hover { color: var(--text); }

  .badge-row { display: flex; align-items: center; gap: 0.6rem; margin-bottom: 1.2rem; }
  .badge {
    display: inline-block; padding: 0.2rem 0.55rem; border-radius: 999px;
    font-size: 0.7rem; font-weight: 700; letter-spacing: 0.05em; text-transform: uppercase;
  }
  .badge.hires { background: linear-gradient(135deg, #d4af37, #b8941f); color: #1a1a1a; }
  .badge.lossless { background: var(--accent, #4a9eff); color: white; }
  .badge.lossy { background: var(--surface-hover, #2a2a2a); color: var(--text-muted); }
  .quality-line { color: var(--text-secondary); font-size: 0.85rem; }

  section { margin-bottom: 1.2rem; }
  section:last-child { margin-bottom: 0; }
  section h3 {
    margin: 0 0 0.5rem; font-size: 0.75rem; text-transform: uppercase;
    letter-spacing: 0.08em; color: var(--text-muted); font-weight: 600;
  }
  dl {
    display: grid; grid-template-columns: 8rem 1fr; gap: 0.35rem 1rem;
    margin: 0; font-size: 0.88rem;
  }
  dt { color: var(--text-muted); }
  dd { margin: 0; font-variant-numeric: tabular-nums; }
  dl.mb dd { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.75rem; word-break: break-all; }
  dl.mb a { color: var(--accent); text-decoration: none; }
  dl.mb a:hover { text-decoration: underline; }

  .empty { color: var(--text-muted); margin: 0; }

  .lyrics-section { margin-top: 1.5rem; }
  .lyrics {
    margin: 0;
    padding: 0.9rem 1rem;
    background: var(--surface-hover, rgba(255,255,255,0.03));
    border-radius: 6px;
    color: var(--text, #eee);
    font-family: inherit;
    font-size: 0.9rem;
    line-height: 1.55;
    white-space: pre-wrap;
    word-wrap: break-word;
    max-height: 40vh;
    overflow-y: auto;
  }
</style>
