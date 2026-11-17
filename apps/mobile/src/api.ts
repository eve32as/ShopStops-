import { ApiError, gatewayURL, requestJSON, type RequestOptions } from './transport.ts';
export { ApiError, RequestScope } from './transport.ts';

export type ApiSettings = {
  baseUrl: string;
  sessionToken: string;
  signal?: AbortSignal;
  allowLocal?: boolean;
};

export async function apiRequest<T>(
  settings: ApiSettings,
  path: string,
  options: RequestOptions = {},
): Promise<T> {
  const base = gatewayURL(settings.baseUrl, settings.allowLocal ?? false);
  const url = new URL(`${base.href.replace(/\/+$/, '')}/${path.replace(/^\/+/, '')}`);
  if (url.origin !== base.origin || !url.pathname.startsWith(`${base.pathname.replace(/\/+$/, '')}/`)) {
    throw new ApiError('Invalid gateway request path.', 'configuration');
  }
  return requestJSON<T>(url, settings.sessionToken, { ...options, signal: settings.signal ?? options.signal });
}

export function idempotencyKey() {
  return `mobile-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}
