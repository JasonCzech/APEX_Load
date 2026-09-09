// @vitest-environment jsdom

import { afterEach, describe, expect, it } from 'vitest'

import { isJavaClasspathUploadEnabled, isRunReportEnabled } from './featureFlags'

describe('featureFlags', () => {
  afterEach(() => {
    window.localStorage.clear()
  })

  it('enables the unified results workspace by default', () => { expect(isRunReportEnabled()).toBe(true) })

  it('reads the run report flag from browser storage', () => {
    window.localStorage.setItem('apexload.featureFlags.runReport', 'true')
    expect(isRunReportEnabled()).toBe(true)

    window.localStorage.setItem('apexload.featureFlags.runReport', 'off')
    expect(isRunReportEnabled()).toBe(false)
  })

  it('defaults the java classpath upload flag to on', () => {
    // The server gate stays fail-closed, so showing the panel by default is
    // safe; disabling it is an explicit per-browser or build-time choice.
    expect(isJavaClasspathUploadEnabled()).toBe(true)
  })

  it('reads the java classpath upload flag from browser storage', () => {
    window.localStorage.setItem('apexload.featureFlags.javaClasspathUpload', 'true')
    expect(isJavaClasspathUploadEnabled()).toBe(true)

    window.localStorage.setItem('apexload.featureFlags.javaClasspathUpload', 'disabled')
    expect(isJavaClasspathUploadEnabled()).toBe(false)
  })
})
