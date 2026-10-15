# Personal API keys

Scripts and integrations — Home Assistant, Kometa, a cron job that marks
things watched — can't run OnScreen's interactive sign-in (password, maybe a
second factor, then short-lived access tokens refreshed every hour). A
personal API key is a long-lived credential for them instead. Keys arrived in
v2.6 (migration 00050).

## Making a key

Signed in on the web app:

- **Everyone:** Account ▸ API keys ▸ **Create API key**.
- **Admins:** Settings ▸ Security ▸ API keys (the same list), plus **All API
  keys** to see and revoke anyone's.

Give the key a name you'll recognise later, pick its access and, if you like,
an expiry (30 days, 90 days, a year, or never). The key is shown **once**,
with a Copy button. OnScreen stores only a SHA-256 hash of it, so if you lose
it, revoke it and make another.

Keys look like `osk_` followed by 43 letters and digits, e.g.
`osk_3fQ9…`. The list shows the first few characters (`osk_3fQ9xA2b…`) so
you can tell keys apart.

Or through the API, from a signed-in session:

```sh
curl -X POST https://onscreen.example.com/api/v1/users/me/api-keys \
  -H "Authorization: Bearer $ACCESS_TOKEN" -H 'Content-Type: application/json' \
  -d '{"name": "Home Assistant", "scope": "read_only"}'
```

The response's `data.key` is the key. `GET /api/v1/users/me/api-keys` lists
your keys and `DELETE /api/v1/users/me/api-keys/{id}` revokes one; admins have
`GET /api/v1/admin/api-keys` and `DELETE /api/v1/admin/api-keys/{id}`.

## Using a key

Send it in either header:

```sh
curl -H "Authorization: Bearer osk_…" https://onscreen.example.com/api/v1/hub
curl -H "X-Api-Key: osk_…"            https://onscreen.example.com/api/v1/libraries
```

Keys work on the same routes an access token does, including the media,
artwork and subtitle file routes. They are **never** accepted in a query
string (`?token=`, `?api_key=`): a URL ends up in access logs, browser
history and `Referer` headers, which is no place for a long-lived secret.

The OPDS book catalog (`/opds/`) also takes a key as the **password of HTTP
Basic** sign-in, since that is all e-reader apps can send; the username is
ignored. Nowhere else accepts Basic. See [docs/opds.md](../opds.md).

## What a key can do

A key acts as the user who made it, with **that user's current** library
access, content-rating ceiling and label restrictions. These are read on every
request, not copied into the key, so when an admin changes a user's
restrictions, their keys follow at once.

Two scopes:

| Scope       | Allows                                             |
|-------------|----------------------------------------------------|
| `full`      | everything the user can do, except the list below  |
| `read_only` | GET (and HEAD) only; anything else is 403 `API_KEY_READ_ONLY` |

So that a leaked key can't be turned into a takeover of the account, keys are
refused (403 `API_KEY_NOT_ALLOWED`) on:

- signing in, refreshing, registering, password reset and invite acceptance;
- two-factor setup, activation and removal;
- the account's PIN, deleting the account, and managed profiles (reading the
  list of profiles is fine);
- signed-in device management and API key management;
- claiming a TV's pairing code, and switching profiles by PIN;
- user administration and invites;
- downloading an account's data (`/users/me/export`, `/users/{id}/export`);
- server settings and backups (even for an admin's key).

An admin's key can otherwise use the admin API — refreshing libraries,
editing metadata, managing collections — which is what tools like Kometa need.
Give such a key only to software you trust with that.

## Revoking

Revoke a key from the list (or `DELETE` it through the API). It stops working
on its very next request, on every server instance: keys are looked up in the
database on each request, not cached. A stream URL handed to a request made
with the key stops working too.

Deleting a user deletes their keys. An expired key is refused but stays in the
list (marked expired) until someone deletes it.

"Last used" is approximate: it's written at most every five minutes per key.

Creating and revoking keys is recorded in the audit log (`api_key.create`,
`api_key.delete`), with the key's name and prefix but never the key.

## Examples

**Home Assistant** (a `rest` sensor for what's playing):

```yaml
sensor:
  - platform: rest
    name: OnScreen now playing
    resource: https://onscreen.example.com/api/v1/sessions
    headers:
      X-Api-Key: !secret onscreen_api_key
    value_template: "{{ value_json.data | length }}"
```

A `read_only` key is enough for sensors.

**A script** that marks an item watched needs a `full` key:

```sh
curl -X POST -H "X-Api-Key: $ONSCREEN_KEY" \
  https://onscreen.example.com/api/v1/items/$ITEM_ID/watched
```
