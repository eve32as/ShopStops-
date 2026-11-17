import { pathToFileURL } from 'node:url'
import { gatewayURL } from '../apps/mobile/src/transport.ts'
import { mobileConfiguration } from '../apps/mobile/src/config.ts'

export function validateRelease(target, env) {
  const errors = []
  if (!['admin', 'mobile'].includes(target)) return ['Choose admin or mobile.']
  // Inspect public values without ever echoing them, even on failure.
  for (const [name, value] of Object.entries(env)) {
    if (/^(VITE_|EXPO_PUBLIC_)/.test(name) && /(?:sk_(?:test|live)_|rk_(?:test|live)_|whsec_)/i.test(value ?? '')) {
      errors.push('Secret-like content detected in public frontend configuration. Remove it and rotate any exposed credential.')
    }
  }
  const raw = env[target === 'mobile' ? 'EXPO_PUBLIC_API_URL' : 'VITE_API_URL']
  try {
    if (!raw?.trim()) throw new Error()
    const url = gatewayURL(raw, false)
    if (/(^|\.)(example\.(com|org|net)|example|test|invalid)$/.test(url.hostname)) throw new Error()
  } catch { errors.push('Release requires a real public HTTPS gateway URL; local, private and placeholder addresses are not allowed.') }
  if (target === 'mobile') {
    const config = mobileConfiguration(raw, false, env.EXPO_PUBLIC_STRIPE_PUBLISHABLE_KEY)
    if (config.error) errors.push(config.error)
  }
  return [...new Set(errors)]
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const errors = validateRelease(process.argv[2], process.env)
  if (errors.length) { for (const error of errors) console.error(error); process.exitCode = 1 }
  else console.log('Frontend public release configuration validated. This is not a backend readiness or live-payment approval.')
}