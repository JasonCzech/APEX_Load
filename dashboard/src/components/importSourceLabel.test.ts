import { describe, expect, it } from 'vitest'

import { importSourceLabel } from './importSourceLabel'

describe('importSourceLabel', () => {
  it('turns a source file name into a folder name', () => {
    expect(importSourceLabel('checkout_plan.jmx')).toBe('checkout plan')
    expect(importSourceLabel('payments-api.openapi.yaml')).toBe('payments api')
    expect(importSourceLabel('Orders.postman_collection.json')).toBe('Orders')
    expect(importSourceLabel('Login.USZ')).toBe('Login')
    expect(importSourceLabel('exports/orders.tar.gz')).toBe('orders')
    expect(importSourceLabel(undefined)).toBe('')
    expect(importSourceLabel('  ')).toBe('')
  })
})
