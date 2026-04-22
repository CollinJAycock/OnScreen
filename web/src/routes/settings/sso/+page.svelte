<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsApi } from '$lib/api';
  import type { OIDCSettings, LDAPSettings } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  let loading = true;
  let savingOIDC = false;
  let savingLDAP = false;
  let error = '';

  let oidc: OIDCSettings = {
    enabled: false, display_name: '', issuer_url: '', client_id: '',
    client_secret: '', scopes: '', username_claim: '', groups_claim: '',
    admin_group: ''
  };
  let oidcSecretMasked = false;

  let ldap: LDAPSettings = {
    enabled: false, display_name: '', host: '', start_tls: false, use_ldaps: false,
    skip_tls_verify: false, bind_dn: '', bind_password: '', user_search_base: '',
    user_filter: '', username_attr: '', email_attr: '', admin_group_dn: ''
  };
  let ldapPasswordMasked = false;

  onMount(async () => {
    try {
      const s = await settingsApi.get();
      if (s.oidc) {
        oidc = { ...s.oidc };
        oidcSecretMasked = oidc.client_secret === '****';
      }
      if (s.ldap) {
        ldap = { ...s.ldap };
        ldapPasswordMasked = ldap.bind_password === '****';
      }
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load settings';
    } finally {
      loading = false;
    }
  });

  async function saveOIDC() {
    savingOIDC = true;
    try {
      const payload: Record<string, unknown> = {
        enabled: oidc.enabled,
        display_name: oidc.display_name,
        issuer_url: oidc.issuer_url,
        client_id: oidc.client_id,
        scopes: oidc.scopes,
        username_claim: oidc.username_claim,
        groups_claim: oidc.groups_claim,
        admin_group: oidc.admin_group,
      };
      // Only send client_secret when the admin actually edited it.
      if (!oidcSecretMasked || oidc.client_secret !== '****') {
        payload.client_secret = oidc.client_secret;
      }
      await settingsApi.update({ oidc: payload } as never);
      toast.success('OIDC settings saved');
      // Refresh to get the new mask state.
      const s = await settingsApi.get();
      oidc = { ...s.oidc };
      oidcSecretMasked = oidc.client_secret === '****';
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Save failed');
    } finally {
      savingOIDC = false;
    }
  }

  async function saveLDAP() {
    savingLDAP = true;
    try {
      const payload: Record<string, unknown> = {
        enabled: ldap.enabled,
        display_name: ldap.display_name,
        host: ldap.host,
        start_tls: ldap.start_tls,
        use_ldaps: ldap.use_ldaps,
        skip_tls_verify: ldap.skip_tls_verify,
        bind_dn: ldap.bind_dn,
        user_search_base: ldap.user_search_base,
        user_filter: ldap.user_filter,
        username_attr: ldap.username_attr,
        email_attr: ldap.email_attr,
        admin_group_dn: ldap.admin_group_dn,
      };
      if (!ldapPasswordMasked || ldap.bind_password !== '****') {
        payload.bind_password = ldap.bind_password;
      }
      await settingsApi.update({ ldap: payload } as never);
      toast.success('LDAP settings saved');
      const s = await settingsApi.get();
      ldap = { ...s.ldap };
      ldapPasswordMasked = ldap.bind_password === '****';
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Save failed');
    } finally {
      savingLDAP = false;
    }
  }
</script>

