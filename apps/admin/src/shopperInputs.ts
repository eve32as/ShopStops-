import { parseMoney } from './financeInputs.ts'
import { gatewayURL, requestJSON } from './transport.ts'

export const shopperUUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
export function shopperRef(text: string): string {
  if (!/^[A-Za-z0-9_-]{1,100}$/.test(text)) throw new Error('Use an opaque reference (1–100 letters, digits, underscore or hyphen). No names, URLs or raw personal data.')
  return text
}
export function shopperAmount(text: string): number {
  const cents = parseMoney(text)
  if (cents < 1) throw new Error('Enter the explicitly approved positive completion amount; there is no default rate.')
  return cents
}
export function shopperURL(base: string, taskId?: string, allowLocal = true): URL {
  const url = gatewayURL(base, allowLocal)
  if (taskId && !shopperUUID.test(taskId)) throw new Error('Select a valid task UUID.')
  url.pathname = `${url.pathname.replace(/\/+$/, '')}/admin/shopper/tasks${taskId ? `/${taskId}/complete` : ''}`
  return url
}
export async function shopperRequest(base: string, token: string, method: 'GET' | 'POST', body?: object, key?: string, taskId?: string, offset = 0, signal?: AbortSignal, allowLocal = true): Promise<unknown> {
  if (!token.trim()) throw new Error('Add the in-memory admin session token above.')
  const url = shopperURL(base, taskId, allowLocal)
  if (method === 'GET') {
    if (!Number.isInteger(offset) || offset < 0 || offset > 100000) throw new Error('Invalid task page.')
    url.searchParams.set('limit', '50'); url.searchParams.set('offset', String(offset))
  } else if (!key || !shopperUUID.test(key)) throw new Error('A UUID retry key is required.')
  return requestJSON(url, token, {
    method, redirect: 'error', signal,
    headers: { Accept: 'application/json', Authorization: `Bearer ${token.trim()}`,
      ...(method === 'POST' ? { 'Content-Type': 'application/json', 'Idempotency-Key': key! } : {}) },
    ...(method === 'POST' ? { body: JSON.stringify(body) } : {}),
  })
}