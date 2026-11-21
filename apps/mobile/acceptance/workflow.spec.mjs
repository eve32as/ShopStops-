import { test as base, expect } from '../../admin/node_modules/@playwright/test/index.mjs'

const preview = 'http://127.0.0.1:4176'
const gateway = 'https://mobile-gateway.example/v1'
const token = 'synthetic-mobile-session-only'
const uuid = n => `10000000-0000-4000-8000-${String(n).padStart(12, '0')}`
const store = uuid(1), partner = uuid(2), itemID = uuid(3), orderID = uuid(4), basketID = uuid(5), routeID = uuid(6)
const item = { id: itemID, name: 'Fixture apples', description: 'Synthetic catalog item', unitPriceCents: 325, volumeLiters: '0.50' }
const savedOrder = { id: orderID, status: 'placed', itemSubtotalCents: 650 }
const basket = {
  id: basketID, status: 'quoted', currency: 'usd', totalCents: 1270,
  priority: { selected: false, testOnly: false },
  invoiceLines: [{ type: 'merchant_items', merchantId: uuid(7), amountCents: 650 }, { type: 'delivery_fee', amountCents: 500 }, { type: 'service_fee', amountCents: 100 }, { type: 'dc_delivery_surcharge', amountCents: 20 }],
}
const result = (data, status = 200) => ({ data, status })

// Every test owns a fresh browser context, fixture state and fail-closed router.
// Only exact-origin local GETs reach a server. All API traffic is fulfilled here.
const test = base.extend({
  app: async ({ page, context }, provideFixture) => {
    const handlers = new Map(), requests = [], violations = [], pageErrors = []
    const on = (method, path, handler) => handlers.set(`${method} /v1/${path}`, handler)
    const calls = (method, path) => requests.filter(entry => entry.method === method && entry.path === `/v1/${path}`)
    on('GET', 'checkout-pricing/priority', () => result({ available: false }))
    on('GET', `stores/${store}/nearby-merchants?radiusMeters=500`, () => result([{ merchantId: uuid(7), name: 'Fixture market', merchantType: 'grocery', storeLocationId: partner, distanceMeters: 120 }]))
    on('GET', `stores/${partner}/catalog`, () => result([item]))
    on('POST', 'orders', () => result(savedOrder, 201))
    on('POST', 'checkouts', () => result(basket, 201))
    on('POST', `checkouts/${basketID}/payment`, () => result({ clientSecret: 'pi_fixture_secret_synthetic', amountCents: 1270, currency: 'usd' }))
    page.on('pageerror', error => pageErrors.push(error.message))
    await context.route('**/*', async route => {
      const request = route.request(), url = new URL(request.url()), method = request.method()
      if (url.origin === preview && method === 'GET') return route.continue()
      const path = url.pathname + url.search
      const key = `${method} ${path}`
      const handler = handlers.get(key)
      // OPTIONS is allowed only for explicitly registered synthetic API resources.
      const knownPreflight = method === 'OPTIONS' && [...handlers.keys()].some(entry => entry.slice(entry.indexOf(' ') + 1) === path)
      if (url.origin !== new URL(gateway).origin || (!handler && !knownPreflight)) {
        violations.push(`Blocked unexpected request: ${method} ${url.origin}${path}`)
        return route.abort('blockedbyclient')
      }
      const cors = { 'access-control-allow-origin': preview, 'access-control-allow-methods': 'GET, POST, PATCH, OPTIONS', 'access-control-allow-headers': 'authorization, content-type, idempotency-key, accept' }
      if (knownPreflight) return route.fulfill({ status: 204, headers: cors })
      const headers = request.headers()
      if (headers.authorization !== `Bearer ${token}`) violations.push(`Non-fixture authorization for ${key}`)
      const entry = { method, path, headers, raw: request.postData(), body: request.postData() ? request.postDataJSON() : undefined }
      requests.push(entry)
      try {
        const response = await handler(entry)
        await route.fulfill({ status: response.status, headers: cors, contentType: 'application/json', body: JSON.stringify(response.data) })
      } catch (error) {
        violations.push(`Fixture failure for ${key}: ${error.message}`)
        await route.abort('failed')
      }
    })
    // Defense in depth: no HMR, socket, service-worker, or native-vendor traffic.
    await context.routeWebSocket('**/*', socket => { violations.push(`Blocked WebSocket: ${socket.url()}`); socket.close() })
    await provideFixture({ on, calls, requests, page })
    expect(violations, 'No unexpected or live traffic').toEqual([])
    expect(pageErrors, 'Actual React screen has no uncaught exceptions').toEqual([])
  },
})

