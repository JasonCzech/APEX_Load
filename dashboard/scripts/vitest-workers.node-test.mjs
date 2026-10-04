import assert from 'node:assert/strict'
import test from 'node:test'
import { parseVitestWorkers } from './vitest-workers.mjs'

test('worker override retains default behavior and accepts positive safe integers', () => {
  assert.equal(parseVitestWorkers(undefined), undefined)
  assert.equal(parseVitestWorkers('1'), 1)
  assert.equal(parseVitestWorkers('2'), 2)
})
test('worker override rejects invalid, ambiguous, and unbounded values', () => {
  for (const value of ['', '0', '-1', '1.5', ' 2', '2 ', 'NaN', 'Infinity', '1e2', '9007199254740992']) {
    assert.throws(() => parseVitestWorkers(value), /positive safe integer/)
  }
})
