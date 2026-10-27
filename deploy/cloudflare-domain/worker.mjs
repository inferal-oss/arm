// Serve the public GitHub Pages site at the secondary domain without forwarding
// browser credentials or introducing a second build of the website.
export default {
  async fetch(request) {
    if (request.method !== 'GET' && request.method !== 'HEAD') {
      return new Response('Method not allowed', {
        status: 405,
        headers: { Allow: 'GET, HEAD' },
      })
    }

    const target = new URL(request.url)
    target.protocol = 'https:'
    target.hostname = 'getarm.org'
    target.port = ''
    target.username = ''
    target.password = ''
    target.hash = ''

    // Copy the public path/query and method only. In particular, no incoming
    // Cookie, Authorization, other headers or request body reach the origin.
    return fetch(target, { method: request.method, redirect: 'manual' })
  },
}
