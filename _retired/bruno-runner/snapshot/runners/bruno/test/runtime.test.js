'use strict';

const assert = require('node:assert/strict');
const test = require('node:test');
const { withDeadline } = require('../src/runtime');

test('withDeadline clears its timer when the operation completes', async () => {
  const cleared = [];
  const timers = {
    setTimeout() { return 42; },
    clearTimeout(timer) { cleared.push(timer); }
  };

  assert.equal(await withDeadline(Promise.resolve('ok'), 1000, 'expired', timers), 'ok');
  assert.deepEqual(cleared, [42]);
});

test('withDeadline rejects and clears its deadline timer', async () => {
  const cleared = [];
  const timers = {
    setTimeout(callback) {
      queueMicrotask(callback);
      return 7;
    },
    clearTimeout(timer) { cleared.push(timer); }
  };

  await assert.rejects(withDeadline(new Promise(() => {}), 1, 'expired', timers), /expired/);
  assert.deepEqual(cleared, [7]);
});
