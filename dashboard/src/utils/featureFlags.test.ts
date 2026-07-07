// @vitest-environment jsdom

import { afterEach, describe, expect, it } from 'vitest'

import { isRunReportEnabled } from './featureFlags'

describe('featureFlags', () => {
  afterEach(() => {
    window.localStorage.clear()
  })

  it('reads the run report flag from browser storage', () => {
    window.localStorage.setItem('apexload.featureFlags.runReport', 'true')
    expect(isRunReportEnabled()).toBe(true)

    window.localStorage.setItem('apexload.featureFlags.runReport', 'off')
    expect(isRunReportEnabled()).toBe(false)
  })
})
