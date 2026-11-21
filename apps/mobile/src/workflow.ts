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

export function deliveryWindow(start: string, end: string, now = Date.now()) {
  const parse = (value: string) => {
    const parts = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(Z|[+-]\d{2}:\d{2})$/.exec(value);
    if (!parts || parts[0] !== value) throw new Error('Enter delivery timestamps in ISO format with an explicit timezone.');
    const [, yearText, monthText, dayText, hourText, minuteText, secondText, zone] = parts;
    const [year, month, day, hour, minute, second] = [yearText, monthText, dayText, hourText, minuteText, secondText].map(Number);
    const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
    const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
    const timestamp = Date.parse(value);
    if (month < 1 || month > 12 || day < 1 || day > days[month - 1] ||
        hour > 23 || minute > 59 || second > 59 ||
        (zone !== 'Z' && (Number(zone.slice(1, 3)) > 23 || Number(zone.slice(4)) > 59)) ||
        !Number.isFinite(timestamp)) throw new Error('Enter a valid calendar date, time, and timezone for delivery.');
    return timestamp;
  };
  const startTime = parse(start);
  const endTime = parse(end);
  if (!Number.isFinite(now) || startTime <= now) throw new Error('Delivery must start in the future.');
  if (endTime <= startTime) throw new Error('Delivery must end after it starts.');
  if (startTime > now + 30 * 24 * 60 * 60 * 1000) throw new Error('Delivery must start within 30 days.');
  if (endTime - startTime > 24 * 60 * 60 * 1000) throw new Error('Delivery windows cannot exceed 24 hours.');
  return { start: new Date(startTime).toISOString(), end: new Date(endTime).toISOString() };
}

export function definiteRejection(error: { status?: number }): boolean {
  // Conflicts, rate limits, server errors and transport failures require unchanged retries/reconciliation.
  return [400, 401, 403, 404, 422].includes(error.status ?? 0);
}

export function defaultDeliveryWindow(now = Date.now()) {
  const start = now + 15 * 60 * 1000;
  return { start: new Date(start).toISOString(), end: new Date(start + 2 * 60 * 60 * 1000).toISOString() };
}

// Request bodies are JSON data. Clone recursively so retries retain nested values
// even on React Native runtimes without structuredClone.
function snapshot<T>(value: T): T {
  if (value instanceof Date) return new Date(value.getTime()) as T;
  if (Array.isArray(value)) return value.map((entry) => snapshot(entry)) as T;
  if (value !== null && typeof value === 'object') {
    return Object.fromEntries(Object.entries(value).map(([key, entry]) => [key, snapshot(entry)])) as T;
  }
  return value;
}

export function retainAttempt<T>(previous: { key: string; fingerprint: string; body: T } | null, body: T, createKey: () => string) {
  const fingerprint = JSON.stringify(body);
  if (fingerprint === undefined) throw new Error('Enter a valid request before trying again.');
  if (previous) {
    if (previous.fingerprint !== fingerprint) {
      throw new Error('Reconcile the previous attempt: refresh its status before changing details or starting a new request.');
    }
    return previous;
  }
  const savedBody = snapshot(body);
  return { key: createKey(), fingerprint, body: savedBody };
}

export function merchantCatalogValues(draft: { name: string; description: string; unitPriceCents: string; volumeLiters: string }) {
  // Go's HTTP validator uses UTF-8 byte lengths, not JavaScript UTF-16 lengths.
  const byteLength = (text: string) => Array.from(text).reduce((length, character) => {
    const point = character.codePointAt(0)!;
    return length + (point <= 0x7f ? 1 : point <= 0x7ff ? 2 : point <= 0xffff ? 3 : 4);
  }, 0);
  const name = draft.name.trim();
  const description = draft.description;
  if (!name || byteLength(name) > 200) throw new Error('Enter an item name of at most 200 UTF-8 bytes.');
  if (byteLength(description) > 1000) throw new Error('Description must be at most 1000 UTF-8 bytes.');
  const priceText = draft.unitPriceCents.trim();
  const unitPriceCents = Number(priceText);
  if (!/^\d+$/.test(priceText) || !Number.isSafeInteger(unitPriceCents) || unitPriceCents < 1 || unitPriceCents > 10_000_000) {
    throw new Error('Enter a whole-number price between 1 and 10000000 cents.');
  }
  const volumeText = draft.volumeLiters.trim();
  const volumeLiters = Number(volumeText);
  // Extra trailing zeros are exact hundredths too; never round entered values.
  if (!/^\d+(?:\.\d+)?$/.test(volumeText) || !Number.isFinite(volumeLiters) || volumeLiters <= 0 || volumeLiters > 100_000 ||
      /[1-9]/.test((volumeText.split('.')[1] ?? '').slice(2))) {
    throw new Error('Enter a positive decimal volume up to 100000 liters with at most two decimal places.');
  }
  return { name, description, unitPriceCents, volumeLiters };
}

export function nextRouteStop<T extends { sequence: number; status: string }>(stops: T[]): T | undefined {
  return [...stops].sort((a, b) => a.sequence - b.sequence).find((stop) => stop.status !== 'completed');
}

export function routeCanProgress(route: { status: string; stops: { sequence: number; status: string }[] } | null): boolean {
  if (route?.status !== 'in_progress') return false;
  const next = nextRouteStop(route.stops);
  return next?.status === 'pending' || next?.status === 'arrived';
}