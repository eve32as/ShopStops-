import { gatewayURL } from './transport.ts'

// Money parsing is decimal text -> integer cents, never floating-point dollars.
export function parseMoney(text: string): number {
  if (!/^\d{1,7}(?:\.\d{1,2})?$/.test(text)) throw new Error('Use nonnegative dollars with at most two decimals (no commas or exponent).')
  const [whole, fraction = ''] = text.split('.')
  const cents = BigInt(whole) * 100n + BigInt(fraction.padEnd(2, '0'))
  if (cents > 100000000n) throw new Error('Money inputs must not exceed $1,000,000.')
  return Number(cents)
}
export function parseCount(text: string, minimum = 0, maximum = 100000): number {
  if (!/^\d{1,6}$/.test(text)) throw new Error('Use a whole nonnegative number.')
  const value = Number(text)
  if (value < minimum || value > maximum) throw new Error(`Value must be ${minimum}..${maximum}.`)
  return value
}
// Decimal miles -> thousandths, never floating-point multiplication.
export function parsePickupRadius(text: string): number {
  if (!/^0(?:\.\d{1,3})?$/.test(text)) throw new Error('Use 0..0.500 miles with at most three decimals (include leading zero).')
  const [, fraction = ''] = text.split('.')
  const milliMiles = Number(fraction.padEnd(3, '0'))
  if (milliMiles > 500) throw new Error('Pickup planning radius must not exceed 0.500 miles.')
  return milliMiles
}
export function parseScenarioInput(key: string, text: string, money: boolean): number {
  if (key === 'pickupRadiusMilliMiles') return parsePickupRadius(text)
  if (money) return parseMoney(text)
  if (key === 'dropsPerBundle') return parseCount(text, 1, 100)
  if (key === 'additionalPickupsPerBundle' || key === 'additionalPickupsPerCheckout') return parseCount(text, 0, 100)
  if (key === 'fastServiceAdoptionBasisPoints') return parseCount(text, 0, 10000)
  return parseCount(text)
}
export function dollars(cents: number | string): string {
  if (typeof cents === 'number' && !Number.isSafeInteger(cents)) throw new Error('Invalid integer cents response.')
  const value = BigInt(cents)
  const absolute = value < 0n ? -value : value
  return `${value < 0n ? '-' : ''}$${absolute / 100n}.${(absolute % 100n).toString().padStart(2, '0')}`
}
export function financeURL(base: string, path: string, allowLocal = true): URL {
  const url = gatewayURL(base, allowLocal)
  if (!['costs', 'scenarios'].includes(path)) throw new Error('Invalid finance operation.')
  url.pathname = `${url.pathname.replace(/\/+$/, '')}/admin/finance/${path}`
  return url
}