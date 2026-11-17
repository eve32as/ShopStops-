import assert from "node:assert/strict";
import test from "node:test";
import { priorityPricing, isTestPublishableKey } from "../src/priority.ts";

const fixture = { available: true, testOnly: true, currency: "usd", publicCheckoutAvailable: false, shoppingCents: 137, deliveryCents: 263, totalCents: 400 };
test("only exact server-sourced test prices enable selection", () => {
  assert.deepEqual(priorityPricing(fixture), fixture);
  for (const value of [null, {}, { ...fixture, available: false }, { ...fixture, testOnly: false },
    { ...fixture, totalCents: 399 }, { ...fixture, shoppingCents: 0 }, { ...fixture, shoppingCents: -1 },
    { ...fixture, shoppingCents: 1.5 }, { ...fixture, deliveryCents: 1000001 }, { ...fixture, currency: "eur" },
    { ...fixture, shoppingCents: "137" }, { ...fixture, publicCheckoutAvailable: undefined }]) {
    assert.equal(priorityPricing(value), null);
  }
});
test("priority payment requires a test publishable key", () => {
  assert.equal(isTestPublishableKey("pk_test_fixture"), true);
  for (const key of [undefined, "", "pk_live_fixture", "pk_test_", " pk_test_fixture", "sk_test_fixture"]) assert.equal(isTestPublishableKey(key), false);
});