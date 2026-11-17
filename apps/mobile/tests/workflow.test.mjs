import test from 'node:test';
import assert from 'node:assert/strict';
import { cartSummary, deliveryCoordinates, money, readableDate, requiredUUID } from '../src/workflow.ts';

test('money keeps exact cents, currency and large integer totals', () => {
  assert.equal(money(1), 'USD 0.01');
  assert.equal(money(-199, 'eur'), '-EUR 1.99');
  assert.equal(money('900719925474099301'), 'USD 9007199254740993.01');
  assert.throws(() => money(1.5));
  assert.throws(() => money(Number.MAX_SAFE_INTEGER + 1));
  assert.throws(() => money(100, 'unknown'));
});
test('cart totals only actual catalog selections, with an explicit empty state', () => {
  const items = [{ id: 'a', unitPriceCents: 137 }, { id: 'b', unitPriceCents: 263 }];
  assert.deepEqual(cartSummary(items, {}), { count: 0, cents: 0n });
  assert.deepEqual(cartSummary(items, { a: 3, b: 2, staleStoreItem: 99 }), { count: 5, cents: 937n });
  assert.deepEqual(cartSummary([], { a: 2 }), { count: 0, cents: 0n });
  assert.equal(cartSummary([{ id: 'a', unitPriceCents: Number.MAX_SAFE_INTEGER }], { a: 99 }).cents, 891712726219358109n);
});
test('cart rejects corrupt quantities or fractional backend prices rather than inventing totals', () => {
  for (const quantity of [-1, 0.5, 100, NaN]) assert.throws(() => cartSummary([{ id: 'a', unitPriceCents: 100 }], { a: quantity }));
  assert.throws(() => cartSummary([{ id: 'a', unitPriceCents: 1.2 }], {}));
});
test('coordinates allow negative longitude and reject blanks, exponent notation and out-of-range values', () => {
  assert.deepEqual(deliveryCoordinates('38.9072', '-77.0369'), { latitude: 38.9072, longitude: -77.0369 });
  assert.deepEqual(deliveryCoordinates('0', '0'), { latitude: 0, longitude: 0 });
  for (const [lat, lon] of [['', '0'], ['0', ''], ['91', '0'], ['0', '-181'], ['1e1', '0'], ['Infinity', '0'], ['0x10', '0']]) assert.throws(() => deliveryCoordinates(lat, lon));
});
test('required IDs are UUIDs, never empty or arbitrary route fragments', () => {
  const uuid = '11111111-1111-4111-8111-111111111111';
  assert.equal(requiredUUID(` ${uuid} `, 'store'), uuid);
  for (const input of ['', '../orders', 'not-an-id']) assert.throws(() => requiredUUID(input, 'assigned route'), /assigned route UUID/);
});
test('invalid backend dates show an honest fallback rather than Invalid Date', () => {
  assert.equal(readableDate('not-a-date'), 'Date unavailable — refresh status');
  assert.notEqual(readableDate('2026-11-17T12:00:00Z'), 'Date unavailable — refresh status');
});

test('real cart helper follows selection, edit, removal and store-change states without retaining stale totals', () => {
  const catalog = [{ id: 'a', unitPriceCents: 501 }, { id: 'b', unitPriceCents: 99 }];
  const quantities = {};
  assert.deepEqual(cartSummary(catalog, quantities), { count: 0, cents: 0n });
  quantities.a = 1;
  assert.deepEqual(cartSummary(catalog, quantities), { count: 1, cents: 501n });
  quantities.b = 2;
  assert.deepEqual(cartSummary(catalog, quantities), { count: 3, cents: 699n });
  quantities.a = 0;
  assert.deepEqual(cartSummary(catalog, quantities), { count: 2, cents: 198n });
  assert.deepEqual(cartSummary([{ id: 'new_store_item', unitPriceCents: 100 }], quantities), { count: 0, cents: 0n });
  assert.deepEqual(cartSummary(catalog, {}), { count: 0, cents: 0n });
});

test('delivery form helpers reject malformed decimal and UUID boundaries before a request can be built', () => {
  for (const value of ['NaN', '1,5', '--1', '1.2.3', ' ', '+1']) {
    assert.throws(() => deliveryCoordinates(value, '0'));
    assert.throws(() => deliveryCoordinates('0', value));
  }
  assert.deepEqual(deliveryCoordinates(' -90 ', ' 180 '), { latitude: -90, longitude: 180 });
  for (const value of ['11111111-1111-0111-8111-111111111111', '11111111-1111-4111-0111-111111111111']) assert.throws(() => requiredUUID(value, 'store'));
});