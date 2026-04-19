<script lang="ts">
  import FocusButton from '$lib/components/FocusButton.svelte';

  let lastPressed = $state('(none)');
</script>

<div class="page">
  <h1>OnScreen</h1>
  <p>Focus demo — arrow keys to navigate, Enter to activate, Backspace/Esc = Back.</p>

  <div class="grid">
    {#each Array(12) as _, i (i)}
      <FocusButton
        autofocus={i === 0}
        variant={i === 0 ? 'primary' : 'default'}
        onclick={() => (lastPressed = `button ${i + 1}`)}
      >
        Button {i + 1}
      </FocusButton>
    {/each}
  </div>

  <p class="status">Last pressed: {lastPressed}</p>
</div>

<style>
  .page {
    padding: var(--page-pad);
  }

  h1 {
    font-size: var(--font-2xl);
    margin: 0 0 16px;
    color: var(--accent);
  }

  p {
    font-size: var(--font-md);
    color: var(--text-secondary);
    margin: 0 0 48px;
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: var(--card-gap);
    max-width: 1200px;
  }

  .status {
    margin-top: 64px;
    font-size: var(--font-lg);
    color: var(--text-primary);
  }
</style>
