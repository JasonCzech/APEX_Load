'use strict';

async function withDeadline(promise, timeoutMS, message, timers = globalThis) {
  let timer;
  try {
    return await Promise.race([
      promise,
      new Promise((_, reject) => {
        timer = timers.setTimeout(() => reject(new Error(message)), timeoutMS);
      })
    ]);
  } finally {
    if (timer !== undefined) timers.clearTimeout(timer);
  }
}

module.exports = { withDeadline };
