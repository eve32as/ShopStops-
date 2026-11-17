export type ErrorKind = 'configuration' | 'timeout' | 'cancelled' | 'network' | 'unauthorized' | 'forbidden' | 'conflict' | 'rate_limit' | 'server' | 'response'
export class ApiError extends Error {
  status?: number
  kind: ErrorKind
  constructor(message: string, kind: ErrorKind = 'response', status?: number) {
    super(message)
    this.name = 'ApiError'; this.kind = kind; this.status = status
  }
}
export function gatewayURL(base: string, allowLocal = true): URL {
  let url: URL
  try { url = new URL(base.trim().replace(/\/+$/, '')) }
  catch { throw new ApiError('Enter a valid public HTTPS gateway URL.', 'configuration') }
  const host = url.hostname.toLowerCase().replace(/\.$/, '')
  const privateHost = !host.includes('.') || host === 'localhost' || /\.(localhost|local|internal)$/.test(host) ||
    /^(127\.|10\.|192\.168\.|169\.254\.|0\.)/.test(host) || /^172\.(1[6-9]|2\d|3[01])\./.test(host) || host.includes(':')
  const local = allowLocal && url.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]', '10.0.2.2'].includes(host)
  if ((!allowLocal && privateHost) || url.username || url.password || url.search || url.hash || (url.protocol !== 'https:' && !local)) {
    throw new ApiError('Use HTTPS without credentials, query or fragment. HTTP is for local development only.', 'configuration')
  }
  return url
}
export class RequestScope {
  controller = new AbortController()
  get signal() { return this.controller.signal }
  close() { this.controller.abort() }
  assertCurrent() {
    if (this.signal.aborted) throw new ApiError('Request cancelled. No automatic retry was made.', 'cancelled')
  }
}
export type RequestOptions = RequestInit & { timeoutMs?: number }
export async function requestJSON<T>(url: URL, token: string, options: RequestOptions = {}): Promise<T> {
  if (!token.trim() || /[\r\n]/.test(token)) throw new ApiError('Enter your signed-in user session token.', 'configuration')
  const { timeoutMs = 15000, signal, ...init } = options
  if (!Number.isFinite(timeoutMs) || timeoutMs < 1 || timeoutMs > 120000) throw new ApiError('Invalid request timeout.', 'configuration')
  const controller = new AbortController()
  let timer: ReturnType<typeof setTimeout> | undefined
  let onAbort: () => void = () => {}
  const interrupted = new Promise<never>((_, reject) => {
    onAbort = () => { controller.abort(); reject(new ApiError('Request cancelled. No automatic retry was made.', 'cancelled')) }
    if (signal?.aborted) { onAbort(); return }
    signal?.addEventListener('abort', onAbort, { once: true })
    timer = setTimeout(() => {
      controller.abort()
      reject(new ApiError('The gateway timed out. The outcome may be unknown; retry unchanged with the retained key or check the record first.', 'timeout'))
    }, timeoutMs)
  })
  const execute = async () => {
    if (signal?.aborted) throw new ApiError('Request cancelled.', 'cancelled')
    const headers = new Headers(init.headers)
    headers.set('Accept', 'application/json'); headers.set('Authorization', `Bearer ${token.trim()}`)
    if (init.body !== undefined) headers.set('Content-Type', 'application/json')
    const response = await fetch(url, { ...init, headers, signal: controller.signal, redirect: 'error', credentials: 'omit', cache: 'no-store' })
    if (response.redirected) throw new ApiError('Gateway redirects are not permitted.', 'response')
    if (!response.ok) {
      const status = response.status
      const [kind, message]: [ErrorKind, string] = status === 401
        ? ['unauthorized', 'Your session expired or was rejected. Clear the session and sign in again.']
        : status === 403 ? ['forbidden', 'Access denied. This role may lack permission, or this backend feature is disabled.']
        : status === 409 ? ['conflict', 'Request conflict. Reconcile the existing record; do not generate a new retry key to bypass it.']
        : status === 429 ? ['rate_limit', 'Too many requests. Wait before retrying manually; no request was retried automatically.']
        : status >= 500 ? ['server', 'The gateway service is unavailable. Try again manually later; a submitted change may still have completed.']
        : ['response', `The gateway rejected the request (${status}). Check the inputs and permissions.`]
      throw new ApiError(message, kind, status)
    }
    if (response.status === 204) return undefined as T
    try {
      const data: unknown = await response.json()
      if (data === null) throw new Error()
      return data as T
    } catch { throw new ApiError('The gateway returned an invalid response. Check the record before repeating a change.', 'response') }
  }
  try { return await Promise.race([interrupted, execute()]) }
  catch (error) {
    if (error instanceof ApiError) throw error
    throw new ApiError('Cannot reach the gateway. Check your connection and HTTPS settings; no automatic retry was made.', 'network')
  } finally { clearTimeout(timer); signal?.removeEventListener('abort', onAbort) }
}