const button = (page, name) => page.getByRole('button', { name, exact: true })
const field = (page, name) => page.getByRole('textbox', { name, exact: true })
async function connect(app, role = 'Customer') {
  const { page } = app
  await page.goto('/')
  await button(page, 'Setup').click()
  await button(page, 'Edit gateway and session').click()
  const dialog = page.getByRole('dialog', { name: 'Gateway settings', exact: true })
  await dialog.getByLabel('Public gateway URL', { exact: true }).fill(gateway)
  await dialog.getByLabel('User session token', { exact: true }).fill(token)
  await dialog.getByRole('button', { name: 'Done', exact: true }).click()
  if (role !== 'Customer') await page.getByRole('tab', { name: role, exact: true }).click()
}
async function cart(app) {
  const { page } = app
  await connect(app)
  await field(page, 'Anchor restaurant store ID').fill(store)
  await button(page, 'Find nearby shops').click()
  await expect(page.getByText('grocery · 120 m away', { exact: true })).toBeVisible()
  await button(page, 'Browse Fixture market').click()
  await expect(page.getByRole('tab', { name: 'Cart order step' })).toHaveAttribute('aria-selected', 'true')
  await button(page, 'Increase Fixture apples quantity').click()
  await button(page, 'Increase Fixture apples quantity').click()
  await field(page, 'Drop-off latitude (-90 to 90)').fill('38.91')
  await field(page, 'Drop-off longitude (-180 to 180)').fill('-76.98')
}
async function save(app) {
  await button(app.page, 'Save order and continue to checkout').click()
  await expect(app.page.getByText(`Order · ${orderID}`, { exact: true })).toBeVisible()
}
async function checkout(app) {
  await cart(app); await save(app)
  await button(app.page, 'Create basket').click()
  await expect(button(app.page, 'Basket created')).toBeDisabled()
}

test('customer browses, reviews a saved order, creates a basket and submits the native sheet without claiming payment confirmation', async ({ app }) => {
  const { page } = app
  await cart(app)
  const start = await field(page, 'Delivery start (ISO timestamp with timezone)').inputValue()
  const end = await field(page, 'Delivery end (ISO timestamp with timezone)').inputValue()
  await save(app)
  await expect(page.getByText('2 item(s) · USD 6.50', { exact: true })).toBeVisible()
  await expect(page.getByText(`Store · ${partner}`, { exact: true })).toBeVisible()
  await expect(page.getByText('Last returned order state · placed', { exact: true })).toBeVisible()
  await expect(page.getByText(/Saved orders cannot be edited or cancelled here/)).toContainText('38.91, -76.98')
  await page.getByRole('tab', { name: 'Cart order step' }).click()
  await expect(field(page, 'Delivery start (ISO timestamp with timezone)')).not.toBeEditable()
  await expect(field(page, 'Drop-off latitude (-90 to 90)')).not.toBeEditable()
  await page.getByRole('tab', { name: 'Checkout order step' }).click()
  await button(page, 'Create basket').click()
  await expect(button(page, 'Basket created')).toBeDisabled()
  expect(app.calls('POST', 'orders')[0].body).toEqual({ storeLocationId: partner, dropoff: { latitude: 38.91, longitude: -76.98 }, deliveryWindowStart: start, deliveryWindowEnd: end, items: [{ catalogItemId: itemID, quantity: 2 }] })
  expect(app.calls('POST', 'orders')[0].headers['idempotency-key']).toMatch(/^mobile-/)
  expect(app.calls('POST', 'checkouts')[0].body).toEqual({ priorityService: false, anchorStoreLocationId: store, orderIds: [orderID], dropoff: { latitude: 38.91, longitude: -76.98 }, deliveryWindowStart: start, deliveryWindowEnd: end })
  await button(page, 'Continue to secure payment').click()
  await expect(page.getByRole('dialog', { name: 'Simulated native payment sheet', exact: true })).toBeVisible()
  await expect(button(page, 'Reconcile payment before retrying')).toBeDisabled()
  expect(await page.evaluate(() => window.mobileAcceptance.stripe.stats())).toEqual({ initializations: [{ merchantDisplayName: 'ShopStops', paymentIntentClientSecret: 'pi_fixture_secret_synthetic' }], presentations: 1 })
  await button(page, 'Simulate sheet success').click()
  await expect(page.getByRole('tab', { name: 'Status order step' })).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByRole('heading', { name: 'Payment submitted · confirmation pending', exact: true })).toBeVisible()
  await button(page, 'Return to checkout').click()
  await expect(button(page, 'Submitted · awaiting confirmation')).toBeDisabled()
  expect(app.calls('POST', `checkouts/${basketID}/payment`)).toHaveLength(1)
  await page.getByRole('tab', { name: 'Status order step' }).click()
  await button(page, 'Start another basket · clear local records').click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Keep basket', exact: true }).click()
  await expect(page.getByText(`USD 12.70 · ${basketID}`, { exact: true })).toBeVisible()
  await button(page, 'Start another basket · clear local records').click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'I have reconciled · clear locally', exact: true }).click()
  await expect(page.getByRole('tab', { name: 'Browse order step' })).toHaveAttribute('aria-selected', 'true')
  expect(app.calls('POST', 'checkouts')).toHaveLength(1)
})

