# Reading your books in an e-reader app (OPDS)

OnScreen publishes your book libraries (EPUB, PDF, CBZ, CBR) as an OPDS 1.2
catalog. E-reader apps that speak OPDS — KOReader, Moon+ Reader, Panels,
Chunky, Thorium, and many others — can browse it, search it and download
books from it. The catalog arrived in v2.6.

## Setting it up

1. **Make a key.** Signed in on the web app, go to **Account ▸ API keys ▸
   Create API key**. Name it after the device ("Kobo", "iPad Panels"), and
   choose **Read-only**: the catalog only ever reads.
   Copy the key (`osk_…`); it is shown once.
2. **Add the catalog in the app.** The address is shown on the Account page
   under the API keys, and is your server's address followed by `/opds/`:

   ```
   https://onscreen.example.com/opds/
   ```

3. **Sign in with the key.** Enter anything as the user name (your username
   is fine) and **paste the key as the password**.

Your account password does **not** work here, on purpose: the catalog is
used by apps signing in unattended, without a second factor, so it only
takes a key you can revoke on its own. Revoke the key (Account ▸ API keys)
and that app loses access on its next request.

### App notes

- **KOReader:** Search ▸ OPDS catalog ▸ `+`. Catalog URL as above, then the
  user name and the key as the password.
- **Moon+ Reader:** Net Library ▸ Add, then fill in the URL, user name and
  password.
- **Panels / Chunky:** add an OPDS source with the URL; enter the user name
  and key when asked.

## What you see

- **Recently added** — the newest books across your book libraries.
- One entry per book library you can open, listed by title, 50 a page.
- **Search** (the app's search box) — title, author and text.

Each book shows its title, author, year, genres, summary and cover, and a
download link for the file in its own format. An EPUB's title, author and
description come from the book itself (its OPF metadata); a book without
them is titled after its filename.

You see exactly what you see in the web app: only the libraries you have
access to, and only books within your content-rating limit and label
restrictions.

## If it doesn't work

- **401 / asks for a password again:** the key is wrong, revoked or expired,
  or you entered your account password. Make a new key.
- **403 "downloads are disabled":** the catalog hands out book files, so it
  follows the server's download setting. An admin can turn downloads on
  under **Settings ▸ General** (Allow web downloads).
- **429:** ten wrong keys in a row from your address lock it out for 15
  minutes.
- **No books:** the catalog lists libraries of type *Books* only.

## For developers

Routes (all GET, Atom XML):

| Path                             | What                                  |
|----------------------------------|---------------------------------------|
| `/opds/`                         | navigation feed                       |
| `/opds/recent?page=N`            | newest books                          |
| `/opds/libraries/{id}?page=N`    | one book library, by title            |
| `/opds/search?q=…&page=N`        | search                                |
| `/opds/opensearch.xml`           | OpenSearch description                |
| `/opds/files/{fileId}/cover`     | a book's cover                        |

Besides HTTP Basic, the routes take the API's usual credentials (Bearer
token, session cookie, `X-Api-Key`). Download links point at
`/media/download/{fileId}` and carry a short-lived token bound to that one
file, so they work in apps that don't send credentials with downloads.
