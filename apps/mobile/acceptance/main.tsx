import { createRoot } from 'react-dom/client';
import HomeScreen from '../app/index';
import { NativeAlerts, nativeBridge } from './native';
import { locationBridge } from './location';
import { stripeBridge, useSimulatedSheet } from './stripe';

// Test controls only expose native side effects, never application state or handlers.
Object.assign(window, { mobileAcceptance: { native: nativeBridge, location: locationBridge, stripe: stripeBridge } });
function SimulatedSheet() {
  const visible = useSimulatedSheet();
  return visible ? <div role="dialog" aria-modal="true" aria-label="Simulated native payment sheet">
    <h2>Simulated native payment sheet</h2>
    <p>Test adapter only. No card entry, Stripe network, or actual payment.</p>
    <button onClick={() => stripeBridge.finish('success')}>Simulate sheet success</button>
    <button onClick={() => stripeBridge.finish('cancel')}>Cancel simulated sheet</button>
    <button onClick={() => stripeBridge.finish('error')}>Simulate uncertain sheet error</button>
  </div> : null;
}
createRoot(document.getElementById('root')!).render(<><main><HomeScreen /></main><NativeAlerts /><SimulatedSheet /></>);