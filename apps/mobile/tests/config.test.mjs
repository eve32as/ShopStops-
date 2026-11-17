import assert from 'node:assert/strict'
import test from 'node:test'
import { mobileConfiguration } from '../src/config.ts'
import { validateRelease } from '../../../scripts/check-frontend-release.mjs'

const env = { EXPO_PUBLIC_API_URL: 'https://api.shopstops.com/v1', EXPO_PUBLIC_STRIPE_PUBLISHABLE_KEY: 'pk_test_fixture', VITE_API_URL: 'https://api.shopstops.com/v1' }
test('release runtime requires HTTPS nonlocal gateway and only a publishable Stripe key', () => {
  assert.equal(mobileConfiguration(env.EXPO_PUBLIC_API_URL, false, env.EXPO_PUBLIC_STRIPE_PUBLISHABLE_KEY).error, '')
  for (const url of [undefined, '', 'http://localhost:8081', 'https://127.0.0.1', 'https://api.local', 'https://10.1.1.1', 'https://u:p@api.shopstops.com']) {
    assert.ok(mobileConfiguration(url, false, env.EXPO_PUBLIC_STRIPE_PUBLISHABLE_KEY).error)
  }
  for (const key of [undefined, '', 'sk_live_secret', 'rk_test_secret', 'whsec_secret', 'pk_test_', ' pk_test_fixture']) {
    const result = mobileConfiguration(env.EXPO_PUBLIC_API_URL, false, key)
    assert.ok(result.error); assert.ok(!result.error.includes('sk_live_secret'))
  }
  assert.equal(mobileConfiguration(undefined, true).error, '')
  assert.equal(mobileConfiguration('http://localhost:8081', true).error, '')
  assert.ok(mobileConfiguration('https://api.shopstops.com', true, 'sk_test_secret').error)
})
test('static release gate checks both apps, placeholders and all public secret-like values', () => {
  assert.deepEqual(validateRelease('mobile', env), [])
  assert.deepEqual(validateRelease('admin', env), [])
  assert.ok(validateRelease('mobile', {}).length)
  assert.ok(validateRelease('admin', {}).length)
  for (const url of ['https://example.com', 'https://api.shopstops.example', 'https://api.test', 'http://localhost:8081', 'https://192.168.1.1']) {
    assert.ok(validateRelease('admin', { ...env, VITE_API_URL: url }).length)
  }
  const errors = validateRelease('mobile', { ...env, EXPO_PUBLIC_OTHER: 'sk_live_supersecret' })
  assert.ok(errors.length); assert.ok(!errors.join().includes('supersecret'))
})