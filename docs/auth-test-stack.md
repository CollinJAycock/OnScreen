# Auth-provider test stack (Windows / dev)

Walkthrough for the "Auth providers" rows of [manual-test-plan.md](manual-test-plan.md) when you don't have a real Keycloak / AD / Google IdP on hand. Everything runs in Docker Desktop on Windows; OnScreen itself runs via `make dev` (or its own docker-compose) alongside the stack.

## Spin up

```bash
docker compose -f docker/docker-compose.idp.yml up -d
docker compose -f docker/docker-compose.idp.yml logs -f keycloak    # wait for "started in"
```

| Service | URL | Default creds |
|---|---|---|
| Keycloak admin | http://localhost:8080 | `admin` / `admin` |
| lldap admin UI | http://localhost:17170 | `admin` / `testpass` |
| Mailpit web UI | http://localhost:8025 | (no auth) |
| Mailpit SMTP | `localhost:1025` | (no auth) |
| LDAP server | `ldap://localhost:3890` | bind DN `uid=admin,ou=people,dc=test,dc=onscreen,dc=local` / `testpass` |

Tear down (drops state):

```bash
docker compose -f docker/docker-compose.idp.yml down -v
```

## OIDC — Keycloak

**One-time Keycloak setup**

1. Sign in to http://localhost:8080 as `admin` / `admin`.
2. Top-left realm dropdown → **Create realm** → `onscreen-test` → Create.
3. **Clients** → **Create client**.
   - Client type: **OpenID Connect**
   - Client ID: `onscreen-test`
   - Next →
   - Client authentication: **On**
   - Authorization: Off
   - Standard flow: On (everything else default) → Next →
   - Valid redirect URIs: `http://localhost:7070/api/v1/auth/oidc/callback`
   - Save.
4. **Credentials** tab on the new client → copy the **Client secret**.
5. **Users** → **Create new user** → username `testuser`, email `test@onscreen.local`, email verified On → Create.
6. **Credentials** tab on the user → **Set password** → `testpass`, Temporary Off → Save.

**OnScreen settings** (Settings → SSO → OIDC):

```
Issuer URL:     http://localhost:8080/realms/onscreen-test
Client ID:      onscreen-test
Client secret:  (paste from step 4)
Scopes:         openid profile email
```

Save → **Enable OIDC**. Sign out, click "Sign in with SSO" on the login page, you're redirected to Keycloak, log in as `testuser` / `testpass`, redirected back, account auto-created.

## SAML — Keycloak (same realm)

1. **Clients** → **Create client** in the `onscreen-test` realm.
   - Client type: **SAML**
   - Client ID: `http://localhost:7070/api/v1/auth/saml/metadata` (this MUST match OnScreen's SP entityID)
   - Next →
   - Valid redirect URIs: `http://localhost:7070/api/v1/auth/saml/acs`
   - Save.
2. On the new client: **Settings** tab.
   - Force POST binding: On
   - Sign assertions: On
   - Save.
3. **Realm settings** → **Keys** → copy the active **RS256** certificate (Certificate column → Public key text).

**OnScreen settings** (Settings → SSO → SAML):

```
SP entity ID:     http://localhost:7070/api/v1/auth/saml/metadata
IdP metadata URL: http://localhost:8080/realms/onscreen-test/protocol/saml/descriptor
ACS URL:          http://localhost:7070/api/v1/auth/saml/acs
```

Save → **Enable SAML**. Sign out → "Sign in with SAML" → Keycloak login → back to OnScreen home.

## LDAP — lldap

**One-time lldap setup**

1. Sign in to http://localhost:17170 as `admin` / `testpass`.
2. **Users** → **Create user** → username `ldapuser`, email `ldap@onscreen.local`, password `ldappass` → Create.
3. (Optional) **Groups** → create `onscreen-admins` and add the user.

**OnScreen settings** (Settings → SSO → LDAP):

```
Host:                 localhost
Port:                 3890
Use TLS:              off  (lldap default is plain LDAP for dev)
Bind DN:              uid=admin,ou=people,dc=test,dc=onscreen,dc=local
Bind password:        testpass
Base DN:              ou=people,dc=test,dc=onscreen,dc=local
User filter:          (uid={username})
Username attribute:   uid
Email attribute:      mail
Display-name attr:    displayName
```

Save → **Enable LDAP**. From the login page, fill `ldapuser` / `ldappass` — server binds to lldap, account auto-created on first success.

**Coverage of the manual-plan rows:**

- ✅ valid creds → login (the "ldap login" row)
- ✅ invalid creds → "invalid credentials" (try wrong password)
- ✅ no enumeration on ambiguous filter (try a username that doesn't exist — same error as wrong password, not "user not found")

## SMTP — Mailpit

**OnScreen settings** (Settings → Email):

```
Host:           localhost
Port:           1025
Encryption:     none
Username:       (leave blank)
Password:       (leave blank)
From address:   noreply@onscreen.local
```

Save → **Send Test Email** → message appears in http://localhost:8025 within seconds. The forgot-password flow puts its reset email into the same inbox — verifies the BumpSessionEpoch path end-to-end.

## What this stack does **not** cover

The manual plan still needs human eyes for:

- **Real Google / GitHub / Discord OAuth** — can't realistically fake a third-party OAuth provider; create a real test app on each platform and use its dev/sandbox creds.
- **Production SSL/cert chains** — Keycloak and lldap here are HTTP/plain LDAP. The TLS+nginx row of the manual plan needs a real reverse-proxy + cert.
- **Cross-org IdP-initiated SAML quirks** — bespoke per-IdP behavior (PingFederate / Okta / ADFS) Keycloak doesn't replicate.
- **AD / FreeIPA edge cases** — lldap is intentionally schema-simplified; if you have customers on AD, test against real AD before claiming compatibility.

## Tying into automation

The Playwright suite at [web/tests/e2e/](../web/tests/e2e) doesn't drive Keycloak/lldap/Mailpit yet — auth flows in `smoke.spec.ts` use the local `admin` user. The next layer (v2.1 candidate): a `web/tests/e2e/auth-providers.spec.ts` that boots this compose stack as a Playwright `globalSetup`, runs the OIDC + LDAP rounds, and tears down. Mailpit's API at `http://localhost:8025/api/v1/messages` is automation-friendly — you can fetch the latest message and click the reset link headlessly.
