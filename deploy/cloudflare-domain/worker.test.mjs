import assert from 'node:assert/strict'
import { test } from 'node:test'
import worker from './worker.mjs'

test('host, protocol, port, URL credentials and fragment cannot change the public origin', async t => {
  let actual
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    actual = { url: String(url), options }
    return new Response('static bytes')
  })
  const response = await worker.fetch({
    method: 'GET',
    url: 'http://user:password@unrelated.invalid:8080//asset.svg?x=a%20b&x=2#fragment',
    headers: new Headers({ Authorization: 'Bearer synthetic', Cookie: 'synthetic=1' }),
  })
  assert.equal(actual.url, 'https://getarm.org//asset.svg?x=a%20b&x=2')
  assert.deepEqual(actual.options, { method: 'GET', redirect: 'manual' })
  assert.equal(await response.text(), 'static bytes')
})

for (const method of ['GET', 'HEAD']) {
  test(`${method} forwards path/query with no browser headers or body`, async t => {
    const calls = []
    const upstream = new Response(method === 'GET' ? 'retained bytes' : null, {
      status: 200,
      headers: { 'Content-Type': 'application/octet-stream', ETag: '"retained"' },
    })
    t.mock.method(globalThis, 'fetch', async (...args) => { calls.push(args); return upstream })
    const response = await worker.fetch(new Request('https://getarm.cloud/screenshots/terminal.png?version=1&name=a%20b', {
      method,
      headers: { Authorization: 'Bearer synthetic', Cookie: 'synthetic=1', 'X-Synthetic': 'private' },
    }))
    assert.equal(response, upstream)
    assert.equal(calls.length, 1)
    assert.equal(String(calls[0][0]), 'https://getarm.org/screenshots/terminal.png?version=1&name=a%20b')
    assert.deepEqual(calls[0][1], { method, redirect: 'manual' })
  })
}

test('mutating and preflight methods never contact the origin', async t => {
  t.mock.method(globalThis, 'fetch', () => assert.fail('unexpected origin request'))
  for (const method of ['POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS']) {
    const response = await worker.fetch(new Request('https://getarm.cloud/', { method }))
    assert.equal(response.status, 405)
    assert.equal(response.headers.get('Allow'), 'GET, HEAD')
  }
})

test('upstream redirects and errors are not silently followed or rewritten', async t => {
  const responses = [
    new Response(null, { status: 301, headers: { Location: 'https://getarm.org/path/' } }),
    new Response('missing static path', { status: 404 }),
  ]
  t.mock.method(globalThis, 'fetch', async (_url, options) => {
    assert.equal(options.redirect, 'manual')
    return responses.shift()
  })
  const redirect = await worker.fetch(new Request('https://getarm.cloud/path'))
  assert.equal(redirect.status, 301)
  assert.equal(redirect.headers.get('Location'), 'https://getarm.org/path/')
  const missing = await worker.fetch(new Request('https://getarm.cloud/missing'))
  assert.equal(missing.status, 404)
  assert.equal(await missing.text(), 'missing static path')
})
