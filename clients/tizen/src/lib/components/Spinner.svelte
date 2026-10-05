<script lang="ts">
  interface Props {
    label?: string;
  }
  let { label = 'Loading…' }: Props = $props();
</script>

<div class="spinner">
  <div class="dot"></div>
  <div class="label">{label}</div>
</div>

<style>
  /* The label's margin stands in for flexbox `gap` (Chrome 84; webOS 6 runs
     Chromium 79). Flex items' margins don't collapse, so it's the same 24px. */
  .spinner {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 40px;
    color: var(--text-primary);
    font-size: var(--font-md);
  }

  .dot {
    width: 96px;
    height: 96px;
    /* Higher contrast than var(--border)'s rgba(255,255,255,.08) so
       the ring is visible at TV viewing distance against any backdrop. */
    border: 10px solid rgba(255, 255, 255, 0.25);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }

  .label {
    margin-top: 24px;
  }

  @keyframes spin {
    to { transform: rotate(360deg); }
  }
</style>
