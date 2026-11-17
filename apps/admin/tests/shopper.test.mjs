import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { shopperAmount, shopperRef, shopperURL, shopperRequest } from '../src/shopperInputs.ts'

const key = '11111111-1111-4111-8111-111111111111'
test('completion amount is explicit positive bounded decimal cents, not a default', () => {
  assert.equal(shopperAmount('5.01'), 501)
  assert.equal(shopperAmount('1000000.00'), 100000000)
  for (const input of ['', '0', '-1', '1e3', '1.001', '1000000.01']) assert.throws(() => shopperAmount(input))
})
test('references reject raw PII, URLs and unbounded evidence', () => {
  assert.equal(shopperRef('opaque_worker-1'), 'opaque_worker-1')
  for (const input of ['', 'Jane Doe', 'person@example.com', 'https://private/proof', 'x'.repeat(101)]) assert.throws(() => shopperRef(input))
})
test('exact shopper URL retains gateway prefix and denies unsafe connections', () => {
  assert.equal(shopperURL('https://example.com/v1', key).href, `https://example.com/v1/admin/shopper/tasks/${key}/complete`)
  assert.equal(shopperURL('http://localhost:8081').pathname, '/admin/shopper/tasks')
  for (const input of ['http://example.com', 'ftp://localhost', 'https://u:p@example.com', 'https://example.com?token=x', 'https://example.com#x']) assert.throws(() => shopperURL(input))
  assert.throws(() => shopperURL('https://example.com', 'invalid'))
})
test('requests use admin user token only, exact payload and preserved retry key', async () => {
  const original = globalThis.fetch
  const calls = []
  globalThis.fetch = async (url, options) => { calls.push({ url: url.href, options }); return { ok: true, json: async () => ({ status: 'verified_completed', earnings: { status: 'pending_review' } }) } }
  try {
    const body = { itemsConfirmed: true, substitutionsConfirmed: true, receiptRef: 'r1', handoffRef: 'h1' }
    for (let i = 0; i < 2; i++) await shopperRequest('https://example.com/v1', 'user-token', 'POST', body, key, key)
    assert.equal(calls[0].url, calls[1].url)
    assert.equal(calls[0].options.body, calls[1].options.body)
    assert.deepEqual([...calls[0].options.headers], [...calls[1].options.headers])
    assert.equal(calls[0].options.redirect, 'error')
    assert.deepEqual(Object.fromEntries(calls[0].options.headers), { accept: 'application/json', authorization: 'Bearer user-token', 'content-type': 'application/json', 'idempotency-key': key })
    assert.deepEqual(JSON.parse(calls[0].options.body), body)
    await shopperRequest('https://example.com/v1', 'user-token', 'GET', undefined, undefined, undefined, 50)
    assert.match(calls[2].url, /limit=50&offset=50$/)
    assert.equal(calls[2].options.body, undefined)
    await assert.rejects(shopperRequest('https://example.com', '', 'POST', body, key))
    await assert.rejects(shopperRequest('https://example.com', 'token', 'POST', body, 'bad'))
  } finally { globalThis.fetch = original }
})
test('conflicts are visible and completion UI never implies payment or legal clearance', async () => {
  const original = globalThis.fetch
  globalThis.fetch = async () => ({ ok: false, status: 409, json: async () => ({ error: 'completion conflict' }) })
  try { await assert.rejects(shopperRequest('https://example.com', 'token', 'POST', {}, key, key), { kind: 'conflict', status: 409 }) } finally { globalThis.fetch = original }
  const ui = readFileSync(new URL('../src/ShopperOrders.tsx', import.meta.url), 'utf8')
  assert.match(ui, /pending_review, not payable or paid/)
  assert.match(ui, /not legal clearance, teen enrollment/)
  assert.match(ui, /setCompletionKey\(key\)/)
  assert.match(ui, /setAssignmentKey\(key\)/)
  assert.match(ui, /no money transferred/)
  assert.doesNotMatch(ui, /localStorage|sessionStorage|\/payout|\/approve/)
})