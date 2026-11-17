export type PriorityPricing = {
  available: true;
  testOnly: true;
  currency: "usd";
  publicCheckoutAvailable: boolean;
  shoppingCents: number;
  deliveryCents: number;
  totalCents: number;
};

// Treat missing/invalid/unavailable prices as unavailable, never as free.
export function priorityPricing(value: unknown): PriorityPricing | null {
  if (!value || typeof value !== "object") return null;
  const p = value as Record<string, unknown>;
  const validRate = (n: unknown): n is number =>
    typeof n === "number" && Number.isSafeInteger(n) && n > 0 && n <= 1000000;
  if (p.available !== true || p.testOnly !== true || p.currency !== "usd" ||
      typeof p.publicCheckoutAvailable !== "boolean" ||
      !validRate(p.shoppingCents) || !validRate(p.deliveryCents) ||
      p.totalCents !== p.shoppingCents + p.deliveryCents) return null;
  return p as PriorityPricing;
}

export function isTestPublishableKey(key: string | undefined): boolean {
  return /^pk_test_[A-Za-z0-9]+$/.test(key ?? "");
}