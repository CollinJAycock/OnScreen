<script lang="ts">
  // Account page for every signed-in user. Admins manage the same list (and
  // 2FA) under Settings ▸ Security; this is where everyone else sees which
  // devices are signed in and signs one out.
  import { userApi } from '$lib/api';
  import SignedInDevices from '$lib/components/SignedInDevices.svelte';
</script>

<svelte:head><title>Account — OnScreen</title></svelte:head>

<div class="account">
  <h1>Account</h1>

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
</div>

<style>
  .account { max-width: 640px; }
  h1 { font-size: 1.25rem; font-weight: 700; color: var(--text-primary); margin: 0 0 1.25rem; }
  .card {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.1rem 1.25rem;
  }
  h2 { font-size: 1rem; font-weight: 600; color: var(--text-primary); margin: 0 0 0.3rem; }
  .sub { color: var(--text-muted); font-size: 0.82rem; margin: 0 0 1rem; line-height: 1.45; max-width: 46ch; }
</style>
