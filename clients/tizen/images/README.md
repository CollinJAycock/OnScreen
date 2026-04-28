# Channel Art

Tizen rejects `.wgt` install on the TV if `config.xml` references
an image that doesn't exist in the bundle. The scaffold ships
without binary art so the repo stays text-only — drop a real PNG
at the path below before sideloading.

| File | Dimensions | Format | config.xml key |
|---|---|---|---|
| `../icon.png` | 512 × 512 | PNG | `<icon src="icon.png" />` |

(The icon lives at the project root, not in this `images/` dir —
`scripts/assemble-package.mjs` copies it into `build/` alongside
`config.xml` so it lands at the widget root the Tizen runtime
reads from. This README is the documentation; the icon goes one
level up.)

For Samsung Apps store submission, additional artwork is required
through the Seller Portal (banners, screenshots, app preview
images) — those are uploaded separately, not bundled in the
`.wgt`.

For dev placeholder art, even a solid-coloured 512×512 PNG is
enough to satisfy the bundler. Real artwork matching the OnScreen
brand can land later — same workflow as `web/static/`.
