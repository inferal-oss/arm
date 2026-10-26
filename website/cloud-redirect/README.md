# Alternate domain redirect

The canonical ARM website is deployed by GitHub Pages from this repository's
main branch. This separate, static Cloudflare Pages deployment serves only the
getarm.cloud redirect, using a DNS-only CNAME and no zone proxy/Redirect Rule.
Its source remains beside the website in this repository.

Upload only index.html and _redirects. The single rule redirects the path to
https://getarm.org with HTTP 301. Verify root and nested paths, original query
strings, HTTPS and the active custom domain on the actual deployment before
recording acceptance. README.md is documentation, not part of the upload.

No account credentials, runtime state or Git history belong in the deployment.
The project URL, exact source and archive hash are recorded after deployment.
