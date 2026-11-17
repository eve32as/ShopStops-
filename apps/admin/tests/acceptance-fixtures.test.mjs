import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { assignedTask, completedTask, costReport, manualCost, orderId, scenario, taskPage } from '../acceptance/fixtures.mjs'

test('simulated acceptance fixtures contain every required OpenAPI response field', () => {
  const api = readFileSync(new URL('../../../api/openapi.yaml', import.meta.url), 'utf8')
  for (const [schema, fixtures] of [['FinanceScenarioResult', [scenario]], ['FinanceCost', [manualCost(orderId)]], ['ShopperTask', [assignedTask(orderId), completedTask(orderId)]]]) {
    const block = api.match(new RegExp(`^    ${schema}:\\n([\\s\\S]*?)(?=^    \\w+:)`, 'm'))[1]
    const required = block.match(/^      required: \[([^\]]+)\]/m)[1].split(',').map(key => key.trim())
    for (const fixture of fixtures) for (const key of required) assert.ok(Object.hasOwn(fixture, key), `${schema}.${key}`)
    if (schema === 'ShopperTask') {
      for (const name of ['completion', 'earnings']) {
        const nested = block.match(new RegExp(`^        ${name}:\\n[\\s\\S]*?^          required: \\[([^\\]]+)\\]`, 'm'))[1].split(',').map(key => key.trim())
        for (const key of nested) assert.ok(Object.hasOwn(fixtures[1][name], key), `ShopperTask.${name}.${key}`)
      }
    }
  }
  assert.equal(assignedTask(orderId).earnings, null)
  assert.equal(completedTask(orderId).earnings.status, 'pending_review')
  assert.deepEqual(Object.keys(taskPage([])).sort(), ['limit', 'offset', 'tasks'])
  assert.equal(costReport(manualCost(orderId)).totalCents, '501')
})