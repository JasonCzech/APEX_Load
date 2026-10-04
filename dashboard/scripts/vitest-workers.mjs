// The default stays owned by Vitest/CI; an operator can bound local concurrency.
export function parseVitestWorkers(value) {
  if (value === undefined) return undefined
  if (!/^[1-9]\d*$/.test(value) || !Number.isSafeInteger(Number(value))) {
    throw new Error('APEXLOAD_VITEST_MAX_WORKERS must be a positive safe integer')
  }
  return Number(value)
}
