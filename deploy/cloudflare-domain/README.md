# Secondary website domain

`getarm.org` remains the canonical GitHub Pages site. This stateless Worker serves
its public content on `getarm.cloud`, using a Cloudflare Worker Custom Domain.
It preserves the request path and query rather than redirecting between domains.
The public site has no ARM backend or account session.

Only GET and HEAD are accepted. Origin requests use HTTPS with normal platform
certificate verification, a fixed `getarm.org` hostname, and no incoming headers
or body. URL credentials, port and fragment are cleared. Responses, including
status and public static bytes, pass through without an HTML rewrite. Origin
redirects are returned as received; the Worker does not follow them.

The configuration has one custom domain and no storage, secrets, service
bindings, preview URLs, additional build or GitHub permissions. Do not attach
`getarm.org` to this Worker: that would replace its GitHub Pages routing and make
the origin ambiguous.

The arrangement uses the Worker Custom Domain mechanism observed on the
Notabenda website, while keeping ARM's existing GitHub Pages publication.
Notabenda's application storage and administration are not part of this service.

Run the source boundary checks with:

```sh
node --test deploy/cloudflare-domain/worker.test.mjs
```

After deploying this exact module through the existing Cloudflare account,
attach only `getarm.cloud` as its Custom Domain. Preserve the previous secondary
DNS records before replacing them. Verify the native HTTPS site, a known static
asset, paths/queries, HEAD and the 405 method response. Canonical Pages settings
and DNS must remain unchanged. No runtime deployment is implied by this file.

[Cloudflare Custom Domains](https://developers.cloudflare.com/workers/configuration/routing/custom-domains/)
and [Wrangler configuration](https://developers.cloudflare.com/workers/wrangler/configuration/)
describe the custom-domain mapping and disabled preview endpoints.
