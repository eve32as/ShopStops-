import test from 'node:test'
import assert from 'node:assert/strict'
import { RequestSession } from '../src/requestSession.ts'

test('session has a synchronous duplicate-submit lock, released by finally', () => {
  const session = new RequestSession(); const scope = session.begin()
  assert.ok(scope); assert.equal(session.begin(), null)
  session.finish(scope); assert.equal(session.begin(), scope); session.close()
})
test('logout aborts old work, StrictMode reactivation is clean, old finally cannot unlock new work', () => {
  const session = new RequestSession(); const old = session.begin()
  session.close(); assert.equal(old.signal.aborted, true); assert.equal(session.begin(), null)
  session.activate(); const current = session.begin(); assert.notEqual(current, old)
  session.finish(old); assert.equal(session.begin(), null)
  session.finish(current); assert.equal(session.begin(), current); session.close()
})