test('customer validates delivery windows before sending any order', async ({ app }) => {
  const { page } = app
  await cart(app)
  const start = field(page, 'Delivery start (ISO timestamp with timezone)'), end = field(page, 'Delivery end (ISO timestamp with timezone)')
  const now = Date.now(), iso = offset => new Date(now + offset).toISOString()
  for (const [from, to, message] of [
    ['not-an-iso-date', iso(7200000), /explicit timezone/],
    [iso(-60000), iso(7200000), /start in the future/],
    [iso(7200000), iso(3600000), /end after it starts/],
    [iso(31 * 86400000), iso(31 * 86400000 + 3600000), /within 30 days/],
    [iso(3600000), iso(26 * 3600000), /cannot exceed 24 hours/],
  ]) {
    await start.fill(from); await end.fill(to)
    await button(page, 'Save order and continue to checkout').click()
    await expect(page.getByRole('alert')).toContainText(message)
    expect(app.calls('POST', 'orders')).toHaveLength(0)
  }
  await button(page, 'Use a new window starting in 15 minutes').click()
  await save(app)
  expect(app.calls('POST', 'orders')).toHaveLength(1)
})

test('503 order uncertainty locks inputs and retains the exact body and retry key until success', async ({ app }) => {
  const { page } = app
  let attempts = 0
  app.on('POST', 'orders', () => ++attempts === 1 ? result({ error: 'synthetic unavailable' }, 503) : result(savedOrder, 201))
  await cart(app)
  await button(page, 'Save order and continue to checkout').click()
  await expect(page.getByRole('heading', { name: 'Order request awaiting reconciliation', exact: true })).toBeVisible()
  for (const label of ['Store location ID', 'Drop-off latitude (-90 to 90)', 'Drop-off longitude (-180 to 180)', 'Delivery start (ISO timestamp with timezone)', 'Delivery end (ISO timestamp with timezone)']) await expect(field(page, label)).not.toBeEditable()
  await expect(button(page, 'Increase Fixture apples quantity')).toBeDisabled()
  await expect(button(page, 'Decrease Fixture apples quantity')).toBeDisabled()
  await expect(button(page, 'Use a new window starting in 15 minutes')).toBeDisabled()
  await page.getByRole('tab', { name: 'Browse order step' }).click()
  await expect(button(page, 'Find nearby shops')).toBeDisabled()
  await expect(field(page, 'Anchor restaurant store ID')).not.toBeEditable()
  await page.getByRole('tab', { name: 'Checkout order step' }).click()
  await expect(button(page, 'Create basket')).toBeDisabled()
  await page.getByRole('tab', { name: 'Status order step' }).click()
  await expect(button(page, 'Start another basket · clear local records')).toBeDisabled()
  await page.getByRole('tab', { name: 'Cart order step' }).click()
  // An expired original window must still be retryable without changing its body.
  await page.clock.install()
  await page.clock.fastForward(3 * 3600000)
  await button(page, 'Retry unchanged order').click()
  await expect(page.getByText(`Order · ${orderID}`, { exact: true })).toBeVisible()
  const [first, retry] = app.calls('POST', 'orders')
  expect(retry.raw).toBe(first.raw)
  expect(retry.headers['idempotency-key']).toBe(first.headers['idempotency-key'])
  expect(attempts).toBe(2)
})

