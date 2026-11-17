import assert from 'node:assert/strict'
import { test } from 'node:test'

const deferred = () => {
  let resolve
  const promise = new Promise(done => { resolve = done })
  return { promise, resolve }
}

export function transportContract({ requestJSON, RequestScope, gatewayURL, ApiError }) {
  const url = new URL('https://api.shopstops.com/v1/orders')
  test('gateway policy rejects credentials, unsafe schemes and private release endpoints', () => {
    for (const value of ['ftp://localhost', 'http://api.shopstops.com', 'https://u:p@api.shopstops.com', 'https://api.shopstops.com?q=secret', 'https://api.shopstops.com#secret']) {
      assert.throws(() => gatewayURL(value))
    }
    for (const host of ['localhost', 'localhost.', '127.0.0.1', '127.1', '10.0.2.2', '192.168.1.1', '172.20.1.1', '[::1]', 'api.local', 'api.internal']) {
      assert.throws(() => gatewayURL(`https://${host}`, false))
    }
    assert.equal(gatewayURL('http://localhost:8081/v1', true).pathname, '/v1')
    assert.throws(() => gatewayURL('http://localhost:8081/v1', false))
    assert.equal(gatewayURL('https://api.shopstops.com/v1/', false).pathname, '/v1')
  })
  test('transport forces bearer-only JSON, no cookies, no cache and no redirects', async t => {
    const calls = []
    t.mock.method(globalThis, 'fetch', async (target, options) => {
      calls.push({ target, options }); return new Response('{"saved":true}')
    })
    const body = JSON.stringify({ amount: 100 })
    for (let i = 0; i < 2; i++) assert.deepEqual(await requestJSON(url, ' user-token ', {
      method: 'POST', credentials: 'include', redirect: 'follow', cache: 'force-cache',
      headers: { Authorization: 'unsafe', 'Idempotency-Key': 'same-retry-key' }, body,
    }), { saved: true })
    for (const { options } of calls) {
      assert.equal(options.credentials, 'omit'); assert.equal(options.redirect, 'error'); assert.equal(options.cache, 'no-store')
      assert.equal(options.headers.get('Authorization'), 'Bearer user-token')
      assert.equal(options.headers.get('Idempotency-Key'), 'same-retry-key')
      assert.equal(options.headers.get('Content-Type'), 'application/json'); assert.equal(options.body, body)
    }
  })
  test('HTTP failures are classified without displaying upstream HTML, secrets or stack traces', async t => {
    const kinds = new Map([[400, 'response'], [401, 'unauthorized'], [403, 'forbidden'], [409, 'conflict'], [429, 'rate_limit'], [503, 'server']])
    for (const [status, kind] of kinds) {
      const readBody = t.mock.fn(() => { throw new Error('must not inspect error bodies') })
      t.mock.method(globalThis, 'fetch', async () => ({ ok: false, status, json: readBody, text: readBody }))
      await assert.rejects(requestJSON(url, 'token'), error => {
        assert.ok(error instanceof ApiError); assert.equal(error.status, status); assert.equal(error.kind, kind)
        assert.doesNotMatch(error.message, /<html|sk_live_|stack/i); return true
      })
      assert.equal(readBody.mock.callCount(), 0)
    }
  })
  test('network failure is safe and never automatically retries a mutation', async t => {
    const fetch = t.mock.method(globalThis, 'fetch', async () => { throw new Error('sk_live_do_not_display') })
    await assert.rejects(requestJSON(url, 'token', { method: 'POST', body: '{}' }), error => error.kind === 'network' && !error.message.includes('sk_live_'))
    assert.equal(fetch.mock.callCount(), 1)
  })
  test('timeout aborts fetch even if fetch ignores cancellation', async t => {
    t.mock.timers.enable({ apis: ['setTimeout'] })
    let signal
    t.mock.method(globalThis, 'fetch', async (_, init) => { signal = init.signal; return new Promise(() => {}) })
    const pending = requestJSON(url, 'token', { timeoutMs: 20 })
    const rejected = assert.rejects(pending, error => error.kind === 'timeout')
    t.mock.timers.tick(20)
    await rejected; assert.equal(signal.aborted, true)
  })
  test('deadline covers stalled JSON body parsing, not just response headers', async t => {
    t.mock.timers.enable({ apis: ['setTimeout'] })
    const entered = deferred()
    t.mock.method(globalThis, 'fetch', async () => ({ ok: true, json: () => { entered.resolve(); return new Promise(() => {}) } }))
    const pending = requestJSON(url, 'token', { timeoutMs: 20 })
    const rejected = assert.rejects(pending, error => error.kind === 'timeout')
    await entered.promise; t.mock.timers.tick(20); await rejected
  })
  test('explicit cancellation differs from timeout; pre-aborted sessions never fetch', async t => {
    const scope = new RequestScope(); scope.close()
    const fetch = t.mock.method(globalThis, 'fetch', async () => new Response('{}'))
    await assert.rejects(requestJSON(url, 'token', { signal: scope.signal }), error => error.kind === 'cancelled')
    assert.equal(fetch.mock.callCount(), 0); assert.throws(() => scope.assertCurrent(), { kind: 'cancelled' })
  })
  test('identity change prevents an old body from committing while new-session requests succeed', async t => {
    const body = deferred(); const entered = deferred(); const scope = new RequestScope(); let committed = false
    t.mock.method(globalThis, 'fetch', async (_, options) => options.headers.get('Authorization') === 'Bearer old'
      ? { ok: true, json: () => { entered.resolve(); return body.promise } }
      : new Response('{"actor":"new"}'))
    const pending = requestJSON(url, 'old', { signal: scope.signal }).then(() => { scope.assertCurrent(); committed = true })
    const rejected = assert.rejects(pending, { kind: 'cancelled' })
    await entered.promise; scope.close(); body.resolve({ actor: 'old' }); await rejected
    assert.deepEqual(await requestJSON(url, 'new', { signal: new RequestScope().signal }), { actor: 'new' })
    assert.equal(committed, false)
  })
  test('success and failure always clear deadline timers and detach abort listeners', async t => {
    const clear = t.mock.method(globalThis, 'clearTimeout')
    for (const fail of [false, true]) {
      const scope = new RequestScope()
      const remove = t.mock.method(scope.signal, 'removeEventListener')
      t.mock.method(globalThis, 'fetch', async () => { if (fail) throw new Error('offline'); return new Response('{}') })
      if (fail) await assert.rejects(requestJSON(url, 'token', { signal: scope.signal }))
      else await requestJSON(url, 'token', { signal: scope.signal })
      assert.equal(remove.mock.callCount(), 1)
      assert.equal(remove.mock.calls[0].arguments[0], 'abort')
    }
    assert.equal(clear.mock.callCount(), 2)
  })
  test('invalid JSON and redirected success are safe response errors; 204 is supported', async t => {
    for (const response of [new Response('<html>secret</html>'), { ok: true, redirected: true }]) {
      t.mock.method(globalThis, 'fetch', async () => response)
      await assert.rejects(requestJSON(url, 'token'), { kind: 'response' })
    }
    t.mock.method(globalThis, 'fetch', async () => new Response(null, { status: 204 }))
    assert.equal(await requestJSON(url, 'token'), undefined)
  })
  test('invalid credentials and deadlines fail before network activity', async t => {
    const fetch = t.mock.method(globalThis, 'fetch', async () => new Response('{}'))
    for (const token of ['', ' \t ', 'bad\r\nheader']) await assert.rejects(requestJSON(url, token), { kind: 'configuration' })
    for (const timeoutMs of [0, -1, NaN, Infinity, 120001]) await assert.rejects(requestJSON(url, 'token', { timeoutMs }), { kind: 'configuration' })
    assert.equal(fetch.mock.callCount(), 0)
  })
}