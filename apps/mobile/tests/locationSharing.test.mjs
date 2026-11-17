import test from 'node:test'
import assert from 'node:assert/strict'
import { LocationSharing } from '../src/locationSharing.ts'

const deferred = () => { let resolve; const promise = new Promise(done => { resolve = done }); return { promise, resolve } }
test('late permission/watch resolution after stop cannot restart GPS', async () => {
  const sharing = new LocationSharing(); const pending = deferred(); let removed = 0
  const start = sharing.start(async () => pending.promise)
  sharing.stop(); pending.resolve({ remove() { removed++ } })
  assert.equal(await start, false); assert.equal(removed, 1); assert.equal(sharing.active, false)
})
test('duplicate starts acquire one watcher and stop clears private positions', async () => {
  const sharing = new LocationSharing(); const pending = deferred(); let acquisitions = 0; let removed = 0
  const start = sharing.start(async (_, receive) => { acquisitions++; receive({ timestamp: 1 }); return pending.promise })
  assert.equal(await sharing.start(async () => { acquisitions++; return null }), false)
  pending.resolve({ remove() { removed++ } }); assert.equal(await start, true)
  sharing.stop(); sharing.stop(); let posted = false
  await sharing.report(async () => { posted = true })
  assert.equal(posted, false); assert.equal(acquisitions, 1); assert.equal(removed, 1)
})
test('location reports never overlap, retry failed positions, and suppress duplicate successes', async () => {
  const sharing = new LocationSharing(); let receive
  await sharing.start(async (_, next) => { receive = next; return { remove() {} } })
  receive({ timestamp: 1 }); const pending = deferred(); let calls = 0
  const first = sharing.report(async () => { calls++; await pending.promise })
  await sharing.report(async () => { calls++ }); assert.equal(calls, 1)
  pending.resolve(); await first
  await sharing.report(async () => { calls++ }); assert.equal(calls, 1)
  receive({ timestamp: 2 })
  await assert.rejects(sharing.report(async () => { throw new Error('offline') }))
  await sharing.report(async position => { calls++; assert.equal(position.timestamp, 2) })
  assert.equal(calls, 2); sharing.stop()
})
test('stop aborts pending posts and ignores old watcher callbacks after restart', async () => {
  const sharing = new LocationSharing(); let oldReceive; let signal; const pending = deferred()
  await sharing.start(async (_, receive) => { oldReceive = receive; receive({ timestamp: 1 }); return { remove() {} } })
  const posting = sharing.report(async (_, nextSignal) => { signal = nextSignal; await pending.promise })
  sharing.stop(); assert.equal(signal.aborted, true)
  await sharing.start(async (_, receive) => { receive({ timestamp: 2 }); return { remove() {} } })
  oldReceive({ timestamp: 999 }); pending.resolve(); await posting
  await sharing.report(async position => assert.equal(position.timestamp, 2)); sharing.stop()
})
test('permission failures release the start lock and can be retried explicitly', async () => {
  const sharing = new LocationSharing()
  await assert.rejects(sharing.start(async () => { throw new Error('permission service unavailable') }))
  assert.equal(await sharing.start(async () => null), false)
  assert.equal(await sharing.start(async () => ({ remove() {} })), true); sharing.stop()
})