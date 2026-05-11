<script lang="ts">
  import { focusable } from '$lib/focus/focusable';
  import { api } from '$lib/api';

  interface Props {
    title: string;
    posterPath?: string;
    subtitle?: string;
    progressRatio?: number;
    onclick?: () => void;
    autofocus?: boolean;
  }
  let { title, posterPath, subtitle, progressRatio, onclick, autofocus }: Props = $props();

  // api.assetUrl handles origin + `?token=<paseto>` for the
  // RequiredAllowQueryToken-protected /artwork/ route. The naive
  // `${origin}/artwork/...?w=400` URL omits auth and 401s — `<img>`
  // can't attach an Authorization header.
  const posterUrl = $derived(posterPath ? api.assetUrl(`/artwork/${posterPath}?w=400`) : '');
</script>

<button use:focusable={{ autofocus }} class="card" {onclick}>
  {#if posterUrl}
    <img src={posterUrl} alt="" loading="lazy" />
  {:else}
    <div class="no-poster">{title.slice(0, 2).toUpperCase()}</div>
  {/if}
  {#if progressRatio !== undefined && progressRatio > 0}
    <div class="progress" style="width: {Math.min(100, progressRatio * 100)}%"></div>
  {/if}
  <div class="label">
    <div class="title">{title}</div>
    {#if subtitle}<div class="subtitle">{subtitle}</div>{/if}
  </div>
</button>

<style>
  .card {
    display: block;
    width: 240px;
    background: transparent;
    border: none;
    padding: 0;
    text-align: left;
    font-family: inherit;
    color: var(--text-primary);
    cursor: pointer;
    flex: 0 0 auto;
  }

  img, .no-poster {
    width: 240px;
    height: 360px;
    border-radius: 12px;
    background: var(--bg-elevated);
    object-fit: cover;
    display: block;
  }

  .no-poster {
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: var(--font-xl);
    color: var(--text-muted);
  }

  .progress {
    margin-top: -6px;
    height: 4px;
    background: var(--accent);
    border-radius: 2px;
    position: relative;
    z-index: 1;
  }

  .label {
    padding: 12px 4px 0;
  }

  .title {
    font-size: var(--font-sm);
    line-height: 1.3;
    overflow: hidden;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
  }

  .subtitle {
    font-size: var(--font-xs);
    color: var(--text-secondary);
    margin-top: 4px;
  }
</style>
