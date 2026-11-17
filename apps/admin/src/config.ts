import { gatewayURL } from './transport.ts'

export function adminConfiguration(raw: string | undefined, development: boolean) {
  // Manual gateway entry remains supported; no invented login or credentials.
  if (!raw?.trim()) return { baseUrl: '', error: '' }
  try { return { baseUrl: gatewayURL(raw, development).href.replace(/\/+$/, ''), error: '' } }
  catch { return { baseUrl: '', error: 'Invalid VITE_API_URL. Configure an approved public HTTPS gateway without credentials, query or fragment and rebuild.' } }
}