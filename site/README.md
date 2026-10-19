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

Deploy:

    aws s3 sync site/ s3://on-screen-site-353750335352/ --delete --exclude README.md --profile loadHub
    # Git Bash: prefix MSYS_NO_PATHCONV=1, or it rewrites "/*" into a Windows path
    aws cloudfront create-invalidation --distribution-id E2MIVU3W0V9WL5 --paths "/*" --profile loadHub