test('definite order 404 unlocks editing and creates a fresh key for a changed request', async ({ app }) => {
  const { page } = app
  let attempts = 0
  app.on('POST', 'orders', () => ++attempts === 1 ? result({ error: 'not found' }, 404) : result({ ...savedOrder, itemSubtotalCents: 975 }, 201))
  await cart(app)
  await button(page, 'Save order and continue to checkout').click()
  await expect(page.getByRole('alert')).toContainText('(404)')
  await expect(field(page, 'Drop-off latitude (-90 to 90)')).toBeEditable()
  await expect(field(page, 'Delivery end (ISO timestamp with timezone)')).toBeEditable()
  await button(page, 'Increase Fixture apples quantity').click()
  await field(page, 'Drop-off latitude (-90 to 90)').fill('38.92')
  await save(app)
  const [first, second] = app.calls('POST', 'orders')
  expect(second.headers['idempotency-key']).not.toBe(first.headers['idempotency-key'])
  expect(second.body.items[0].quantity).toBe(3)
  expect(second.body.dropoff.latitude).toBe(38.92)
})

test('basket 503 preserves basket body, IDs and key and prevents mutation across stages', async ({ app }) => {
  const { page } = app
  let attempts = 0
  app.on('POST', 'checkouts', () => ++attempts === 1 ? result({ error: 'unavailable' }, 503) : result(basket, 201))
  await cart(app); await save(app)
  await button(page, 'Create basket').click()
  await expect(button(page, 'Retry unchanged basket')).toBeEnabled()
  await expect(button(page, 'Add items from another store')).toBeDisabled()
  await expect(button(page, 'Refresh priority test pricing')).toBeDisabled()
  await button(page, 'Show advanced order IDs').click()
  await expect(field(page, 'Order IDs (comma separated)')).not.toBeEditable()
  await page.getByRole('tab', { name: 'Cart order step' }).click()
  await expect(field(page, 'Store location ID')).not.toBeEditable()
  await expect(button(page, 'Save order and continue to checkout')).toBeDisabled()
  await page.getByRole('tab', { name: 'Checkout order step' }).click()
  await button(page, 'Retry unchanged basket').click()
  await expect(button(page, 'Basket created')).toBeDisabled()
  const [first, retry] = app.calls('POST', 'checkouts')
  expect(retry.raw).toBe(first.raw)
  expect(retry.headers['idempotency-key']).toBe(first.headers['idempotency-key'])
})

test('saved orders keep their existing window when checkout starts after its requested start', async ({ app }) => {
  await cart(app); await save(app)
  const original = app.calls('POST', 'orders')[0].body
  await app.page.clock.install()
  await app.page.clock.fastForward(20 * 60000)
  await button(app.page, 'Create basket').click()
  await expect(button(app.page, 'Basket created')).toBeDisabled()
  expect(app.calls('POST', 'checkouts')[0].body.deliveryWindowStart).toBe(original.deliveryWindowStart)
  expect(app.calls('POST', 'checkouts')[0].body.deliveryWindowEnd).toBe(original.deliveryWindowEnd)
})

test('sheet cancellation permits retry but uncertain outcome requires explicit selectable reconciliation', async ({ app }) => {
  const { page } = app
  await checkout(app)
  await button(page, 'Continue to secure payment').click()
  await button(page, 'Cancel simulated sheet').click()
  await expect(page.getByRole('alert')).toContainText('No payment confirmation was received')
  await button(page, 'Continue to secure payment').click()
  await button(page, 'Simulate uncertain sheet error').click()
  await expect(button(page, 'Reconcile payment before retrying')).toBeDisabled()
  await button(page, 'View basket status').click()
  await expect(page.getByRole('heading', { name: 'Payment outcome uncertain · contact support', exact: true })).toBeVisible()
  await button(page, 'Support confirmed this basket is safe to retry').click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Keep blocked', exact: true }).click()
  await button(page, 'Return to checkout').click()
  await expect(button(page, 'Reconcile payment before retrying')).toBeDisabled()
  expect(app.calls('POST', `checkouts/${basketID}/payment`)).toHaveLength(2)
  await button(page, 'View basket status').click()
  await button(page, 'Support confirmed this basket is safe to retry').click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Confirmed safe by support', exact: true }).click()
  await expect(button(page, 'Continue to secure payment')).toBeEnabled()
})