{#if loading}
  <p class="muted">Loading…</p>
{:else if error}
  <p class="error">{error}</p>
{:else}
  <div class="wrap">
    <section>
      <header>
        <h2>OpenID Connect</h2>
        <p class="hint">
          Generic OIDC SSO (Authentik, Keycloak, Auth0, Azure AD, etc.).
          Set the redirect URI in your IdP to
          <code>{location.origin}/api/v1/auth/oidc/callback</code>.
        </p>
      </header>

      <label class="check">
        <input type="checkbox" bind:checked={oidc.enabled} />
        <span>Enable OIDC sign-in</span>
      </label>

      <div class="grid">
        <label>
          Display name
          <input type="text" bind:value={oidc.display_name} placeholder="Authentik" />
        </label>
        <label>
          Issuer URL
          <input type="url" bind:value={oidc.issuer_url} placeholder="https://idp.example.com/application/o/onscreen/" />
        </label>
        <label>
          Client ID
          <input type="text" bind:value={oidc.client_id} />
        </label>
        <label>
          Client secret
          <input
            type="password"
            bind:value={oidc.client_secret}
            on:input={() => { oidcSecretMasked = false; }}
            placeholder={oidcSecretMasked ? 'unchanged' : ''}
          />
        </label>
        <label>
          Scopes (space separated)
          <input type="text" bind:value={oidc.scopes} placeholder="openid profile email" />
        </label>
        <label>
          Username claim
          <input type="text" bind:value={oidc.username_claim} placeholder="preferred_username" />
        </label>
        <label>
          Groups claim
          <input type="text" bind:value={oidc.groups_claim} placeholder="groups" />
        </label>
        <label>
          Admin group
          <input type="text" bind:value={oidc.admin_group} placeholder="onscreen-admins" />
        </label>
      </div>

      <button class="btn btn-primary" disabled={savingOIDC} on:click={saveOIDC}>
        {savingOIDC ? 'Saving…' : 'Save OIDC settings'}
      </button>
    </section>

    <section>
      <header>
        <h2>LDAP / Active Directory</h2>
        <p class="hint">
          Bind-as-user authentication. The server first binds with the service account,
          searches for the user record using <code>{'{username}'}</code>, then re-binds
          as that user with the supplied password.
        </p>
      </header>

      <label class="check">
        <input type="checkbox" bind:checked={ldap.enabled} />
        <span>Enable LDAP sign-in</span>
      </label>

      <div class="grid">
        <label>
          Display name
          <input type="text" bind:value={ldap.display_name} placeholder="Company SSO" />
        </label>
        <label>
          Host (host:port)
          <input type="text" bind:value={ldap.host} placeholder="ldap.example.com:636" />
        </label>
        <label>
          Bind DN (service account)
          <input type="text" bind:value={ldap.bind_dn} placeholder="cn=svc-onscreen,ou=services,dc=example,dc=com" />
        </label>
        <label>
          Bind password
          <input
            type="password"
            bind:value={ldap.bind_password}
            on:input={() => { ldapPasswordMasked = false; }}
            placeholder={ldapPasswordMasked ? 'unchanged' : ''}
          />
        </label>
        <label>
          User search base
          <input type="text" bind:value={ldap.user_search_base} placeholder="ou=people,dc=example,dc=com" />
        </label>
        <label>
          User filter
          <input type="text" bind:value={ldap.user_filter} placeholder="(uid={'{username}'})" />
        </label>
        <label>
          Username attribute
          <input type="text" bind:value={ldap.username_attr} placeholder="uid" />
        </label>
        <label>
          Email attribute
          <input type="text" bind:value={ldap.email_attr} placeholder="mail" />
        </label>
        <label class="full">
          Admin group DN
          <input type="text" bind:value={ldap.admin_group_dn} placeholder="cn=onscreen-admins,ou=groups,dc=example,dc=com" />
        </label>
      </div>

      <div class="checks">
        <label class="check">
          <input type="checkbox" bind:checked={ldap.use_ldaps} />
          <span>Use LDAPS (implicit TLS)</span>
        </label>
        <label class="check">
          <input type="checkbox" bind:checked={ldap.start_tls} disabled={ldap.use_ldaps} />
          <span>StartTLS (upgrade plain LDAP to TLS)</span>
        </label>
        <label class="check">
          <input type="checkbox" bind:checked={ldap.skip_tls_verify} />
          <span>Skip TLS verification (dev / self-signed)</span>
        </label>
        {#if ldap.skip_tls_verify && (ldap.use_ldaps || ldap.start_tls)}
          <div class="warn">
            <strong>Warning:</strong> TLS certificate verification is disabled. Any
            attacker on the network path can impersonate your LDAP server and
            harvest user passwords as they bind. Use this only for local
            development with self-signed certificates — never in production.
          </div>
        {/if}
      </div>

      <button class="btn btn-primary" disabled={savingLDAP} on:click={saveLDAP}>
        {savingLDAP ? 'Saving…' : 'Save LDAP settings'}
      </button>
    </section>
  </div>
{/if}

<style>
  .wrap { display: flex; flex-direction: column; gap: 2rem; }
  section {
    background: var(--surface);
    border: 1px solid rgba(255,255,255,0.05);
    border-radius: 8px;
    padding: 1.25rem 1.5rem;
  }
  h2 { font-size: 0.95rem; margin: 0 0 0.5rem; font-weight: 600; }
  .hint { color: var(--text-secondary); font-size: 0.82rem; line-height: 1.5; margin: 0 0 1rem; }
  .muted { color: var(--text-muted); }
  .error { color: var(--error); }

  .grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0.75rem 1rem;
    margin: 1rem 0;
  }
  .grid .full { grid-column: 1 / -1; }
  label {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    font-size: 0.78rem;
    color: var(--text-secondary);
  }
  input[type="text"], input[type="url"], input[type="password"] {
    padding: 0.45rem 0.6rem;
    border-radius: 4px;
    border: 1px solid rgba(255,255,255,0.1);
    background: var(--bg);
    color: var(--text-primary);
    font-family: inherit;
    font-size: 0.85rem;
  }

  .checks {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    margin: 0.75rem 0 1rem;
  }
  .check {
    flex-direction: row;
    align-items: center;
    gap: 0.5rem;
    color: var(--text-secondary);
    font-size: 0.82rem;
    cursor: pointer;
  }
  .warn {
    margin-top: 0.5rem;
    padding: 0.6rem 0.8rem;
    background: rgba(255, 168, 76, 0.08);
    border: 1px solid rgba(255, 168, 76, 0.35);
    border-radius: 4px;
    font-size: 0.78rem;
    color: #ffc88a;
    line-height: 1.45;
  }
  .warn strong { color: #ffa84c; }

  .btn {
    padding: 0.55rem 1.1rem;
    border-radius: 4px;
    font-size: 0.82rem;
    font-weight: 500;
    border: 1px solid transparent;
    cursor: pointer;
    transition: background 0.12s;
  }
  .btn:disabled { opacity: 0.55; cursor: not-allowed; }
  .btn-primary { background: var(--accent); color: var(--accent-text); }
  .btn-primary:hover:not(:disabled) { filter: brightness(1.1); }

  code {
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, monospace;
    font-size: 0.85em;
    background: rgba(255,255,255,0.05);
    padding: 0.05rem 0.35rem;
    border-radius: 3px;
  }

  @media (max-width: 720px) {
    .grid { grid-template-columns: 1fr; }
  }
</style>
