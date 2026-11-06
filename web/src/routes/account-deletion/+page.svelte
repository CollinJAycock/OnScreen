<script lang="ts">
  import { tr } from '$lib/i18n/index.svelte';

  const site = 'onscreen.wolverscreen.com';
  const email = 'collin.j.aycock@gmail.com';
  const issues = 'github.com/CollinJAycock/OnScreen/issues';
</script>

<svelte:head>
  <title>{$tr('accountDeletion.pageTitle')}</title>
  <meta name="description" content={$tr('accountDeletion.metaDescription')} />
</svelte:head>

<div class="page">
  <header class="header">
    <a href="/" class="brand">
      <img src="/favicon-96x96.png" alt="" width="36" height="36" />
      <span>OnScreen</span>
    </a>
  </header>

  <main class="content">
    <h1>{$tr('accountDeletion.title')}</h1>
    <p class="updated">{$tr('accountDeletion.updated', { date: '2026-10-01' })}</p>

    <p>{$tr('accountDeletion.intro')}</p>

    <h2>{$tr('accountDeletion.selfHostedHeading')}</h2>

    <p>{$tr('accountDeletion.selfHostedData')}</p>

    <p>
      {#each $tr.rich('accountDeletion.selfHostedAsk', { path: $tr('accountDeletion.settingsUsers') }) as seg}{#if seg.param}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}
    </p>

    <h2>{$tr('accountDeletion.hostedHeading')}</h2>

    <p>
      {#each $tr.rich('accountDeletion.hostedHow', { site, email }) as seg}{#if seg.param === 'site'}<a href="https://onscreen.wolverscreen.com" rel="noopener">{seg.text}</a>{:else if seg.param === 'email'}<a href="mailto:collin.j.aycock@gmail.com?subject=OnScreen%20account%20deletion%20request">{seg.text}</a>{:else}{seg.text}{/if}{/each}
    </p>

    <p>{$tr('accountDeletion.hostedTiming')}</p>

    <h2>{$tr('accountDeletion.deletedHeading')}</h2>

    <p>{$tr('accountDeletion.deletedIntro')}</p>

    <ul>
      <li>{$tr('accountDeletion.deletedUser')}</li>
      <li>{$tr('accountDeletion.deletedHistory')}</li>
      <li>{$tr('accountDeletion.deletedFavorites')}</li>
      <li>{$tr('accountDeletion.deletedProfiles')}</li>
      <li>{$tr('accountDeletion.deletedSessions')}</li>
      <li>{$tr('accountDeletion.deletedApiTokens')}</li>
      <li>{$tr('accountDeletion.deletedNotifications')}</li>
    </ul>

    <p>
      {#each $tr.rich('accountDeletion.keptIntro', { not: $tr('accountDeletion.not') }) as seg}{#if seg.param}<em>{seg.text}</em>{:else}{seg.text}{/if}{/each}
    </p>

    <ul>
      <li>{$tr('accountDeletion.keptMedia')}</li>
      <li>{$tr('accountDeletion.keptAggregates')}</li>
    </ul>

    <h2>{$tr('accountDeletion.localHeading')}</h2>

    <p>{$tr('accountDeletion.localIntro')}</p>

    <ul>
      <li>{$tr('accountDeletion.localServerUrl')}</li>
      <li>{$tr('accountDeletion.localTokens')}</li>
      <li>{$tr('accountDeletion.localImageCache')}</li>
    </ul>

    <p>{$tr('accountDeletion.localRemove')}</p>

    <h2>{$tr('accountDeletion.retentionHeading')}</h2>

    <p>{$tr('accountDeletion.retention')}</p>

    <h2>{$tr('accountDeletion.contactHeading')}</h2>

    <p>
      {#each $tr.rich('accountDeletion.contact', { email, issues }) as seg}{#if seg.param === 'email'}<a href="mailto:collin.j.aycock@gmail.com">{seg.text}</a>{:else if seg.param === 'issues'}<a href="https://github.com/CollinJAycock/OnScreen/issues" rel="noopener">{seg.text}</a>{:else}{seg.text}{/if}{/each}
    </p>
  </main>
</div>

<style>
  .page {
    min-height: 100vh;
    background: var(--bg-primary, #0a0a0a);
    color: var(--text-primary, #f4f4f5);
  }

  .header {
    border-bottom: 1px solid var(--border, rgba(255, 255, 255, 0.08));
    padding: 16px 24px;
  }

  .brand {
    display: inline-flex;
    align-items: center;
    gap: 12px;
    color: inherit;
    text-decoration: none;
    font-weight: 600;
    font-size: 18px;
  }

  .content {
    max-width: 720px;
    margin: 0 auto;
    padding: 48px 24px 96px;
    line-height: 1.65;
  }

  h1 {
    font-size: 32px;
    margin: 0 0 8px;
  }

  h2 {
    font-size: 20px;
    margin: 32px 0 12px;
  }

  .updated {
    color: var(--text-secondary, rgba(255, 255, 255, 0.6));
    margin: 0 0 32px;
    font-size: 14px;
  }

  p, ul {
    margin: 0 0 16px;
  }

  ul {
    padding-left: 24px;
  }

  li {
    margin-bottom: 8px;
  }

  a {
    color: var(--accent, #60a5fa);
    text-decoration: underline;
    text-underline-offset: 2px;
  }

  code {
    background: rgba(255, 255, 255, 0.06);
    padding: 1px 6px;
    border-radius: 4px;
    font-size: 0.92em;
  }
</style>
