// A selectable simulated native sheet, not Stripe.js and not a payment certification.
import { useSyncExternalStore } from 'react';
const initializations: unknown[] = [];
let presentations = 0;
let pending: ((result: object) => void) | null = null;
let initResult: object = {};
const listeners = new Set<() => void>();
function notify() { listeners.forEach(listener => listener()); }
const stripe = {
  async initPaymentSheet(options: unknown) { initializations.push(options); return initResult; },
  presentPaymentSheet() {
    presentations++;
    return new Promise<object>(resolve => { pending = resolve; notify(); });
  },
};
export function useStripe() { return stripe; }
export const stripeBridge = {
  stats() { return { initializations, presentations }; },
  initializationResult(value: object) { initResult = value; },
  finish(outcome: 'success' | 'cancel' | 'error') {
    const resolve = pending; pending = null; notify();
    resolve?.(outcome === 'success' ? {} : { error: { code: outcome === 'cancel' ? 'Canceled' : 'Failed' } });
  },
};
export function useSimulatedSheet() {
  return useSyncExternalStore(listener => { listeners.add(listener); return () => { listeners.delete(listener); }; }, () => Boolean(pending));
}