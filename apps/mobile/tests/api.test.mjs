import test from 'node:test'
import assert from 'node:assert/strict'
import { apiRequest, RequestScope } from '../src/api.ts'

test('API preserves gateway prefix, query, session abort and mutation retry headers', async t => {
  let call
  t.mock.method(globalThis, 'fetch', async (url, options) => { call = { url, options }; return new Response('{}') })
  await apiRequest({ baseUrl: 'https://api.shopstops.com/v1/', sessionToken: 'actor' }, 'stores/store-id/catalog?limit=50', { method: 'POST', headers: { 'Idempotency-Key': 'retained' }, body: '{}' })
  assert.equal(call.url.href, 'https://api.shopstops.com/v1/stores/store-id/catalog?limit=50')
  assert.equal(call.options.headers.get('Idempotency-Key'), 'retained')
  const scope = new RequestScope(); scope.close()
  await assert.rejects(apiRequest({ baseUrl: 'https://api.shopstops.com/v1', sessionToken: 'actor', signal: scope.signal }, 'orders'), { kind: 'cancelled' })
})
test('API cannot escape configured gateway prefix or use release-local HTTP', async t => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => new Response('{}'))
  const settings = { baseUrl: 'https://api.shopstops.com/v1', sessionToken: 'actor' }
  for (const path of ['../admin', '%2e%2e/admin']) await assert.rejects(apiRequest(settings, path), { kind: 'configuration' })
  await assert.rejects(apiRequest({ ...settings, baseUrl: 'http://localhost:8081' }, 'orders'), { kind: 'configuration' })
  assert.equal(fetch.mock.callCount(), 0)
})