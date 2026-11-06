<script lang="ts">
  import { onMount } from 'svelte';
  import { settingsApi } from '$lib/api';
  import type { OIDCSettings, LDAPSettings, SAMLSettings } from '$lib/api';
  import { toast } from '$lib/stores/toast';
  import { t, tr } from '$lib/i18n/index.svelte';

  let loading = true;
  let savingOIDC = false;
  let savingLDAP = false;
  let savingSAML = false;
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

  let saml: SAMLSettings = {
    enabled: false, display_name: '', idp_metadata_url: '', entity_id: '',
    sp_certificate_pem: '', sp_private_key_pem: '',
    email_attribute: '', username_attribute: '', groups_attribute: '', admin_group: ''
  };
  let samlPrivateKeyMasked = false;

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
      if (s.saml) {
        saml = { ...s.saml };
        samlPrivateKeyMasked = saml.sp_private_key_pem === '****';
      }
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : t('settings.sso.loadFailed');
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
      toast.success(t('settings.sso.oidcSaved'));
      // Refresh to get the new mask state.
      const s = await settingsApi.get();
      oidc = { ...s.oidc };
      oidcSecretMasked = oidc.client_secret === '****';
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.sso.saveFailed'));
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
      toast.success(t('settings.sso.ldapSaved'));
      const s = await settingsApi.get();
      ldap = { ...s.ldap };
      ldapPasswordMasked = ldap.bind_password === '****';
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.sso.saveFailed'));
    } finally {
      savingLDAP = false;
    }
  }

  async function saveSAML() {
    savingSAML = true;
    try {
      const payload: Record<string, unknown> = {
        enabled: saml.enabled,
        display_name: saml.display_name,
        idp_metadata_url: saml.idp_metadata_url,
        entity_id: saml.entity_id,
        sp_certificate_pem: saml.sp_certificate_pem,
        email_attribute: saml.email_attribute,
        username_attribute: saml.username_attribute,
        groups_attribute: saml.groups_attribute,
        admin_group: saml.admin_group,
      };
      // Only send the SP private key when the admin actually edited it —
      // mirrors the OIDC client-secret pattern. Sending "****" would be
      // dropped server-side anyway, but skipping keeps the payload clean.
      if (!samlPrivateKeyMasked || saml.sp_private_key_pem !== '****') {
        payload.sp_private_key_pem = saml.sp_private_key_pem;
      }
      await settingsApi.update({ saml: payload } as never);
      toast.success(t('settings.sso.samlSaved'));
      const s = await settingsApi.get();
      saml = { ...s.saml };
      samlPrivateKeyMasked = saml.sp_private_key_pem === '****';
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('settings.sso.saveFailed'));
    } finally {
      savingSAML = false;
    }
  }
</script>

