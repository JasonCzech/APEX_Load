import { describe, expect, it } from 'vitest'

import { importFolderNameFromSource } from './importDestinationContext'

describe('importFolderNameFromSource', () => {
  it('turns a source file name into a folder name', () => {
    expect(importFolderNameFromSource('checkout_plan.jmx')).toBe('checkout plan')
    expect(importFolderNameFromSource('payments-api.openapi.yaml')).toBe('payments api')
    expect(importFolderNameFromSource('Orders.postman_collection.json')).toBe('Orders')
    expect(importFolderNameFromSource('Login.USZ')).toBe('Login')
    expect(importFolderNameFromSource('exports/orders.tar.gz')).toBe('orders')
    expect(importFolderNameFromSource(undefined)).toBe('')
    expect(importFolderNameFromSource('  ')).toBe('')
  })
})
