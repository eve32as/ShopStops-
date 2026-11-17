import { test as base, expect } from '@playwright/test'
import { gateway, syntheticToken } from './fixtures.mjs'

export { expect }
export const test = base.extend({
  api: async ({ context, baseURL }, provideFixture) => {
    const calls = []
    const unexpected = []
    const handlers = new Map()
    await context.route('**/*', async route => {
      const request = route.request()
      const url = new URL(request.url())
      if (url.origin === new URL(gateway).origin) {
        const headers = { 'access-control-allow-origin': baseURL, 'access-control-allow-headers': '*', 'access-control-allow-methods': 'GET, POST, OPTIONS' }
        if (request.method() === 'OPTIONS') return route.fulfill({ status: 204, headers })
        const key = `${request.method()} ${url.pathname}`
        const handler = handlers.get(key)
        if (handler) {
          expect(request.headers().authorization).toBe(`Bearer ${syntheticToken}`)
          const call = {
            method: request.method(), path: url.pathname, query: url.search,
            body: request.postDataJSON(), key: request.headers()['idempotency-key'],
          }
          calls.push(call)
          return handler({ route, request, call, reply: (json, status = 200) => route.fulfill({ status, headers, json }) })
        }
      } else if (url.origin === baseURL && request.method() === 'GET' &&
        (url.pathname === '/' || url.pathname.startsWith('/assets/') || ['/shopstops-mark.svg', '/favicon.ico'].includes(url.pathname))) {
        return route.continue()
      }
      // All API requests must have an explicit fixture. Never fall through to
      // DNS, a real gateway, payment provider, telemetry or another local port.
      unexpected.push(`${request.method()} ${url.origin}${url.pathname}`)
      return route.abort('blockedbyclient')
    })
    await provideFixture({ calls, on: (method, path, handler) => handlers.set(`${method} /v1${path}`, handler) })
    expect(unexpected, 'All non-static requests must be explicitly simulated').toEqual([])
  },
})

export async function connect(page) {
  await page.getByText(/^Operator connection · Setup required$/).click()
  await page.getByRole('textbox', { name: 'Operator connection · public gateway URL' }).fill(gateway)
  await page.getByLabel('Admin session token', { exact: true }).fill(syntheticToken)
}

export function navigation(page) {
  return page.getByRole('navigation', { name: 'Admin workspaces', exact: true })
}