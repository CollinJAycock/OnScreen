<script lang="ts">
  // Account page for every signed-in user. Admins manage the same list (and
  // 2FA) under Settings ▸ Security; this is where everyone else sees which
  // devices are signed in and signs one out.
  import { userApi } from '$lib/api';
  import SignedInDevices from '$lib/components/SignedInDevices.svelte';
  import MyDownloads from '$lib/components/MyDownloads.svelte';
  import ApiKeys from '$lib/components/ApiKeys.svelte';
  import DigestOptIn from '$lib/components/DigestOptIn.svelte';
</script>

<svelte:head><title>Account — OnScreen</title></svelte:head>

<div class="account">
  <h1>Account</h1>

  <section class="card">
    <h2>My stats</h2>
    <p class="sub">
      Your own viewing and listening numbers: hours, top titles, genres, when
      you play, and your year in review. Only you can see them.
    </p>
    <a class="stats-link" href="/stats">Open my stats →</a>
  </section>

  <section class="card">
    <h2>Email me a digest</h2>
    <p class="sub">
      A weekly or monthly email of what was added to your libraries, with what
      you're in the middle of and your numbers for the period.
    </p>
    <DigestOptIn />
  </section>

  <section class="card">
    <h2>Signed-in devices</h2>
    <p class="sub">
      Browsers and apps signed in to your account. Signing one out ends its
      session at once; your other devices stay signed in.
    </p>
    <SignedInDevices
      load={() => userApi.listMySessions()}
      revoke={(id) => userApi.revokeMySession(id)}
      revokeAll={() => userApi.revokeMyOtherSessions()}
      revokeAllLabel="Sign out everywhere else"
      revokeAllConfirm="Sign out every other device signed in to your account? This browser stays signed in."
      keepCurrent
    />
  </section>

  <section class="card">
    <h2>Downloads</h2>
    <p class="sub">
      Smaller copies you asked for from a title's Download menu. Each is kept
      for a while after you last downloaded it, then removed to save space.
    </p>
    <MyDownloads />
  </section>

  <section class="card">
    <h2>API keys</h2>
    <p class="sub">
      Long-lived keys for scripts and integrations such as Home Assistant or
      Kometa. A key can do what you can, except sign in or change your account's
      security. Revoking one stops it at once.
    </p>
    <ApiKeys />
  </section>
</div>

<style>
  .account { max-width: 640px; }
  h1 { font-size: 1.25rem; font-weight: 700; color: var(--text-primary); margin: 0 0 1.25rem; }
  .card + .card { margin-top: 1rem; }
  .card {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.1rem 1.25rem;
  }
  h2 { font-size: 1rem; font-weight: 600; color: var(--text-primary); margin: 0 0 0.3rem; }
  .sub { color: var(--text-muted); font-size: 0.82rem; margin: 0 0 1rem; line-height: 1.45; max-width: 46ch; }
  .stats-link { color: var(--accent-text); font-size: 0.85rem; font-weight: 600; text-decoration: none; }
  .stats-link:hover { text-decoration: underline; }
</style>
