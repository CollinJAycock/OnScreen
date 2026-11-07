<script lang="ts">
  import { i18n } from '$lib/i18n/index.svelte';

  const site = 'onscreen.wolverscreen.com';
  const email = 'collin.j.aycock@gmail.com';
  const issues = 'github.com/CollinJAycock/OnScreen/issues';
</script>

<svelte:head>
  <title>{$i18n('accountDeletion.pageTitle')}</title>
  <meta name="description" content={$i18n('accountDeletion.metaDescription')} />
</svelte:head>

<div class="page">
  <header class="header">
    <a href="/" class="brand">
      <img src="/favicon-96x96.png" alt="" width="36" height="36" />
      <span>OnScreen</span>
    </a>
  </header>

  <main class="content">
    <h1>{$i18n('accountDeletion.title')}</h1>
    <p class="updated">{$i18n('accountDeletion.updated', { date: '2026-10-01' })}</p>

    <p>{$i18n('accountDeletion.intro')}</p>

    <h2>{$i18n('accountDeletion.selfHostedHeading')}</h2>

    <p>{$i18n('accountDeletion.selfHostedData')}</p>

    <p>
      {#each $i18n.rich('accountDeletion.selfHostedAsk', { path: $i18n('accountDeletion.settingsUsers') }) as seg}{#if seg.param}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}
    </p>

    <h2>{$i18n('accountDeletion.hostedHeading')}</h2>

    <p>
      {#each $i18n.rich('accountDeletion.hostedHow', { site, email }) as seg}{#if seg.param === 'site'}<a href="https://onscreen.wolverscreen.com" rel="noopener">{seg.text}</a>{:else if seg.param === 'email'}<a href="mailto:collin.j.aycock@gmail.com?subject=OnScreen%20account%20deletion%20request">{seg.text}</a>{:else}{seg.text}{/if}{/each}
    </p>

    <p>{$i18n('accountDeletion.hostedTiming')}</p>

    <h2>{$i18n('accountDeletion.deletedHeading')}</h2>

    <p>{$i18n('accountDeletion.deletedIntro')}</p>

    <ul>
      <li>{$i18n('accountDeletion.deletedUser')}</li>
      <li>{$i18n('accountDeletion.deletedHistory')}</li>
      <li>{$i18n('accountDeletion.deletedFavorites')}</li>
      <li>{$i18n('accountDeletion.deletedProfiles')}</li>
      <li>{$i18n('accountDeletion.deletedSessions')}</li>
      <li>{$i18n('accountDeletion.deletedApiTokens')}</li>
      <li>{$i18n('accountDeletion.deletedNotifications')}</li>
    </ul>

    <p>
      {#each $i18n.rich('accountDeletion.keptIntro', { not: $i18n('accountDeletion.not') }) as seg}{#if seg.param}<em>{seg.text}</em>{:else}{seg.text}{/if}{/each}
    </p>

    <ul>
      <li>{$i18n('accountDeletion.keptMedia')}</li>
      <li>{$i18n('accountDeletion.keptAggregates')}</li>
    </ul>

    <h2>{$i18n('accountDeletion.localHeading')}</h2>

    <p>{$i18n('accountDeletion.localIntro')}</p>

    <ul>
      <li>{$i18n('accountDeletion.localServerUrl')}</li>
      <li>{$i18n('accountDeletion.localTokens')}</li>
      <li>{$i18n('accountDeletion.localImageCache')}</li>
    </ul>

    <p>{$i18n('accountDeletion.localRemove')}</p>

    <h2>{$i18n('accountDeletion.retentionHeading')}</h2>

    <p>{$i18n('accountDeletion.retention')}</p>

    <h2>{$i18n('accountDeletion.contactHeading')}</h2>

    <p>
      {#each $i18n.rich('accountDeletion.contact', { email, issues }) as seg}{#if seg.param === 'email'}<a href="mailto:collin.j.aycock@gmail.com">{seg.text}</a>{:else if seg.param === 'issues'}<a href="https://github.com/CollinJAycock/OnScreen/issues" rel="noopener">{seg.text}</a>{:else}{seg.text}{/if}{/each}
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
