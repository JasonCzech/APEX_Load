// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest'

import { readSettingsBreadcrumb, writeSettingsBreadcrumb } from './settingsBreadcrumbs'

describe('settingsBreadcrumbs', () => {
  afterEach(() => {
    window.sessionStorage.clear()
    vi.useRealTimers()
  })

  it('round-trips a value and stays readable for every mount in a remount chain', () => {
    writeSettingsBreadcrumb('crumb', { projectId: 'p1', message: 'Created.' })

    // A single scope switch remounts the page several times (project, then
    // application/release canonicalization); each mount must see the crumb.
    expect(readSettingsBreadcrumb<{ projectId: string; message: string }>('crumb'))
      .toEqual({ projectId: 'p1', message: 'Created.' })
    expect(readSettingsBreadcrumb<{ projectId: string; message: string }>('crumb'))
      .toEqual({ projectId: 'p1', message: 'Created.' })
  })

  it('retires a crumb past the freshness bound and removes it', () => {
    vi.useFakeTimers()
    vi.setSystemTime(1_000_000)
    writeSettingsBreadcrumb('crumb', 'stale message')
    vi.setSystemTime(1_000_000 + 16_000)

    expect(readSettingsBreadcrumb('crumb')).toBeNull()
    expect(window.sessionStorage.getItem('crumb')).toBeNull()
  })

  it('keeps a crumb readable within the freshness bound', () => {
    vi.useFakeTimers()
    vi.setSystemTime(1_000_000)
    writeSettingsBreadcrumb('crumb', 'fresh message')
    vi.setSystemTime(1_000_000 + 5_000)

    expect(readSettingsBreadcrumb('crumb')).toBe('fresh message')
  })

  it('returns null for missing or malformed entries and clears the bad data', () => {
    expect(readSettingsBreadcrumb('missing')).toBeNull()

    window.sessionStorage.setItem('bad', 'not json {')
    expect(readSettingsBreadcrumb('bad')).toBeNull()
    expect(window.sessionStorage.getItem('bad')).toBeNull()

    window.sessionStorage.setItem('shapeless', JSON.stringify({ value: 'x' }))
    expect(readSettingsBreadcrumb('shapeless')).toBeNull()
    expect(window.sessionStorage.getItem('shapeless')).toBeNull()
  })
})
