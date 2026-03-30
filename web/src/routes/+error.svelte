<script lang="ts">
  import { page } from '$app/stores';
</script>

<svelte:head><title>{$page.status} — OnScreen</title></svelte:head>

<div class="error-page">
  <div class="error-code">{$page.status}</div>
  <div class="error-message">
    {#if $page.status === 404}
      Page not found
    {:else}
      Something went wrong
    {/if}
  </div>
  {#if $page.error?.message}
    <p class="error-detail">{$page.error.message}</p>
  {/if}
  <a href="/" class="back-link">Back to home</a>
</div>

<style>
  .error-page {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    min-height: 100vh;
    padding: 2rem;
    text-align: center;
    background: #0e0e14;
    color: #eeeef8;
  }
  .error-code {
    font-size: 5rem;
    font-weight: 900;
    color: #7c6af7;
    line-height: 1;
    margin-bottom: 0.5rem;
  }
  .error-message {
    font-size: 1.3rem;
    font-weight: 600;
    color: #aaaacc;
    margin-bottom: 1rem;
  }
  .error-detail {
    font-size: 0.85rem;
    color: #66667a;
    max-width: 400px;
    margin-bottom: 2rem;
  }
  .back-link {
    padding: 0.5rem 1.2rem;
    background: rgba(124,106,247,0.12);
    border: 1px solid rgba(124,106,247,0.25);
    border-radius: 8px;
    color: #a89ffa;
    font-size: 0.85rem;
    font-weight: 600;
    text-decoration: none;
    transition: background 0.15s;
  }
  .back-link:hover { background: rgba(124,106,247,0.2); }
</style>
