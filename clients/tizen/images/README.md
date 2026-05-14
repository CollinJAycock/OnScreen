# Channel Art

Tizen rejects `.wgt` install on the TV if `config.xml` references
an image that doesn't exist in the bundle. The scaffold ships
without binary art so the repo stays text-only — drop a real PNG
at the path below before sideloading.

| File | Dimensions | Format | config.xml key |
|---|---|---|---|
| `../icon.png` | 512 × 423 | PNG | `<icon src="icon.png" />` |

Samsung Apps Store launcher tiles are wider than square — main
image content must fit inside the inner 423 × 423 region centered
on the 512-wide canvas (Samsung Self Checklist items #168–169).
Earlier drafts of this README documented 512 × 512, which is wrong
for store submission though it satisfies the bundler.

(The icon lives at the project root, not in this `images/` dir —
`scripts/assemble-package.mjs` copies it into `build/` alongside
`config.xml` so it lands at the widget root the Tizen runtime
reads from. This README is the documentation; the icon goes one
level up.)

For Samsung Apps store submission, additional artwork is required
through the Seller Portal (banners, screenshots, app preview
images) — those are uploaded separately, not bundled in the
`.wgt`.
