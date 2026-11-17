import test from 'node:test'
import assert from 'node:assert/strict'
import { workspaceFromHash, workspaces } from '../src/workspaces.ts'
import { recordState } from '../src/recordState.ts'

test('workspace hashes support direct links and safe fallback without accepting arbitrary routes', () => {
  for (const workspace of workspaces) assert.equal(workspaceFromHash(`#${workspace.id}`), workspace.id)
  for (const hash of ['', '#', '#unknown', '#finance-planning?token=secret', '#%73hopper-orders']) assert.equal(workspaceFromHash(hash), 'overview')
})
test('record UI distinguishes not loaded, loaded empty and populated pages', () => {
  assert.equal(recordState(false, '', false, 0).kind, 'idle')
  assert.equal(recordState(false, '', true, 0).kind, 'empty')
  assert.equal(recordState(false, '', true, 50).kind, 'ready')
  assert.match(recordState(false, '', true, 0).detail, /does not establish payment/)
})
test('busy and failed refreshes never present stale records as a successful empty result', () => {
  assert.equal(recordState(true, '', true, 0).kind, 'busy')
  assert.equal(recordState(true, '', true, 10).kind, 'busy')
  assert.match(recordState(true, '', true, 10).detail, /duplicate/)
  const failed = recordState(false, 'Gateway unavailable. Retry unchanged.', true, 0)
  assert.equal(failed.kind, 'error')
  assert.equal(failed.detail, 'Gateway unavailable. Retry unchanged.')
})