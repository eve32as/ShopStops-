import test from 'node:test';
import assert from 'node:assert/strict';
import { cartSummary, defaultDeliveryWindow, definiteRejection, deliveryCoordinates, deliveryWindow, merchantCatalogValues, money, nextRouteStop, readableDate, requiredUUID, retainAttempt, routeCanProgress } from '../src/workflow.ts';

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

const now = Date.parse('2026-11-18T12:00:00Z');
const futureStart = '2026-11-18T12:15:00Z';
const futureEnd = '2026-11-18T14:15:00Z';

test('delivery windows normalize explicit timezone offsets and fractional seconds to UTC', () => {
  assert.deepEqual(deliveryWindow('2026-11-18T07:15:00-05:00', '2026-11-18T16:15:00+02:00', now), {
    start: '2026-11-18T12:15:00.000Z', end: '2026-11-18T14:15:00.000Z',
  });
  assert.equal(deliveryWindow('2026-11-18T12:15:00.123Z', futureEnd, now).start, '2026-11-18T12:15:00.123Z');
  assert.equal(deliveryWindow('2028-02-29T12:00:00Z', '2028-02-29T13:00:00Z', Date.parse('2028-02-29T11:00:00Z')).start, '2028-02-29T12:00:00.000Z');
});

test('delivery windows reject malformed timestamps, missing timezones and normalized invalid dates in either field', () => {
  for (const value of ['', 'not-a-date', '2026-11-18', '2026-11-18T12:15:00', '11/18/2026 12:15:00',
    '2026-11-18 12:15:00Z', '2026-11-18T12:15Z', '2026-11-18T12:15:00+0500',
    '2026-11-18T12:15:00Z\n', ' 2026-11-18T12:15:00Z', '2027-02-29T12:15:00Z',
    '2100-02-29T12:15:00Z', '2027-02-30T12:15:00Z', '2027-04-31T12:15:00Z',
    '2027-00-01T12:15:00Z', '2027-13-01T12:15:00Z', '2027-01-00T12:15:00Z',
    '2027-01-01T24:00:00Z', '2027-01-01T12:60:00Z', '2027-01-01T12:00:60Z',
    '2027-01-01T12:00:00+24:00', '2027-01-01T12:00:00+01:60']) {
    assert.throws(() => deliveryWindow(value, futureEnd, now), undefined, `start: ${value}`);
    assert.throws(() => deliveryWindow(futureStart, value, now), undefined, `end: ${value}`);
  }
});

test('delivery windows require a future start and strictly later end by instant, not local text', () => {
  for (const start of ['2026-11-18T11:59:59Z', '2026-11-18T12:00:00Z', '2026-11-18T13:00:00+01:00']) {
    assert.throws(() => deliveryWindow(start, futureEnd, now), /future/);
  }
  for (const end of [futureStart, '2026-11-18T12:14:59Z', '2026-11-18T13:15:00+01:00']) {
    assert.throws(() => deliveryWindow(futureStart, end, now), /after/);
  }
  assert.throws(() => deliveryWindow(futureStart, futureEnd, NaN), /future/);
});

test('default delivery windows start fifteen minutes ahead and last two hours across date boundaries', () => {
  for (const instant of [now, Date.parse('2028-02-28T23:55:00.123Z'), Date.parse('2026-12-31T23:55:00Z')]) {
    const window = defaultDeliveryWindow(instant);
    assert.equal(Date.parse(window.start), instant + 15 * 60 * 1000);
    assert.equal(Date.parse(window.end) - Date.parse(window.start), 2 * 60 * 60 * 1000);
    assert.deepEqual(deliveryWindow(window.start, window.end, instant), window);
  }
});

test('time helpers use Date.now when no clock is supplied', (t) => {
  t.mock.method(Date, 'now', () => now);
  assert.deepEqual(defaultDeliveryWindow(), { start: '2026-11-18T12:15:00.000Z', end: '2026-11-18T14:15:00.000Z' });
  assert.deepEqual(deliveryWindow(futureStart, futureEnd), defaultDeliveryWindow());
});