function driverFixtures(app, status = 'in_progress') {
  const stops = [
    { id: uuid(13), sequence: 3, type: 'dropoff', latitude: 38.93, longitude: -76.97, status: 'pending' },
    { id: uuid(11), sequence: 1, type: 'pickup', latitude: 38.91, longitude: -76.99, status: 'pending' },
    { id: uuid(12), sequence: 2, type: 'pickup', latitude: 38.92, longitude: -76.98, status: 'pending' },
  ]
  const route = { id: routeID, status, routingProvider: 'osrm', stops, legs: [], checkoutId: basketID }
  app.on('GET', `driver/routes/${routeID}`, () => result(route))
  app.on('POST', `driver/routes/${routeID}/location`, () => result({ id: 1 }, 201))
  for (const stop of stops) app.on('POST', `driver/routes/${routeID}/stops/${stop.id}/events`, entry => {
    const type = entry.body.type
    stop.status = type === 'arrive' ? 'arrived' : 'completed'
    if (stops.every(value => value.status === 'completed')) route.status = 'completed'
    return result({ stopId: stop.id, stopStatus: stop.status, routeStatus: route.status })
  })
  return route
}
async function loadDriver(app) {
  await connect(app, 'Driver')
  await field(app.page, 'Assigned route UUID from dispatch').fill(routeID)
  await button(app.page, 'Load route').click()
  await expect(app.page.getByText('ROUTE · IN_PROGRESS', { exact: true })).toBeVisible()
}

test('terminal route refresh releases an uncertain stop lock so another assignment can be loaded', async ({ app }) => {
  const route = driverFixtures(app), { page } = app
  app.on('POST', `driver/routes/${routeID}/stops/${uuid(11)}/events`, () => result({ error: 'unavailable' }, 503))
  await loadDriver(app)
  await button(page, 'Confirm arrival').click()
  await expect(field(page, 'Assigned route UUID from dispatch')).not.toBeEditable()
  route.status = 'cancelled'
  await button(page, 'Load route').click()
  await expect(page.getByText('ROUTE · CANCELLED', { exact: true })).toBeVisible()
  await expect(field(page, 'Assigned route UUID from dispatch')).toBeEditable()
  await expect(page.getByRole('heading', { name: 'Stop request awaiting reconciliation', exact: true })).toHaveCount(0)
})

test('driver sorts unsorted stops, opens next-stop directions, reports GPS and arrives/completes through the final stop', async ({ app }) => {
  const { page } = app
  driverFixtures(app)
  await loadDriver(app)
  const labels = await page.getByText(/^(Pickup [12]|Customer drop-off)$/).allTextContents()
  expect(labels).toEqual(['Pickup 1', 'Pickup 2', 'Customer drop-off'])
  await expect(button(page, 'Confirm pickup')).toHaveCount(0)
  await button(page, 'Open directions to next stop').click()
  expect(await page.evaluate(() => window.mobileAcceptance.native.links)).toEqual(['https://www.google.com/maps/dir/?api=1&destination=38.91,-76.99&travelmode=driving'])
  await page.clock.install()
  await button(page, 'Start 5-second location sharing').click()
  await expect(page.getByRole('heading', { name: 'GPS sharing active on this device', exact: true })).toBeVisible()
  await expect(field(page, 'Assigned route UUID from dispatch')).not.toBeEditable()
  await page.clock.fastForward(5000)
  await expect.poll(() => app.calls('POST', `driver/routes/${routeID}/location`).length).toBe(1)
  expect(app.calls('POST', `driver/routes/${routeID}/location`)[0].body).toEqual({ eventId: 'gps-1795003200000', observedAt: '2026-11-18T12:00:00.000Z', location: { latitude: 38.91, longitude: -76.98 } })
  await expect(page.getByText(/Last position accepted by gateway:/)).toBeVisible()
  await page.clock.fastForward(5000)
  expect(app.calls('POST', `driver/routes/${routeID}/location`)).toHaveLength(1)
  await page.evaluate(() => window.mobileAcceptance.location.emit({ timestamp: Date.UTC(2026, 10, 18, 12, 0, 5), coords: { latitude: 38.915, longitude: -76.985 } }))
  await page.clock.fastForward(5000)
  await expect.poll(() => app.calls('POST', `driver/routes/${routeID}/location`).length).toBe(2)
  for (const sequence of [1, 2, 3]) {
    await expect(button(page, sequence === 3 ? 'Confirm delivery' : 'Confirm pickup')).toHaveCount(0)
    await button(page, 'Confirm arrival').click()
    await expect(button(page, 'Confirm arrival')).toHaveCount(0)
    await button(page, sequence === 3 ? 'Confirm delivery' : 'Confirm pickup').click()
    await expect(page.getByText(new RegExp(`^${sequence} of 3 stops completed`))).toBeVisible()
  }
  await expect(page.getByText('ROUTE · COMPLETED', { exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'GPS sharing off', exact: true })).toBeVisible()
  await expect(button(page, 'Start 5-second location sharing')).toBeDisabled()
  await expect(button(page, 'Open directions to next stop')).toHaveCount(0)
  const events = app.requests.filter(entry => entry.path.endsWith('/events'))
  expect(events.map(entry => [entry.path.split('/').at(-2), entry.body.type])).toEqual([1, 2, 3].flatMap(n => [[uuid(10 + n), 'arrive'], [uuid(10 + n), 'complete']]))
  expect(new Set(events.map(entry => entry.body.eventId)).size).toBe(6)
  for (const event of events) expect(Number.isFinite(Date.parse(event.body.observedAt))).toBe(true)
  expect(await page.evaluate(() => window.mobileAcceptance.location.stats())).toEqual({ watchers: 0, removed: 1, permissionRequests: 1 })
  await page.clock.fastForward(10000)
  expect(app.calls('POST', `driver/routes/${routeID}/location`)).toHaveLength(2)
})

