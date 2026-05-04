<svelte:head>
  <title>OnScreen — Privacy Policy</title>
  <meta name="description" content="Privacy policy for the OnScreen media server and clients." />
</svelte:head>

<div class="page">
  <header class="header">
    <a href="/" class="brand">
      <img src="/favicon-96x96.png" alt="" width="36" height="36" />
      <span>OnScreen</span>
    </a>
  </header>

  <main class="content">
    <h1>Privacy Policy</h1>
    <p class="updated">Last updated: 2026-05-04</p>

    <p>
      OnScreen is an open-source, self-hosted media server. This policy
      covers the OnScreen Android TV app (<code>tv.onscreen.android</code>),
      the OnScreen web client, and any first-party desktop or mobile
      clients we publish. It does not cover third-party clients that may
      connect to your OnScreen server.
    </p>

    <p>
      <strong>Short version:</strong> the apps do not collect, store, or
      share any data with us, the developers. Everything you do in an
      OnScreen client talks only to the OnScreen server <em>you</em>
      configure. We operate no servers that receive data from these apps.
      There are no analytics, no advertising, and no third-party
      trackers.
    </p>

    <h2>Data the apps handle</h2>

    <p>Three kinds of data are involved when you use an OnScreen client:</p>

    <ul>
      <li>
        <strong>Server connection settings.</strong> The URL of your
        OnScreen server, your account credentials when you sign in, and
        the authentication tokens the server returns. These are stored
        locally on your device in the application sandbox (AndroidX
        DataStore on Android, browser storage on the web). They are not
        readable by other apps and are removed when you uninstall.
      </li>
      <li>
        <strong>Watch progress, library queries, and playback events.</strong>
        Sent to the OnScreen server you configured, never to any third
        party. The apps do not call TMDB, OpenSubtitles, or any other
        external service directly. Metadata enrichment and subtitle
        searches you trigger from the client are forwarded to your
        server, which makes those calls on your behalf using credentials
        you provided in the server's settings.
      </li>
      <li>
        <strong>Image and trickplay cache.</strong> Posters, fanart, and
        seek-bar thumbnails downloaded from your server are cached on
        disk for performance. They live in the app's private cache
        directory and are never shared with any other app or service.
      </li>
    </ul>

    <h2>Data the developer collects</h2>

    <p>
      <strong>None.</strong> Collin Aycock, the developer of the OnScreen
      project, operates no servers that receive telemetry, analytics,
      crash reports, or any other data from these apps. The clients make
      no network calls except to:
    </p>

    <ul>
      <li>
        the OnScreen server URL you configure (over HTTPS by default;
        HTTP only if you explicitly opt in for a LAN deployment), and
      </li>
      <li>
        on Android TV, the operating system's <code>WatchNextPrograms</code>
        content provider, which integrates with your TV launcher's
        "Continue Watching" row. This data stays on your device.
      </li>
    </ul>

    <p>
      We do not see, collect, or have access to anything you watch, your
      account credentials, your server's address, or your usage patterns.
    </p>

    <h2>Permissions the Android TV app requests</h2>

    <ul>
      <li>
        <strong>Internet</strong> — to communicate with your OnScreen
        server.
      </li>
      <li>
        <strong>Wake lock</strong> — to keep the CPU awake during video
        playback so the stream does not stutter.
      </li>
      <li>
        <strong>Read/Write EPG data</strong> — to publish "Continue
        Watching" tiles to your TV launcher's system row, so you can
        resume an episode from the launcher home screen.
      </li>
    </ul>

    <p>
      The app does not request access to your contacts, location,
      microphone, camera, calendar, SMS, call logs, or any other device
      data.
    </p>

    <h2>Third-party services</h2>

    <p>
      The clients do not contain advertising SDKs, analytics SDKs, crash
      reporting services, or any third-party libraries that send data
      outside your device. The full list of dependencies is visible in
      the project's source code.
    </p>

    <h2>Children's privacy</h2>

    <p>
      OnScreen is not directed at children under 13. The clients have no
      features specifically targeted at children and do not knowingly
      collect data from children.
    </p>

    <h2>Open source</h2>

    <p>
      The project's source code is published at
      <a href="https://github.com/CollinJAycock/OnScreen" rel="noopener">
        github.com/CollinJAycock/OnScreen
      </a>
      under the AGPLv3 license. You can audit exactly what data the
      clients handle, and what the server does with the data they send,
      by reading the source.
    </p>

    <h2>Changes to this policy</h2>

    <p>
      If we change this policy we will update the "Last updated" date
      above and note the change in the project's CHANGELOG. Material
      changes will be summarised in the release notes.
    </p>

    <h2>Contact</h2>

    <p>
      Questions about this policy:
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