{#if loading}
  <div class="skeleton-block"></div>
{:else if error}
  <p class="error">{error}</p>
{:else}
  <div class="wrap">
    <section>
      <header>
        <h2>OpenID Connect</h2>
        <p class="hint">
          {#each $tr.rich('settings.sso.oidcHint', { uri: `${location.origin}/api/v1/auth/oidc/callback` }) as seg}{#if seg.param}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
        </p>
      </header>

      <label class="check">
        <input type="checkbox" bind:checked={oidc.enabled} />
        <span>{$tr('settings.sso.oidcEnable')}</span>
      </label>

      <div class="grid">
        <label>
          {$tr('settings.sso.displayName')}
          <input type="text" bind:value={oidc.display_name} placeholder="Authentik" />
        </label>
        <label>
          {$tr('settings.sso.issuerUrl')}
          <input type="url" bind:value={oidc.issuer_url} placeholder="https://idp.example.com/application/o/onscreen/" />
        </label>
        <label>
          {$tr('settings.sso.clientId')}
          <input type="text" bind:value={oidc.client_id} />
        </label>
        <label>
          {$tr('settings.sso.clientSecret')}
          <input
            type="password"
            bind:value={oidc.client_secret}
            on:input={() => { oidcSecretMasked = false; }}
            placeholder={oidcSecretMasked ? $tr('settings.sso.unchanged') : ''}
          />
        </label>
        <label>
          {$tr('settings.sso.scopes')}
          <input type="text" bind:value={oidc.scopes} placeholder="openid profile email" />
        </label>
        <label>
          {$tr('settings.sso.usernameClaim')}
          <input type="text" bind:value={oidc.username_claim} placeholder="preferred_username" />
        </label>
        <label>
          {$tr('settings.sso.groupsClaim')}
          <input type="text" bind:value={oidc.groups_claim} placeholder="groups" />
        </label>
        <label>
          {$tr('settings.sso.adminGroup')}
          <input type="text" bind:value={oidc.admin_group} placeholder="onscreen-admins" />
        </label>
      </div>

      <button class="btn btn-primary" disabled={savingOIDC} on:click={saveOIDC}>
        {savingOIDC ? $tr('settings.sso.saving') : $tr('settings.sso.oidcSave')}
      </button>
    </section>

    <section>
      <header>
        <h2>{$tr('settings.sso.ldapTitle')}</h2>
        <p class="hint">
          {#each $tr.rich('settings.sso.ldapHint', { placeholder: '{username}' }) as seg}{#if seg.param}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
        </p>
      </header>

      <label class="check">
        <input type="checkbox" bind:checked={ldap.enabled} />
        <span>{$tr('settings.sso.ldapEnable')}</span>
      </label>

      <div class="grid">
        <label>
          {$tr('settings.sso.displayName')}
          <input type="text" bind:value={ldap.display_name} placeholder={$tr('settings.sso.companySso')} />
        </label>
        <label>
          {$tr('settings.sso.host')}
          <input type="text" bind:value={ldap.host} placeholder="ldap.example.com:636" />
        </label>
        <label>
          {$tr('settings.sso.bindDn')}
          <input type="text" bind:value={ldap.bind_dn} placeholder="cn=svc-onscreen,ou=services,dc=example,dc=com" />
        </label>
        <label>
          {$tr('settings.sso.bindPassword')}
          <input
            type="password"
            bind:value={ldap.bind_password}
            on:input={() => { ldapPasswordMasked = false; }}
            placeholder={ldapPasswordMasked ? $tr('settings.sso.unchanged') : ''}
          />
        </label>
        <label>
          {$tr('settings.sso.userSearchBase')}
          <input type="text" bind:value={ldap.user_search_base} placeholder="ou=people,dc=example,dc=com" />
        </label>
        <label>
          {$tr('settings.sso.userFilter')}
          <input type="text" bind:value={ldap.user_filter} placeholder="(uid={'{username}'})" />
        </label>
        <label>
          {$tr('settings.sso.usernameAttr')}
          <input type="text" bind:value={ldap.username_attr} placeholder="uid" />
        </label>
        <label>
          {$tr('settings.sso.emailAttr')}
          <input type="text" bind:value={ldap.email_attr} placeholder="mail" />
        </label>
        <label class="full">
          {$tr('settings.sso.adminGroupDn')}
          <input type="text" bind:value={ldap.admin_group_dn} placeholder="cn=onscreen-admins,ou=groups,dc=example,dc=com" />
        </label>
      </div>

      <div class="checks">
        <label class="check">
          <input type="checkbox" bind:checked={ldap.use_ldaps} />
          <span>{$tr('settings.sso.useLdaps')}</span>
        </label>
        <label class="check">
          <input type="checkbox" bind:checked={ldap.start_tls} disabled={ldap.use_ldaps} />
          <span>{$tr('settings.sso.startTls')}</span>
        </label>
        <label class="check">
          <input type="checkbox" bind:checked={ldap.skip_tls_verify} />
          <span>{$tr('settings.sso.skipTls')}</span>
        </label>
        {#if ldap.skip_tls_verify && (ldap.use_ldaps || ldap.start_tls)}
          <div class="warn">
            {#each $tr.rich('settings.sso.skipTlsWarning', { warning: $tr('settings.sso.warning') }) as seg}{#if seg.param}<strong>{seg.text}</strong>{:else}{seg.text}{/if}{/each}
          </div>
        {/if}
      </div>

      <button class="btn btn-primary" disabled={savingLDAP} on:click={saveLDAP}>
        {savingLDAP ? $tr('settings.sso.saving') : $tr('settings.sso.ldapSave')}
      </button>
    </section>

    <section>
      <header>
        <h2>SAML 2.0</h2>
        <p class="hint">
          {#each $tr.rich('settings.sso.samlHint', { url: `${location.origin}/api/v1/auth/saml/metadata` }) as seg}{#if seg.param}<code>{seg.text}</code>{:else}{seg.text}{/if}{/each}
        </p>
      </header>

      <label class="check">
        <input type="checkbox" bind:checked={saml.enabled} />
        <span>{$tr('settings.sso.samlEnable')}</span>
      </label>

      <div class="grid">
        <label>
          {$tr('settings.sso.displayName')}
          <input type="text" bind:value={saml.display_name} placeholder={$tr('settings.sso.companySso')} />
        </label>
        <label>
          {$tr('settings.sso.idpMetadataUrl')}
          <input type="url" bind:value={saml.idp_metadata_url} placeholder="https://idp.example.com/realms/onscreen-test/protocol/saml/descriptor" />
        </label>
        <label class="full">
          {$tr('settings.sso.entityId', { url: `${location.origin}/api/v1/auth/saml/metadata` })}
          <input type="text" bind:value={saml.entity_id} placeholder={`${location.origin}/api/v1/auth/saml/metadata`} />
        </label>
        <label>
          {$tr('settings.sso.emailAttr')}
          <input type="text" bind:value={saml.email_attribute} placeholder={$tr('settings.sso.emailAttrPlaceholder', { claim: 'http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress' })} />
        </label>
        <label>
          {$tr('settings.sso.usernameAttr')}
          <input type="text" bind:value={saml.username_attribute} placeholder={$tr('settings.sso.usernameAttrPlaceholder')} />
        </label>
        <label>
          {$tr('settings.sso.groupsAttr')}
          <input type="text" bind:value={saml.groups_attribute} placeholder="groups" />
        </label>
        <label>
          {$tr('settings.sso.adminGroup')}
          <input type="text" bind:value={saml.admin_group} placeholder="onscreen-admins" />
        </label>
        <label class="full">
          {$tr('settings.sso.spCert')}
          <textarea
            rows="4"
            bind:value={saml.sp_certificate_pem}
            placeholder="-----BEGIN CERTIFICATE-----&#10;…&#10;-----END CERTIFICATE-----"
          ></textarea>
        </label>
        <label class="full">
          {$tr('settings.sso.spKey')}
          <textarea
            rows="4"
            bind:value={saml.sp_private_key_pem}
            on:input={() => { samlPrivateKeyMasked = false; }}
            placeholder={samlPrivateKeyMasked ? $tr('settings.sso.unchanged') : '-----BEGIN PRIVATE KEY-----\n…\n-----END PRIVATE KEY-----'}
          ></textarea>
        </label>
      </div>

      <button class="btn btn-primary" disabled={savingSAML} on:click={saveSAML}>
        {savingSAML ? $tr('settings.sso.saving') : $tr('settings.sso.samlSave')}
      </button>
    </section>
  </div>
{/if}

<style>
  .wrap { display: flex; flex-direction: column; gap: 1.5rem; }
  section {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.1rem 1.25rem;
  }
  h2 { font-size: 0.95rem; margin: 0 0 0.5rem; font-weight: 600; }
  .hint { color: var(--text-secondary); font-size: 0.82rem; line-height: 1.5; margin: 0 0 1rem; }
  .error { color: var(--error); }

  .skeleton-block {
    height: 120px;
    border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, var(--bg-hover) 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer {
    0% { background-position: 200% 0; }
    100% { background-position: -200% 0; }
  }

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
  input[type="text"], input[type="url"], input[type="password"], textarea {
    padding: 0.48rem 0.7rem;
    border-radius: 7px;
    border: 1px solid var(--border-strong);
    background: var(--input-bg);
    color: var(--text-primary);
    font-family: inherit;
    font-size: 0.85rem;
    width: 100%;
  }
  input::placeholder, textarea::placeholder { color: var(--text-muted); }
  input:focus, textarea:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }
  input[type="checkbox"] { accent-color: var(--accent); }

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
    background: rgba(251, 191, 36, 0.12);
    border: 1px solid rgba(251, 191, 36, 0.35);
    border-radius: 7px;
    font-size: 0.78rem;
    color: #fcd34d;
    line-height: 1.45;
  }
  .warn strong { color: #fcd34d; }

  .btn {
    padding: 0.45rem 0.9rem;
    border-radius: 7px;
    font-size: 0.8rem;
    font-weight: 600;
    border: none;
    cursor: pointer;
    transition: background 0.15s, border-color 0.15s, color 0.15s;
  }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-primary { background: var(--accent); color: #fff; }
  .btn-primary:hover:not(:disabled) { background: var(--accent-hover); }

  code {
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, monospace;
    font-size: 0.85em;
    background: var(--bg-hover);
    padding: 0.05rem 0.35rem;
    border-radius: 4px;
  }

  @media (max-width: 720px) {
    .grid { grid-template-columns: 1fr; }
  }
</style>
