<svelte:head>
  <title>OnScreen — Account & Data Deletion</title>
  <meta name="description" content="How to request deletion of your OnScreen account and associated data." />
</svelte:head>

<div class="page">
  <header class="header">
    <a href="/" class="brand">
      <img src="/favicon-96x96.png" alt="" width="36" height="36" />
      <span>OnScreen</span>
    </a>
  </header>

  <main class="content">
    <h1>Account &amp; Data Deletion</h1>
    <p class="updated">Last updated: 2026-05-04</p>

    <p>
      OnScreen is a self-hosted media server. Where your account data
      lives — and how to delete it — depends on which OnScreen server
      you're using.
    </p>

    <h2>If you're using a server you run yourself</h2>

    <p>
      Your account and all associated data (watch history, favorites,
      profiles) are stored on the OnScreen server you or your server
      admin operates. The OnScreen project (the developers of the app
      and server software) does not have access to that data.
    </p>

    <p>
      Ask the server's admin user to delete your account via
      <strong>Settings → Users</strong> in the admin panel. The admin
      can remove your user record, watch history, favorites, profiles,
      sessions, and pairing tokens from their server.
    </p>

    <h2>If you're using the OnScreen public beta</h2>

    <p>
      The public beta runs at
      <a href="https://onscreen.wolverscreen.com" rel="noopener">onscreen.wolverscreen.com</a>.
      To delete an account on the beta, email
      <a href="mailto:collin.j.aycock@gmail.com?subject=OnScreen%20account%20deletion%20request">collin.j.aycock@gmail.com</a>
      with the username (and, if known, the email address) of the
      account to delete.
    </p>

    <p>
      We'll confirm receipt within 7 days and complete the deletion
      within 30 days. We may ask you to verify ownership of the
      account before deleting (for example, by responding from the
      email address registered with the account).
    </p>

    <h2>What gets deleted</h2>

    <p>When your account is deleted, the following are removed from the OnScreen server:</p>

    <ul>
      <li>Your user record (username, email if provided, password hash)</li>
      <li>Watch history and progress events</li>
      <li>Favorites and playlists</li>
      <li>Managed profiles attached to your account</li>
      <li>Active sessions, refresh tokens, and pairing tokens</li>
      <li>API tokens you've created</li>
      <li>Notification preferences</li>
    </ul>

    <p>The following are <em>not</em> deleted because they aren't tied to your account:</p>

    <ul>
      <li>
        Media files in the library (these belong to the server's
        owner, not to individual users)
      </li>
      <li>
        Aggregate analytics that have been stripped of user identity
        (e.g., total play counts per item) — your contribution to these
        cannot be reverse-attributed to you after deletion
      </li>
    </ul>

    <h2>Local data on your TV or device</h2>

    <p>
      The OnScreen TV app and other clients store the following on the
      device:
    </p>

    <ul>
      <li>The server URL you configured</li>
      <li>Authentication tokens (cleared when you sign out)</li>
      <li>An image cache (poster and thumbnail images downloaded for performance)</li>
    </ul>

    <p>
      To remove all local data from a device, uninstall the app
      (Settings → Apps → OnScreen → Uninstall on Android TV / Google TV /
      Fire TV). All local data is wiped as part of the uninstall.
    </p>

    <h2>Retention</h2>

    <p>
      Deleted accounts are removed from the live database immediately.
      Database backups, retained for up to 30 days for disaster
      recovery, are overwritten on rotation; deleted accounts are not
      restored from backup.
    </p>

    <h2>Contact</h2>

    <p>
      Questions:
      <a href="mailto:collin.j.aycock@gmail.com">collin.j.aycock@gmail.com</a>
      or open an issue at
      <a href="https://github.com/CollinJAycock/OnScreen/issues" rel="noopener">
        github.com/CollinJAycock/OnScreen/issues
      </a>.
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