for (const status of ['cancelled', 'blocked']) test(`driver ${status} route cannot progress, open directions or start GPS`, async ({ app }) => {
  driverFixtures(app, status)
  const { page } = app
  await connect(app, 'Driver')
  await field(page, 'Assigned route UUID from dispatch').fill(routeID)
  await button(page, 'Load route').click()
  await expect(page.getByText(`ROUTE · ${status.toUpperCase()}`, { exact: true })).toBeVisible()
  await expect(button(page, 'Confirm arrival')).toHaveCount(0)
  await expect(button(page, 'Confirm pickup')).toHaveCount(0)
  await expect(button(page, 'Open directions to next stop')).toHaveCount(0)
  await expect(button(page, 'Start 5-second location sharing')).toBeDisabled()
  expect(await page.evaluate(() => window.mobileAcceptance.location.stats())).toEqual({ watchers: 0, removed: 0, permissionRequests: 0 })
  expect(app.requests.filter(entry => entry.method === 'POST')).toHaveLength(0)
})

test('driver GPS permission denial, backgrounding and terminal refresh leave sharing off', async ({ app }) => {
  const route = driverFixtures(app), { page } = app
  await loadDriver(app)
  await page.evaluate(() => window.mobileAcceptance.location.permission('denied'))
  await button(page, 'Start 5-second location sharing').click()
  await expect(page.getByRole('alert')).toContainText('Allow location access')
  await expect(page.getByRole('heading', { name: 'GPS sharing off', exact: true })).toBeVisible()
  await page.evaluate(() => window.mobileAcceptance.location.permission('granted'))
  await button(page, 'Start 5-second location sharing').click()
  await expect(page.getByRole('heading', { name: 'GPS sharing active on this device', exact: true })).toBeVisible()
  await page.evaluate(() => window.mobileAcceptance.native.appState('background'))
  await expect(page.getByRole('heading', { name: 'GPS sharing off', exact: true })).toBeVisible()
  await page.evaluate(() => window.mobileAcceptance.native.appState('active'))
  await expect(page.getByRole('heading', { name: 'GPS sharing off', exact: true })).toBeVisible()
  await button(page, 'Start 5-second location sharing').click()
  await expect(page.getByRole('heading', { name: 'GPS sharing active on this device', exact: true })).toBeVisible()
  route.status = 'cancelled'
  await button(page, 'Load route').click()
  await expect(page.getByRole('heading', { name: 'GPS sharing off', exact: true })).toBeVisible()
  await expect(button(page, 'Start 5-second location sharing')).toBeDisabled()
  expect(await page.evaluate(() => window.mobileAcceptance.location.stats())).toEqual({ watchers: 0, removed: 2, permissionRequests: 3 })
})

