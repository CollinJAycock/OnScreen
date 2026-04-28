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
  .spinner {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 20px;
    padding: 80px;
    color: var(--text-secondary);
    font-size: var(--font-md);
  }

  .dot {
    width: 64px;
    height: 64px;
    border: 6px solid var(--border);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }

  @keyframes spin {
    to { transform: rotate(360deg); }
  }
</style>
