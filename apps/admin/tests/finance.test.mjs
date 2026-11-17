import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { parseMoney, parseCount, parsePickupRadius, parseScenarioInput, dollars, financeURL } from '../src/financeInputs.ts'

test('decimal dollars parse exactly into bounded cents', () => {
  for (const [input, expected] of [['0', 0], ['0.01', 1], ['6.2', 620], ['199.00', 19900], ['1000000.00', 100000000]]) assert.equal(parseMoney(input), expected)
  for (const input of ['', '-1', '+1', '1e3', '.5', '1.', 'NaN', 'Infinity', '1.001', '1,000', ' 1', '1000000.01']) assert.throws(() => parseMoney(input))
})
test('counts reject fractions, overflow and invalid drops', () => {
  assert.equal(parseCount('100000'), 100000)
  assert.equal(parseCount('100', 1, 100), 100)
  for (const input of ['-1', '1.5', '1e2', '', '100001']) assert.throws(() => parseCount(input))
  assert.throws(() => parseCount('0', 1, 100))
})
test('planning radius parses decimal miles exactly and enforces the outer cap', () => {
  for (const [text, expected] of [['0', 0], ['0.001', 1], ['0.25', 250], ['0.250', 250], ['0.5', 500], ['0.500', 500]]) {
    assert.equal(parsePickupRadius(text), expected)
    assert.equal(parseScenarioInput('pickupRadiusMilliMiles', text, false), expected)
  }
  for (const text of ['', '.25', '-0.25', '0.501', '0.5001', '1', '0.', ' 0.25', '0.25 ', '2.5e-1', 'NaN', 'Infinity', '0,25']) assert.throws(() => parsePickupRadius(text))
})
test('scenario extension inputs retain separate cents and bounded counts', () => {
  for (const key of ['originalPickupCents', 'shopperCentsPerCheckout', 'fastShoppingFeeCents', 'fastDeliveryFeeCents', 'fastShopperPremiumCents', 'fastDriverPremiumCents']) {
    assert.equal(parseScenarioInput(key, '0.00', true), 0)
    assert.equal(parseScenarioInput(key, '5.01', true), 501)
  }
  assert.equal(parseScenarioInput('additionalPickupsPerBundle', '100', false), 100)
  assert.throws(() => parseScenarioInput('additionalPickupsPerBundle', '101', false))
  assert.equal(parseScenarioInput('fastServiceAdoptionBasisPoints', '3333', false), 3333)
  assert.equal(parseScenarioInput('fastServiceAdoptionBasisPoints', '10000', false), 10000)
  assert.throws(() => parseScenarioInput('fastServiceAdoptionBasisPoints', '10001', false))
  assert.throws(() => parseScenarioInput('fastServiceAdoptionBasisPoints', '33.33', false))
  assert.throws(() => parseScenarioInput('dropsPerBundle', '0', false))
})
test('maintenance uses exact bounded money parsing and an unapproved zero UI assumption', () => {
  for (const [text, expected] of [['0.00', 0], ['5.01', 501], ['1000000.00', 100000000]]) {
    assert.equal(parseScenarioInput('maintenanceCents', text, true), expected)
  }
  for (const text of ['', '-1', '1.001', '1e3', '1000000.01', ' 1']) assert.throws(() => parseScenarioInput('maintenanceCents', text, true))
  const ui = readFileSync(new URL('../src/FinancePlanning.tsx', import.meta.url), 'utf8')
  assert.match(ui, /\['maintenanceCents', 'Monthly maintenance\/support \(exclude other overhead\) \$', '0\.00', true\]/)
  assert.match(ui, /\['serviceFeeCents', 'Platform\/service fee per checkout — assumed \$', '2\.00', true\]/)
})
test('finance OpenAPI properties match calculator JSON fields and all outputs are required', () => {
  const go = readFileSync(new URL('../../../internal/finance/scenario.go', import.meta.url), 'utf8')
  const api = readFileSync(new URL('../../../api/openapi.yaml', import.meta.url), 'utf8')
  const fields = name => [...go.match(new RegExp(`type ${name} struct \\{([\\s\\S]*?)\\n\\}`))[1].matchAll(/json:"([^"]+)"/g)].map(match => match[1]).sort()
  const schema = name => api.match(new RegExp(`^    ${name}:\\n([\\s\\S]*?)(?=^    \\w+:|$(?![\\s\\S]))`, 'm'))[1]
  const properties = block => [...block.matchAll(/^        (\w+):/gm)].map(match => match[1]).sort()
  const required = block => block.match(/^      required: \[([^\]]+)\]/m)[1].split(',').map(key => key.trim()).sort()
  assert.deepEqual(properties(schema('FinanceScenario')), fields('Scenario'))
  assert.deepEqual(required(schema('FinanceScenario')), ['dropsPerBundle'])
  assert.deepEqual(properties(schema('FinanceScenarioResult')), fields('Result'))
  assert.deepEqual(required(schema('FinanceScenarioResult')), fields('Result'))
  assert.match(schema('FinanceScenario'), /maintenanceCents:\n          allOf: \[\{ \$ref: '#\/components\/schemas\/FinanceCents' \}\]/)
})
test('platform pickup scope is separate from driver bundle scope and uses fixed 25 bps', () => {
  assert.equal(parseScenarioInput('additionalPickupsPerCheckout', '100', false), 100)
  for (const value of ['101', '-1', '1.5', '1e2']) assert.throws(() => parseScenarioInput('additionalPickupsPerCheckout', value, false))
  assert.equal(parseScenarioInput('merchandiseSubtotalCents', '50.00', true), 5000)
  assert.throws(() => parseScenarioInput('merchandiseSubtotalCents', '1000000.01', true))
  const ui = readFileSync(new URL('../src/FinancePlanning.tsx', import.meta.url), 'utf8')
  assert.match(ui, /\['merchandiseSubtotalCents', [^\n]+ '0\.00', true\]/)
  assert.match(ui, /\['additionalPickupsPerCheckout', [^\n]+ '0', false\]/)
  assert.match(ui, /NOT charged to each checkout/)
  assert.match(ui, /fixed 25 basis points \(0\.25%, NOT 25%\)/)
  assert.match(ui, /\$50 with one extra = \$0\.13; \$100 with two extras = \$0\.50/)
  const go = readFileSync(new URL('../../../internal/finance/scenario.go', import.meta.url), 'utf8')
  const sql = readFileSync(new URL('../../../db/migrations/011_extra_pickup_test_checkout.sql', import.meta.url), 'utf8')
  assert.match(go, /const AdditionalPickupPlatformFeeBasisPoints int64 = 25/)
  assert.match(sql, /rate_basis_points = 25/)
  assert.match(sql, /\(v_subtotal \* 25 \* v_extra \+ 5000\) \/ 10000/)
  assert.match(sql, /count\(DISTINCT o\.store_location_id\) - 1/)
  assert.match(sql, /line_type = 'merchant_items'/)
})
test('format exact cents including negative and large ledger totals', () => {
  assert.equal(dollars(-1), '-$0.01')
  assert.equal(dollars('900719925474099301'), '$9007199254740993.01')
  assert.throws(() => dollars(Number.MAX_SAFE_INTEGER + 1))
})
test('finance settings require trusted HTTPS, preserve gateway prefix', () => {
  assert.equal(financeURL('https://api.example/v1/', 'costs').href, 'https://api.example/v1/admin/finance/costs')
  assert.equal(financeURL('http://localhost:8081/v1', 'scenarios').protocol, 'http:')
  for (const base of ['http://api.example', 'https://user:pass@api.example', 'https://api.example/?secret=x', 'https://api.example/#x', 'ftp://localhost']) assert.throws(() => financeURL(base, 'costs'))
})