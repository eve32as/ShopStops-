import assert from 'node:assert/strict'
import test from 'node:test'
import { adminConfiguration } from '../src/config.ts'
import { financeURL } from '../src/financeInputs.ts'
import { shopperURL } from '../src/shopperInputs.ts'

test('admin config preserves manual operator entry but never hides an invalid configured release URL', () => {
  assert.deepEqual(adminConfiguration(undefined, false), { baseUrl: '', error: '' })
  assert.deepEqual(adminConfiguration('https://api.shopstops.com/v1/', false), { baseUrl: 'https://api.shopstops.com/v1', error: '' })
  for (const url of ['http://localhost:8081', 'https://127.0.0.1', 'https://10.1.1.1', 'https://api.local', 'https://token@api.shopstops.com']) {
    const config = adminConfiguration(url, false)
    assert.ok(config.error); assert.equal(config.baseUrl, ''); assert.ok(!config.error.includes('token@'))
  }
})
test('finance and shopper helpers use the same release gateway policy and exact prefix', () => {
  for (const url of ['http://localhost:8081', 'https://localhost', 'https://192.168.1.1']) {
    assert.throws(() => financeURL(url, 'costs', false))
    assert.throws(() => shopperURL(url, undefined, false))
  }
  assert.equal(financeURL('https://api.shopstops.com/v1', 'costs', false).pathname, '/v1/admin/finance/costs')
  assert.equal(shopperURL('https://api.shopstops.com/v1', undefined, false).pathname, '/v1/admin/shopper/tasks')
  assert.throws(() => financeURL('https://api.shopstops.com/v1', '../refunds', false))
})