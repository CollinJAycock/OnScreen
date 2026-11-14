# on-screen.tv

The project's one-page website, served from S3 through CloudFront (AWS profile
`loadHub`): bucket `on-screen-site-353750335352` (us-east-2, private, read by
CloudFront through origin access control `on-screen-site-oac`), with the
`on-screen-www-redirect` CloudFront function sending `www.on-screen.tv` to
`on-screen.tv`.

Plain HTML and CSS with no build step, no third-party fonts or scripts, and no
cookies. Screenshots come from `docs/store-assets` and show only openly
licensed media (credited in the footer).

Preview locally:

    python -m http.server 5190 --directory site

`kodi/` is the add-on repository Kodi installs and updates the OnScreen add-on
from. It's generated: rebuild it after a Kodi add-on version bump with
`python clients/kodi/scripts/build_repo.py --out site/kodi` (see
clients/kodi/README.md), and don't edit it by hand. `/kodi/` is served as
`/kodi/index.html` by the `on-screen-www-redirect` CloudFront function, which
maps any path ending in `/` to its `index.html`.

Deploy:

    aws s3 sync site/ s3://on-screen-site-353750335352/ --delete --exclude README.md --profile loadHub
    # Git Bash: prefix MSYS_NO_PATHCONV=1, or it rewrites "/*" into a Windows path
    aws cloudfront create-invalidation --distribution-id E2MIVU3W0V9WL5 --paths "/*" --profile loadHub

The `on-screen-www-redirect` CloudFront function (viewer request), as
published 2026-11-14. Change it in the CloudFront console or with
`aws cloudfront update-function` + `publish-function` (profile `loadHub`),
and keep this copy in step:

```js
function handler(event) {
  var request = event.request;
  var host = request.headers.host && request.headers.host.value;
  if (host === 'www.on-screen.tv') {
    var qs = Object.keys(request.querystring).map(function (k) {
      var v = request.querystring[k];
      return v.multiValue ? v.multiValue.map(function (m) { return k + '=' + m.value; }).join('&') : k + '=' + v.value;
    }).join('&');
    return {
      statusCode: 301,
      statusDescription: 'Moved Permanently',
      headers: { location: { value: 'https://on-screen.tv' + request.uri + (qs ? '?' + qs : '') } }
    };
  }
  // The Kodi add-on repository is typed into Kodi as a source, with or
  // without the slash.
  if (request.uri === '/kodi') {
    return { statusCode: 301, statusDescription: 'Moved Permanently', headers: { location: { value: '/kodi/' } } };
  }
  // A folder path serves its index.html (/kodi/ is the repository's
  // listing page).
  if (request.uri.length > 1 && request.uri.charAt(request.uri.length - 1) === '/') {
    request.uri += 'index.html';
  }
  return request;
}
```
