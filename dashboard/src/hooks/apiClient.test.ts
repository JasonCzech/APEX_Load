import { describe, expect, it } from 'vitest'

import { resolveApiOrigin } from './apiClient'

describe('resolveApiOrigin', () => {
  it('uses an explicit API origin when configured', () => {
    expect(resolveApiOrigin(
      { VITE_API_ORIGIN: 'http://api.example.test:8080/' },
      { protocol: 'http:', hostname: 'localhost', port: '3000', origin: 'http://localhost:3000' },
    )).toBe('http://api.example.test:8080')
  })

  it('uses the same-origin Vite proxy for local dev dashboards', () => {
    expect(resolveApiOrigin(
      { DEV: true },
      { protocol: 'http:', hostname: 'localhost', port: '3031', origin: 'http://localhost:3031' },
    )).toBe('http://localhost:3031')
  })

  it('uses the same-origin proxy for localhost port 3000 even when DEV metadata is absent', () => {
    expect(resolveApiOrigin(
      {},
      { protocol: 'http:', hostname: '127.0.0.1', port: '3000', origin: 'http://127.0.0.1:3000' },
    )).toBe('http://127.0.0.1:3000')
  })

  it('falls back to the local controller for file URLs', () => {
    expect(resolveApiOrigin(
      { DEV: true },
      { protocol: 'file:', hostname: '', port: '', origin: 'null' },
    )).toBe('http://127.0.0.1:8080')
  })

  it('uses the serving origin for embedded controller dashboard assets', () => {
    expect(resolveApiOrigin(
      {},
      { protocol: 'http:', hostname: 'stormrunner.example.test', port: '', origin: 'https://stormrunner.example.test' },
    )).toBe('https://stormrunner.example.test')
  })
})