function merchantFixtures(app) {
  let catalog = []
  app.on('GET', 'merchant/store-locations', () => result([{ id: store, name: 'Fixture restaurant' }, { id: partner, name: 'Fixture market' }]))
  app.on('GET', 'merchant/catalog', () => result(catalog))
  app.on('POST', 'merchant/catalog', entry => {
    const created = { id: itemID, ...entry.body, volumeLiters: entry.body.volumeLiters.toFixed(2), available: true }
    catalog = [created]
    return result(created, 201)
  })
  app.on('PATCH', `merchant/catalog/${itemID}`, entry => {
    catalog = [{ ...catalog[0], ...entry.body, volumeLiters: entry.body.volumeLiters.toFixed(2) }]
    return result(catalog[0])
  })
}
async function merchantDraft(app) {
  const { page } = app
  await connect(app, 'Merchant')
  await expect(button(page, 'Add catalog item')).toBeDisabled()
  await button(page, 'Load my store locations').click()
  await page.getByRole('radio', { name: new RegExp('Fixture market') }).click()
  await expect(page.getByRole('radio', { name: new RegExp('Fixture market') })).toHaveAttribute('aria-checked', 'true')
  await field(page, 'Item name').fill('Fixture pears')
  await field(page, 'Description').fill('Synthetic merchant description')
  await field(page, 'Price (cents)').fill('450')
  await field(page, 'Volume (liters)').fill('1.25')
}

test('merchant validates creation, saves PATCH changes and toggles availability without leaking an unsaved draft', async ({ app }) => {
  merchantFixtures(app)
  const { page } = app
  await merchantDraft(app)
  for (const [label, invalid, valid, message] of [
    ['Item name', 'é'.repeat(101), 'Fixture pears', /200 UTF-8 bytes/],
    ['Price (cents)', '4.5', '450', /whole-number price/],
    ['Volume (liters)', '1.251', '1.25', /two decimal places/],
  ]) {
    await field(page, label).fill(invalid)
    await button(page, 'Add catalog item').click()
    await expect(page.getByRole('alert')).toContainText(message)
    expect(app.calls('POST', 'merchant/catalog')).toHaveLength(0)
    await field(page, label).fill(valid)
  }
  await button(page, 'Add catalog item').click()
  await expect(button(page, 'Save item changes')).toBeVisible()
  expect(app.calls('POST', 'merchant/catalog')[0].body).toEqual({ storeLocationId: partner, name: 'Fixture pears', description: 'Synthetic merchant description', unitPriceCents: 450, volumeLiters: 1.25 })
  await expect(field(page, 'Item name').first()).toHaveValue('')
  const name = field(page, 'Item name').nth(1), price = field(page, 'Price (cents)').nth(1)
  await name.fill('Saved revised pears'); await price.fill('500')
  await button(page, 'Save item changes').click()
  await expect(page.getByRole('alert')).toContainText('Catalog item updated')
  expect(app.calls('PATCH', `merchant/catalog/${itemID}`)[0].body).toEqual({ name: 'Saved revised pears', description: 'Synthetic merchant description', unitPriceCents: 500, volumeLiters: 1.25, available: true })
  await name.fill('DO NOT PERSIST THIS DRAFT'); await price.fill('999')
  await button(page, 'Hide from customer catalogs').click()
  await expect(button(page, 'Make available to customers')).toBeVisible()
  expect(app.calls('PATCH', `merchant/catalog/${itemID}`)[1].body).toEqual({ name: 'Saved revised pears', description: 'Synthetic merchant description', unitPriceCents: 500, volumeLiters: 1.25, available: false })
  await expect(name).toHaveValue('DO NOT PERSIST THIS DRAFT')
  await button(page, 'Make available to customers').click()
  await expect(button(page, 'Hide from customer catalogs')).toBeVisible()
  expect(app.calls('PATCH', `merchant/catalog/${itemID}`)[2].body.available).toBe(true)
})

