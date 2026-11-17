import { gatewayURL } from './transport.ts';

export function publishableKeyError(key: string | undefined, development: boolean): string {
  if (!key && development) return '';
  if (!key || !/^pk_(test|live)_[A-Za-z0-9]+$/.test(key)) return 'Payment configuration missing or invalid: use only an EXPO_PUBLIC_STRIPE_PUBLISHABLE_KEY beginning pk_test_ or pk_live_. Never use a secret key. Rebuild after correcting configuration.';
  return '';
}

export function mobileConfiguration(raw: string | undefined, development: boolean, key?: string) {
  const keyError = publishableKeyError(key, development);
  if (keyError) return { baseUrl: '', error: keyError };
  if (!raw?.trim()) return development
    ? { baseUrl: '', error: '' }
    : { baseUrl: '', error: 'Release configuration missing: EXPO_PUBLIC_API_URL must be set to your approved public HTTPS gateway. Rebuild the app after configuring it.' };
  try { return { baseUrl: gatewayURL(raw, development).href.replace(/\/+$/, ''), error: '' }; }
  catch { return { baseUrl: '', error: 'Invalid gateway configuration. Releases require a public HTTPS URL without credentials, query or fragment.' }; }
}