test('delivery window constraints match backend advance and duration limits', () => {
  const iso = offset => new Date(now + offset).toISOString();
  const day = 86400000;
  assert.throws(() => deliveryWindow(iso(31 * day), iso(31 * day + 1000), now), /30 days/);
  assert.throws(() => deliveryWindow(futureStart, iso(2 * day), now), /24 hours/);
  assert.doesNotThrow(() => deliveryWindow(iso(30 * day), iso(31 * day), now));
});

test('only definite rejection statuses release a failed mutation for correction', () => {
  for (const status of [400, 401, 403, 404, 422]) assert.equal(definiteRejection({ status }), true);
  for (const status of [undefined, 0, 408, 409, 429, 500, 502, 503, 504]) assert.equal(definiteRejection({ status }), false);
});

test('retry attempts retain the same object and key for equal JSON payloads', () => {
  let keys = 0;
  const createKey = () => `key-${++keys}`;
  const body = { items: [{ id: 'a', quantity: 2 }], dropoff: { latitude: 38 } };
  const attempt = retainAttempt(null, body, createKey);
  assert.equal(attempt.key, 'key-1');
  assert.equal(attempt.fingerprint, JSON.stringify(body));
  assert.deepEqual(attempt.body, body);
  assert.equal(retainAttempt(attempt, { items: [{ id: 'a', quantity: 2 }], dropoff: { latitude: 38 } }, createKey), attempt);
  assert.equal(keys, 1);
  assert.equal(retainAttempt(null, body, createKey).key, 'key-2');
});

test('retry attempts snapshot nested objects and arrays without mutating the caller', () => {
  const body = { items: [{ quantity: 2 }], tags: ['first'], dropoff: { latitude: 38 } };
  const attempt = retainAttempt(null, body, () => 'stable-key');
  assert.notEqual(attempt.body, body);
  assert.notEqual(attempt.body.items, body.items);
  assert.notEqual(attempt.body.items[0], body.items[0]);
  body.items[0].quantity = 9;
  body.tags.push('second');
  body.dropoff.latitude = 0;
  assert.deepEqual(attempt.body, { items: [{ quantity: 2 }], tags: ['first'], dropoff: { latitude: 38 } });
  assert.equal(attempt.fingerprint, JSON.stringify(attempt.body));
  assert.throws(() => retainAttempt(attempt, body, () => assert.fail('must not create another key')), /reconcile.*refresh.*status/i);
});

test('changed retry payloads require reconciliation rather than silently rotating keys', () => {
  const attempt = retainAttempt(null, { priorityService: false, orderIds: ['a'] }, () => 'original');
  for (const body of [{ priorityService: true, orderIds: ['a'] }, { priorityService: false, orderIds: ['b'] }]) {
    assert.throws(() => retainAttempt(attempt, body, () => assert.fail('must not create another key')), /previous attempt/i);
  }
  assert.equal(attempt.key, 'original');
  assert.deepEqual(attempt.body, { priorityService: false, orderIds: ['a'] });
});

const catalogDraft = { name: ' Soup ', description: ' Daily special ', unitPriceCents: ' 650 ', volumeLiters: ' 0.50 ' };

test('catalog values normalize names and numeric input while preserving description text and the draft', () => {
  assert.deepEqual(merchantCatalogValues(catalogDraft), { name: 'Soup', description: ' Daily special ', unitPriceCents: 650, volumeLiters: 0.5 });
  assert.equal(catalogDraft.name, ' Soup ');
  assert.equal(merchantCatalogValues({ ...catalogDraft, description: '' }).description, '');
  assert.equal(merchantCatalogValues({ ...catalogDraft, volumeLiters: '1.2300' }).volumeLiters, 1.23);
});

