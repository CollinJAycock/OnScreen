# Unraid template

`onscreen.xml` is an Unraid Docker template for the OnScreen server
(`ghcr.io/collinjaycock/onscreen:2`).

## Before using it

OnScreen needs two other containers, both available in Community Applications:

1. **PostgreSQL 16+**: the stock `postgres:16` image is enough. OnScreen only
   uses the `pg_trgm`, `pgcrypto` and `unaccent` extensions, which ship with it.
   Create a database and user named `onscreen`.
2. **Valkey or Redis 7+**.

Then fill in `DATABASE_URL`, `VALKEY_URL` and a `SECRET_KEY`
(`openssl rand -hex 32`).

The template runs the container as `--user 99:100` (Unraid's `nobody:users`),
so it can write the appdata cache and backup folders on Unraid's shares.

## Installing it by hand

Docker tab › Add Container › Template: paste the raw URL
`https://raw.githubusercontent.com/CollinJAycock/OnScreen/main/docker/unraid/onscreen.xml`
into Template repositories (Docker › Template repositories), or copy the file
to `/boot/config/plugins/dockerMan/templates-user/my-OnScreen.xml`.

## Getting it into Community Applications

CA lists templates from a GitHub repository registered with it, plus a support
thread on the Unraid forums:

1. Test the template on a real Unraid server: install, sign in, add a library,
   play something. **It has not been run on Unraid yet.**
2. Open a support thread in the Unraid forums (Docker Containers board) for
   OnScreen, and put its URL in `<Support>` if you'd rather use it than
   GitHub Discussions.
3. Submit the repository through CA's template submission process (see
   the Community Applications "Application Policies" and submission post on
   the Unraid forums).
