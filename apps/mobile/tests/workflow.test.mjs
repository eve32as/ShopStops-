import test from "node:test";
import assert from "node:assert/strict";
import { cartSummary, customerStages, deliveryCoordinates, money } from "../src/workflow.ts";

test("customer navigation presents the requested ordered stages", () => {
  assert.deepEqual(customerStages, ["Browse", "Cart", "Checkout", "Status"]);
});

test("cart totals use only selected catalog items and preserve exact cents", () => {
  const items = [
    { id: "milk", unitPriceCents: 137 },
    { id: "bread", unitPriceCents: 263 },
  ];
  assert.deepEqual(cartSummary(items, {}), { count: 0, cents: 0n });
  assert.deepEqual(cartSummary(items, { milk: 3, bread: 2, stale: 9 }), {
    count: 5,
    cents: 937n,
  });
  assert.equal(
    cartSummary([{ id: "item", unitPriceCents: Number.MAX_SAFE_INTEGER }], { item: 99 }).cents,
    891712726219358109n,
  );
  assert.equal(money(937), "USD 9.37");
  assert.equal(money(-199, "eur"), "-EUR 1.99");
});

test("cart rejects invalid prices and quantities instead of inventing totals", () => {
  for (const quantity of [-1, 0.5, 100, Number.NaN]) {
    assert.throws(() => cartSummary([{ id: "item", unitPriceCents: 100 }], { item: quantity }));
  }
  assert.throws(() => cartSummary([{ id: "item", unitPriceCents: 1.5 }], {}));
  assert.throws(() => money(Number.MAX_SAFE_INTEGER + 1));
});

test("delivery coordinates reject missing and malformed values but allow western longitudes", () => {
  assert.deepEqual(deliveryCoordinates("38.9072", "-77.0369"), {
    latitude: 38.9072,
    longitude: -77.0369,
  });
  for (const [latitude, longitude] of [
    ["", "0"],
    ["0", ""],
    ["91", "0"],
    ["0", "-181"],
    ["1e1", "0"],
    ["Infinity", "0"],
  ]) {
    assert.throws(() => deliveryCoordinates(latitude, longitude));
  }
});