test('catalog text limits match backend UTF-8 bytes, including multibyte names and descriptions', () => {
  for (const name of ['a'.repeat(200), 'é'.repeat(100), '🍎'.repeat(50)]) {
    assert.equal(merchantCatalogValues({ ...catalogDraft, name: ` ${name} ` }).name, name);
  }
  for (const name of ['', ' \t\n ', 'a'.repeat(201), 'é'.repeat(101), '🍎'.repeat(51)]) {
    assert.throws(() => merchantCatalogValues({ ...catalogDraft, name }), /name/);
  }
  for (const description of ['a'.repeat(1000), 'é'.repeat(500), '🍎'.repeat(250)]) {
    assert.equal(merchantCatalogValues({ ...catalogDraft, description }).description, description);
  }
  for (const description of ['a'.repeat(1001), 'é'.repeat(501), '🍎'.repeat(251), ' '.repeat(1001)]) {
    assert.throws(() => merchantCatalogValues({ ...catalogDraft, description }), /Description/);
  }
});

test('catalog prices require positive safe decimal integers within the backend cap', () => {
  for (const value of ['1', '10000000', '000650']) {
    assert.equal(merchantCatalogValues({ ...catalogDraft, unitPriceCents: value }).unitPriceCents, Number(value));
  }
  for (const value of ['', ' ', '0', '-1', '+1', '1.5', '650.0', '1e3', '0x10', 'NaN', 'Infinity', '1,000', '10000001', '9007199254740992']) {
    assert.throws(() => merchantCatalogValues({ ...catalogDraft, unitPriceCents: value }), /price/);
  }
});

test('catalog volume requires positive decimals within backend range and exact hundredths', () => {
  for (const value of ['0.01', '0.29', '1', '100000', '100000.00', '0001.20']) {
    assert.equal(merchantCatalogValues({ ...catalogDraft, volumeLiters: value }).volumeLiters, Number(value));
  }
  for (const value of ['', ' ', '0', '0.00', '-1', '+1', '1e2', '0x10', 'NaN', 'Infinity', '1,2', '.5', '1.', '0.001', '1.231', '1.230000000000000001', '100000.01', '100001']) {
    assert.throws(() => merchantCatalogValues({ ...catalogDraft, volumeLiters: value }), /volume/);
  }
});

test('next route stop sorts a copy, skips completed stops, and preserves typed stop identity', () => {
  const last = { sequence: 3, status: 'pending', id: 'last' };
  const next = { sequence: 2, status: 'arrived', id: 'next' };
  const done = { sequence: 1, status: 'completed', id: 'done' };
  const stops = Object.freeze([last, next, done]);
  assert.equal(nextRouteStop(stops), next);
  assert.deepEqual(stops, [last, next, done]);
  assert.equal(nextRouteStop([]), undefined);
  assert.equal(nextRouteStop([done]), undefined);
});

test('routes progress only in progress with a pending or arrived next stop', () => {
  for (const status of ['pending', 'arrived']) {
    assert.equal(routeCanProgress({ status: 'in_progress', stops: [{ sequence: 2, status }, { sequence: 1, status: 'completed' }] }), true);
  }
  assert.equal(routeCanProgress(null), false);
  for (const status of ['pending', 'assigned', 'completed', 'cancelled', '']) {
    assert.equal(routeCanProgress({ status, stops: [{ sequence: 1, status: 'pending' }] }), false);
  }
  assert.equal(routeCanProgress({ status: 'in_progress', stops: [] }), false);
  assert.equal(routeCanProgress({ status: 'in_progress', stops: [{ sequence: 1, status: 'completed' }] }), false);
  for (const status of ['blocked', 'cancelled', 'unknown']) {
    assert.equal(routeCanProgress({ status: 'in_progress', stops: [{ sequence: 2, status: 'pending' }, { sequence: 1, status }] }), false);
  }
});

test('route completion advances through out-of-order stops and disables further progress', () => {
  const route = { status: 'in_progress', stops: [{ sequence: 2, status: 'pending' }, { sequence: 1, status: 'arrived' }] };
  assert.equal(routeCanProgress(route), true);
  nextRouteStop(route.stops).status = 'completed';
  assert.equal(nextRouteStop(route.stops).sequence, 2);
  assert.equal(routeCanProgress(route), true);
  nextRouteStop(route.stops).status = 'completed';
  assert.equal(nextRouteStop(route.stops), undefined);
  assert.equal(routeCanProgress(route), false);
  route.status = 'completed';
  assert.equal(routeCanProgress(route), false);
});