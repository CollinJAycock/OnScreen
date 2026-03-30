<script lang="ts">
  /** Size variant: "sm" (sidebar), "lg" (splash/login) */
  export let size: 'sm' | 'lg' = 'sm';
  /** Show the wordmark next to the icon */
  export let wordmark = true;
</script>

<span class="logo logo--{size}">
  <img src="/favicon.svg" alt="OnScreen" class="logo-icon" />
  {#if wordmark}
    <span class="logo-wordmark">OnScreen</span>
  {/if}
</span>

<style>
  .logo {
    display: inline-flex;
    align-items: center;
    gap: 0.55rem;
    text-decoration: none;
  }

  .logo-icon {
    display: block;
    flex-shrink: 0;
    border-radius: 20%;
    object-fit: cover;
  }

  .logo--sm .logo-icon  { width: 26px; height: 26px; }
  .logo--lg .logo-icon  { width: 56px; height: 56px; border-radius: 24%; }

  .logo-wordmark {
    font-weight: 700;
    letter-spacing: -0.02em;
    color: #eeeef8;
    line-height: 1;
  }

  .logo--sm .logo-wordmark { font-size: 0.95rem; }
  .logo--lg .logo-wordmark { font-size: 1.6rem; }
</style>
