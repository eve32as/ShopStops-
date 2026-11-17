export const customerStages = ['Browse', 'Cart', 'Checkout', 'Status'] as const;
export type CustomerStage = typeof customerStages[number];

export function money(cents: number | string | bigint, currency = 'USD'): string {
  if (typeof cents === 'number' && !Number.isSafeInteger(cents)) throw new Error('Invalid integer cents.');
  if (!/^[a-z]{3}$/i.test(currency)) throw new Error('Invalid currency.');
  const value = BigInt(cents);
  const absolute = value < 0n ? -value : value;
  return `${value < 0n ? '-' : ''}${currency.toUpperCase()} ${absolute / 100n}.${String(absolute % 100n).padStart(2, '0')}`;
}

export function cartSummary(items: { id: string; unitPriceCents: number }[], quantities: Record<string, number>) {
  let count = 0;
  let cents = 0n;
  for (const item of items) {
    const quantity = quantities[item.id] ?? 0;
    if (!Number.isSafeInteger(quantity) || quantity < 0 || quantity > 99 || !Number.isSafeInteger(item.unitPriceCents) || item.unitPriceCents < 0) throw new Error('Invalid catalog quantity or price.');
    count += quantity;
    cents += BigInt(item.unitPriceCents) * BigInt(quantity);
  }
  return { count, cents };
}

export function requiredUUID(text: string, label: string): string {
  const value = text.trim();
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(value)) throw new Error(`Enter a valid ${label} UUID.`);
  return value;
}

export function deliveryCoordinates(latitude: string, longitude: string) {
  const parse = (text: string, max: number, label: string) => {
    if (!/^-?\d+(?:\.\d+)?$/.test(text.trim())) throw new Error(`Enter ${label} as decimal degrees, including a minus sign when needed.`);
    const value = Number(text);
    if (!Number.isFinite(value) || Math.abs(value) > max) throw new Error(`${label} must be between -${max} and ${max}.`);
    return value;
  };
  return { latitude: parse(latitude, 90, 'Latitude'), longitude: parse(longitude, 180, 'Longitude') };
}

export function readableDate(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? 'Date unavailable — refresh status' : date.toLocaleString();
}