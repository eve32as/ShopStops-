import { test, expect, connect, navigation } from './harness.mjs'
import { assignedTask, completedTask, costReport, manualCost, orderId, scenario, taskPage } from './fixtures.mjs'

test.describe('simulated API frontend acceptance', () => {
  test('direct hashes, history, active navigation and workspace drafts agree', async ({ page, api }) => {
    await page.goto('/#finance-planning')
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Plan the economics')
    const volume = page.getByLabel('Monthly checkouts (one checkout per drop)', { exact: true })
    await volume.fill('1234')
    await navigation(page).getByRole('link', { name: 'Refund desk', exact: true }).click()
    await page.getByLabel('Checkout ID', { exact: true }).fill(orderId)
    await page.goBack()
    await expect(volume).toHaveValue('1234')
    await expect(navigation(page).getByRole('link', { name: 'Finance planning', exact: true })).toHaveAttribute('aria-current', 'page')
    await page.goForward()
    await expect(page.getByLabel('Checkout ID', { exact: true })).toHaveValue(orderId)
    await page.goto('/#not-a-workspace')
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('The right tool for the next step')
    expect(api.calls).toHaveLength(0)
  })

  test('finance rejects invalid input, then displays API-owned estimates with honest boundaries', async ({ page, api }) => {
    api.on('POST', '/admin/finance/scenarios', ({ reply }) => reply(scenario))
    await page.goto('/#finance-planning')
    await connect(page)
    const volume = page.getByLabel('Monthly checkouts (one checkout per drop)', { exact: true })
    await volume.fill('1e3')
    await page.getByRole('button', { name: 'Calculate estimate', exact: true }).click()
    await expect(page.getByRole('alert')).toContainText('Monthly checkouts')
    expect(api.calls).toHaveLength(0)
    await volume.fill('1000')
    await page.getByRole('button', { name: 'Calculate estimate', exact: true }).click()
    await expect(page.getByRole('status')).toContainText('Estimated monthly result — not verified revenue')
    await expect(page.getByRole('heading', { name: '$8000.00', exact: true })).toBeVisible()
    await expect(page.getByRole('heading', { name: '$2400.00', exact: true })).toBeVisible()
    await expect(page.getByRole('heading', { name: '200', exact: true })).toBeVisible()
    expect(api.calls).toHaveLength(1)
    expect(api.calls[0].body).toMatchObject({ checkouts: 1000, deliveryFeeCents: 600, serviceFeeCents: 200, dropsPerBundle: 1, pickupRadiusMilliMiles: 500 })
    expect(Object.values(api.calls[0].body).every(Number.isSafeInteger)).toBe(true)
    // Deliberately distinctive API values prove the browser does not replace
    // the server result with its own pricing arithmetic.
    api.on('POST', '/admin/finance/scenarios', ({ reply }) => reply({ ...scenario, platformRevenueForecastCents: 12345, netOperatingCents: -123, breakEvenCheckouts: null, netMarginBasisPoints: null }))
    await page.getByRole('button', { name: 'Calculate estimate', exact: true }).click()
    await expect(page.getByRole('heading', { name: '$123.45', exact: true })).toBeVisible()
    await expect(page.getByRole('heading', { name: '-$1.23', exact: true })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Not achievable', exact: true })).toBeVisible()
    await volume.fill('999')
    await expect(page.getByRole('heading', { name: 'No estimate yet', exact: true })).toBeVisible()
  })

  for (const status of [401, 403]) {
    test(`simulated ${status} role/session rejection is safe and not retried`, async ({ page, api }) => {
      api.on('GET', '/admin/shopper/tasks', ({ reply }) => reply({ error: '<script>UPSTREAM_PRIVATE_DETAIL</script>' }, status))
      await page.goto('/#shopper-orders')
      await connect(page)
      await page.getByRole('button', { name: '3. Review records', exact: true }).click()
      await page.getByRole('button', { name: 'Refresh tasks', exact: true }).click()
      await expect(page.getByRole('alert').first()).toContainText(status === 401 ? 'session expired or was rejected' : 'Access denied')
      await expect(page.locator('body')).not.toContainText('UPSTREAM_PRIVATE_DETAIL')
      await expect(page.getByRole('button', { name: 'Refresh tasks', exact: true })).toBeEnabled()
      expect(api.calls).toHaveLength(1)
    })
  }

  test('shopper assignment and completion keep retry keys on failures/conflicts and remain pending review', async ({ page, api }) => {
    let assignments = 0
    let completions = 0
    let taskId
    api.on('POST', '/admin/shopper/tasks', ({ call, reply }) => {
      assignments++
      taskId = call.key
      if (assignments === 1) return reply({ error: 'simulated uncertain outcome' }, 503)
      return reply(assignedTask(taskId), 201)
    })
    await page.goto('/#shopper-orders')
    await connect(page)
    await page.getByLabel('Existing merchant order UUID', { exact: true }).fill('invalid')
    await page.getByLabel('Opaque shopper actor', { exact: true }).fill('synthetic_worker')
    await page.getByLabel('Explicitly approved completion amount (USD)', { exact: true }).fill('5.01')
    await page.getByLabel('Jurisdiction reference — not clearance', { exact: true }).fill('synthetic_jurisdiction')
    await page.getByRole('button', { name: 'Record assignment', exact: true }).click()
    await expect(page.getByRole('alert')).toContainText('existing merchant order UUID')
    expect(api.calls).toHaveLength(0)
    await page.getByLabel('Existing merchant order UUID', { exact: true }).fill(orderId)
    await page.getByRole('button', { name: 'Record assignment', exact: true }).click()
    await expect(page.getByRole('alert')).toContainText('may still have completed')
    expect(assignments).toBe(1)
    await page.getByRole('button', { name: 'Record assignment', exact: true }).click()
    await expect(page.getByLabel('Task UUID', { exact: true })).toHaveValue(taskId)
    expect(api.calls[1]).toEqual(api.calls[0])
    expect(api.calls[0].body).toEqual({ orderId, shopperActor: 'synthetic_worker', completionAmountCents: 501, jurisdictionRef: 'synthetic_jurisdiction' })
    await expect(page.getByRole('status')).toContainText('no earnings created')
    api.on('POST', `/admin/shopper/tasks/${taskId}/complete`, ({ reply }) => {
      completions++
      return completions === 1 ? reply({ error: 'simulated conflict' }, 409) : reply(completedTask(taskId))
    })
    await page.getByLabel('Private receipt evidence reference', { exact: true }).fill('synthetic_receipt')
    await page.getByLabel('Private handoff proof reference', { exact: true }).fill('synthetic_handoff')
    const submit = page.getByRole('button', { name: 'Record verified completion — pending earnings review', exact: true })
    await submit.click()
    expect(completions).toBe(0) // Native required checkboxes block incomplete evidence.
    await page.getByRole('checkbox', { name: /item shopping and receipt/ }).check()
    await page.getByRole('checkbox', { name: /authorized substitutions/ }).check()
    await submit.click()
    await expect(page.getByRole('alert')).toContainText('Reconcile the existing record')
    expect(completions).toBe(1)
    // Simulates manual reconciliation permitting an unchanged retry, not a bypass.
    await submit.click()
    await expect(page.getByRole('button', { name: '3. Review records', exact: true })).toHaveAttribute('aria-pressed', 'true')
    await expect(page.getByRole('status').first()).toContainText('$5.01 earned per order — pending_review; no money transferred.')
    expect(api.calls[3]).toEqual(api.calls[2])
    expect(api.calls[2].body).toEqual({ itemsConfirmed: true, substitutionsConfirmed: true, receiptRef: 'synthetic_receipt', handoffRef: 'synthetic_handoff' })
    expect(api.calls[2].key).not.toBe(api.calls[0].key)
    api.on('GET', '/admin/shopper/tasks', ({ call, reply }) => {
      expect(call.query).toBe('?limit=50&offset=0')
      return reply(taskPage([completedTask(taskId)]))
    })
    await page.getByRole('button', { name: 'Refresh tasks', exact: true }).click()
    await expect(page.getByText('verified_completed · $5.01 earned, pending review', { exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Next', exact: true })).toBeDisabled()
    api.on('GET', '/admin/shopper/tasks', ({ reply }) => reply(taskPage([])))
    await page.getByRole('button', { name: 'Refresh tasks', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'No records returned on this page', exact: true })).toBeVisible()
  })

  test('simulated refund retries keep the key and clearing the session removes the result', async ({ page, api }) => {
    let attempts = 0
    api.on('POST', `/admin/checkouts/${orderId}/refund`, ({ reply }) => {
      attempts++
      return attempts === 1 ? reply({ error: 'simulated unavailable' }, 503) : reply({ refundId: 're_synthetic', status: 'pending', amountCents: 501, currency: 'usd' }, 202)
    })
    await page.goto('/#refund-desk')
    await connect(page)
    await page.getByLabel('Checkout ID', { exact: true }).fill('invalid')
    const submit = page.getByRole('button', { name: /Submit refund request/ })
    await submit.click()
    await expect(page.getByRole('alert')).toContainText('valid checkout UUID')
    expect(api.calls).toHaveLength(0)
    await page.getByLabel('Checkout ID', { exact: true }).fill(orderId)
    await submit.click()
    await expect(page.getByRole('alert')).toContainText('may still have completed')
    expect(attempts).toBe(1)
    await submit.click()
    await expect(page.getByRole('status')).toContainText('Refund pending · USD $5.01 · re_synthetic')
    expect(api.calls[1]).toEqual(api.calls[0])
    expect(api.calls[0].body).toEqual({ reason: 'requested_by_customer' })
    expect(api.calls[0].key).toMatch(/^[a-f0-9-]{36}$/)
    await page.getByRole('button', { name: 'Clear session and private records', exact: true }).click()
    await expect(page.getByRole('status')).toHaveCount(0)
    await expect(page.getByLabel('Checkout ID', { exact: true })).toHaveValue('')
    await expect(page.getByLabel('Admin session token', { exact: true })).toHaveValue('')
  })

  test('manual cost records exact cents, retries unchanged, and loads a separate server report', async ({ page, api }) => {
    let attempts = 0
    let cost
    api.on('POST', '/admin/finance/costs', ({ call, reply }) => {
      attempts++
      cost = manualCost(call.key)
      return attempts === 1 ? reply({ error: 'simulated uncertain outcome' }, 503) : reply(cost, 201)
    })
    api.on('GET', '/admin/finance/costs', ({ call, reply }) => {
      expect(call.query).toBe('?period=2026-11&limit=50&offset=0')
      return reply(costReport(cost))
    })
    await page.goto('/#finance-planning')
    await connect(page)
    await page.getByLabel('UTC accounting period', { exact: true }).fill('2026-11')
    await page.getByLabel('Manual cost $', { exact: true }).fill('5.01')
    const submit = page.getByRole('button', { name: 'Record manual cost — no payment', exact: true })
    await submit.click()
    await expect(page.getByRole('alert')).toContainText('may still have completed')
    expect(attempts).toBe(1)
    await submit.click()
    await expect(page.getByRole('status')).toContainText('Manual cost recorded: $5.01')
    expect(api.calls[1]).toEqual(api.calls[0])
    expect(api.calls[0].body).toEqual({ period: '2026-11', category: 'hosting', amountCents: 501 })
    await page.getByRole('button', { name: 'Load this month’s cost report', exact: true }).click()
    await expect(page.getByRole('table')).toContainText('synthetic_admin')
    await expect(page.getByText('Total manual costs for 2026-11:', { exact: false })).toContainText('$5.01')
    await expect(page.getByRole('button', { name: 'Next page', exact: true })).toBeDisabled()
    await expect(page.getByRole('heading', { name: 'No estimate yet', exact: true })).toBeVisible()
  })

  for (const change of ['clear session', 'change gateway', 'change token']) {
    test(`${change} clears private drafts/results and aborts a pending stale response`, async ({ page, api }) => {
      api.on('POST', '/admin/finance/scenarios', ({ reply }) => reply(scenario))
      await page.goto('/#finance-planning')
      await connect(page)
      await page.getByRole('button', { name: 'Calculate estimate', exact: true }).click()
      await expect(page.getByRole('heading', { name: '$2400.00', exact: true })).toBeVisible()
      await navigation(page).getByRole('link', { name: 'Refund desk', exact: true }).click()
      await page.getByLabel('Checkout ID', { exact: true }).fill(orderId)
      await navigation(page).getByRole('link', { name: 'Shopper orders', exact: true }).click()
      await page.getByLabel('Opaque shopper actor', { exact: true }).fill('private_draft')
      await page.getByRole('button', { name: '3. Review records', exact: true }).click()
      api.on('GET', '/admin/shopper/tasks', ({ reply }) => reply(taskPage([assignedTask(orderId)])))
      await page.getByRole('button', { name: 'Refresh tasks', exact: true }).click()
      await expect(page.locator('.shopper-task-list article')).toHaveCount(1)
      let release
      let started
      const pending = new Promise(resolve => { release = resolve })
      const intercepted = new Promise(resolve => { started = resolve })
      api.on('GET', '/admin/shopper/tasks', async ({ reply }) => {
        started()
        await pending
        // Chromium may reject fulfillment once the request is aborted.
        await reply(taskPage([assignedTask(orderId)])).catch(() => {})
      })
      await page.getByRole('button', { name: 'Refresh tasks', exact: true }).click()
      await intercepted
      await expect(page.getByRole('button', { name: 'Refresh tasks', exact: true })).toBeDisabled()
      const aborted = page.waitForEvent('requestfailed', request => request.url().includes('/admin/shopper/tasks'))
      try {
        if (change === 'clear session') await page.getByRole('button', { name: 'Clear session and private records', exact: true }).click()
        else if (change === 'change gateway') await page.getByRole('textbox', { name: 'Operator connection · public gateway URL', exact: true }).fill('https://replacement.shopstops.example/v1')
        else await page.getByLabel('Admin session token', { exact: true }).fill('synthetic-replacement-not-a-credential')
        await aborted
      } finally { release() }
      await expect(page.getByRole('button', { name: '1. Assign order', exact: true })).toHaveAttribute('aria-pressed', 'true')
      await expect(page.getByLabel('Opaque shopper actor', { exact: true })).toHaveValue('')
      await page.getByRole('button', { name: '3. Review records', exact: true }).click()
      await expect(page.getByRole('heading', { name: 'Records have not been loaded', exact: true })).toBeVisible()
      await expect(page.locator('.shopper-task-list article')).toHaveCount(0)
      await navigation(page).getByRole('link', { name: 'Finance planning', exact: true }).click()
      await expect(page.getByRole('heading', { name: 'No estimate yet', exact: true })).toBeVisible()
      await navigation(page).getByRole('link', { name: 'Refund desk', exact: true }).click()
      await expect(page.getByLabel('Checkout ID', { exact: true })).toHaveValue('')
      if (change === 'clear session') await expect(page.getByLabel('Admin session token', { exact: true })).toHaveValue('')
      expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0])
      expect(api.calls).toHaveLength(3)
    })
  }

  test('390px keyboard navigation reaches each workspace without horizontal overflow', async ({ page, api }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/')
    const compact = page.getByRole('navigation', { name: 'Compact admin workspaces', exact: true })
    await expect(compact).toBeVisible()
    // Reach navigation using Tab, rather than programmatic focus or DOM clicks.
    await page.keyboard.press('Tab')
    await expect(compact.getByRole('link', { name: 'Overview', exact: true })).toBeFocused()
    for (const name of ['Refund desk', 'Finance planning', 'Shopper orders']) {
      const link = compact.getByRole('link', { name, exact: true })
      // Native hash activation may move focus to the fragment's panel. Find
      // the next link using only real Tab presses, including across that jump.
      for (let tabs = 0; tabs < 80; tabs++) {
        await page.keyboard.press('Tab')
        if (await link.evaluate(element => element === document.activeElement)) break
      }
      await expect(link).toBeFocused()
      await page.keyboard.press('Enter')
      await expect(compact.getByRole('link', { name, exact: true })).toHaveAttribute('aria-current', 'page')
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    }
    expect(api.calls).toHaveLength(0)
  })
})