test('merchant unresolved catalog creation locks fields and location selection and retries unchanged', async ({ app }) => {
  merchantFixtures(app)
  const { page } = app
  let attempts = 0
  app.on('POST', 'merchant/catalog', entry => ++attempts === 1 ? result({ error: 'unavailable' }, 503) : result({ id: itemID, ...entry.body, volumeLiters: '1.25', available: true }, 201))
  await merchantDraft(app)
  await button(page, 'Add catalog item').click()
  await expect(page.getByRole('heading', { name: 'Catalog creation awaiting reconciliation', exact: true })).toBeVisible()
  for (const label of ['Item name', 'Description', 'Price (cents)', 'Volume (liters)']) await expect(field(page, label)).not.toBeEditable()
  await expect(button(page, 'Load my store locations')).toBeDisabled()
  await expect(page.getByRole('radio').first()).toBeDisabled()
  await button(page, 'Retry unchanged catalog item').click()
  await expect(button(page, 'Add catalog item')).toBeEnabled()
  const [first, retry] = app.calls('POST', 'merchant/catalog')
  expect(retry.raw).toBe(first.raw)
  expect(retry.headers['idempotency-key']).toBe(first.headers['idempotency-key'])
  await expect(field(page, 'Item name')).toBeEditable()
})

test('merchant billing refresh and native setup refresh readiness but subscription stays hidden until paid invoice', async ({ app }) => {
  const { page } = app
  let ready = false, started = false
  app.on('GET', 'merchant/subscription', () => result({ merchantId: uuid(7), status: started ? 'incomplete' : 'inactive', monthlyAmountCents: 19900, currency: 'usd', storefrontVisible: false, currentPeriodEnd: null, paymentFailedAt: null, paymentMethodReady: ready }))
  app.on('POST', 'merchant/subscription/setup-intent', () => result({ setupIntentId: 'seti_fixture', clientSecret: 'seti_fixture_secret_synthetic' }))
  app.on('POST', 'merchant/subscription', () => { started = true; return result({ subscriptionId: 'sub_fixture', status: 'incomplete', monthlyAmountCents: 19900, currency: 'usd', billingStartsOn: '2026-12-01' }, 202) })
  await connect(app, 'Merchant')
  await expect(button(page, 'Load billing status before subscribing')).toBeDisabled()
  await button(page, 'Refresh billing status').click()
  await expect(page.getByText('USD 199.00 / month', { exact: true })).toBeVisible()
  await expect(page.getByText('Storefront hidden · Payment method needed', { exact: true })).toBeVisible()
  await expect(button(page, 'Start subscription · USD 199.00 / month')).toBeDisabled()
  await button(page, 'Save payment method').click()
  await expect(button(page, 'Save payment method')).toBeDisabled()
  await expect(button(page, 'Refresh billing status')).toBeDisabled()
  expect(await page.evaluate(() => window.mobileAcceptance.stripe.stats())).toEqual({ initializations: [{ merchantDisplayName: 'ShopStops Merchant', setupIntentClientSecret: 'seti_fixture_secret_synthetic' }], presentations: 1 })
  ready = true
  await button(page, 'Simulate sheet success').click()
  await expect(page.getByText('Storefront hidden · Payment method saved', { exact: true })).toBeVisible()
  await expect(page.getByRole('alert')).toContainText('Billing setup submitted')
  await button(page, 'Start subscription · USD 199.00 / month').click()
  await expect(page.getByText('incomplete', { exact: true })).toBeVisible()
  await expect(page.getByText(/Saving a payment method does not activate the storefront; a successful invoice is required/)).toBeVisible()
  await expect(page.getByText('Storefront hidden · Payment method saved', { exact: true })).toBeVisible()
  expect(app.calls('GET', 'merchant/subscription')).toHaveLength(3)
  expect(app.calls('POST', 'merchant/subscription/setup-intent')[0].headers['idempotency-key']).toMatch(/^mobile-/)
  expect(app.calls('POST', 'merchant/subscription')).toHaveLength(1)
})

test('subscription confirmation survives the automatic billing refresh', async ({ app }) => {
  const { page } = app
  let started = false
  app.on('GET', 'merchant/subscription', () => result({ status: started ? 'incomplete' : 'inactive', monthlyAmountCents: 19900, currency: 'usd', storefrontVisible: false, currentPeriodEnd: null, paymentMethodReady: true }))
  app.on('POST', 'merchant/subscription', () => { started = true; return result({ status: 'incomplete', subscriptionId: 'sub_fixture' }, 202) })
  await connect(app, 'Merchant')
  await button(page, 'Refresh billing status').click()
  await button(page, 'Start subscription · USD 199.00 / month').click()
  await expect(page.getByText('incomplete', { exact: true })).toBeVisible()
  await expect(page.getByRole('alert')).toContainText('Storefront activation waits for a paid invoice')
})