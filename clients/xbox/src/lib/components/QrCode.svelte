<script lang="ts">
  // A QR code for a URL the user opens on their phone (the TV can't open
  // Trakt's / Last.fm's approval pages itself). Encoded by the in-tree
  // encoder ($lib/qr) and drawn as one SVG path on a white quiet zone —
  // no canvas, no dependency. Renders nothing if the text doesn't fit.
  import { i18n } from '$lib/i18n/index.svelte';
  import { encodeQr, qrPath } from '$lib/qr';

  interface Props {
    text: string;
    /** Rendered width/height in px. */
    size?: number;
    /** Defaults to "QR code" in the app's language. */
    label?: string;
  }
  let { text, size = 320, label }: Props = $props();

  const qr = $derived(text ? encodeQr(text, 'M') : null);
  // 4-module quiet zone on each side, as the standard asks.
  const dim = $derived(qr ? qr.size + 8 : 0);
  const path = $derived(qr ? qrPath(qr, 4) : '');
</script>

{#if qr}
  <svg
    class="qr"
    width={size}
    height={size}
    viewBox="0 0 {dim} {dim}"
    role="img"
    aria-label={label ?? $i18n('browse.qrCode')}
    shape-rendering="crispEdges"
  >
    <rect width={dim} height={dim} fill="#ffffff" />
    <path d={path} fill="#000000" />
  </svg>
{/if}

<style>
  .qr {
    display: block;
    border-radius: 8px;
    flex: 0 0 auto;
  }
</style>
