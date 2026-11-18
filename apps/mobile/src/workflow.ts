export const customerStages = ["Browse", "Cart", "Checkout", "Status"] as const;
export type CustomerStage = (typeof customerStages)[number];

export function cartSummary(
  items: { id: string; unitPriceCents: number }[],
  quantities: Record<string, number>,
) {
  let count = 0;
  let cents = 0n;
  for (const item of items) {
    const quantity = quantities[item.id] ?? 0;
    if (
      !Number.isSafeInteger(quantity) ||
      quantity < 0 ||
      quantity > 99 ||
      !Number.isSafeInteger(item.unitPriceCents) ||
      item.unitPriceCents < 0
    ) {
      throw new Error("Invalid catalog quantity or price.");
    }
    count += quantity;
    cents += BigInt(item.unitPriceCents) * BigInt(quantity);
  }
  return { count, cents };
}

export function money(cents: bigint | number, currency = "USD") {
  if (typeof cents === "number" && !Number.isSafeInteger(cents)) {
    throw new Error("Invalid integer cents.");
  }
  if (!/^[a-z]{3}$/i.test(currency)) throw new Error("Invalid currency.");
  const amount = BigInt(cents);
  const absolute = amount < 0n ? -amount : amount;
  return `${amount < 0n ? "-" : ""}${currency.toUpperCase()} ${absolute / 100n}.${String(absolute % 100n).padStart(2, "0")}`;
}

export function deliveryCoordinates(latitude: string, longitude: string) {
  const parse = (text: string, max: number, label: string) => {
    if (!/^-?\d+(?:\.\d+)?$/.test(text.trim())) {
      throw new Error(`Enter ${label} as decimal degrees.`);
    }
    const value = Number(text);
    if (!Number.isFinite(value) || Math.abs(value) > max) {
      throw new Error(`${label} must be between -${max} and ${max}.`);
    }
    return value;
  };
  return {
    latitude: parse(latitude, 90, "Latitude"),
    longitude: parse(longitude, 180, "Longitude"),
  };
}
