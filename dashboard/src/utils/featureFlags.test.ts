// @vitest-environment jsdom

import { afterEach, describe, expect, it } from 'vitest'

import { isJavaClasspathUploadEnabled, isRunReportEnabled } from './featureFlags'

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

  it('defaults the java classpath upload flag to off', () => {
    expect(isJavaClasspathUploadEnabled()).toBe(false)
  })

  it('reads the java classpath upload flag from browser storage', () => {
    window.localStorage.setItem('apexload.featureFlags.javaClasspathUpload', 'true')
    expect(isJavaClasspathUploadEnabled()).toBe(true)

    window.localStorage.setItem('apexload.featureFlags.javaClasspathUpload', 'disabled')
    expect(isJavaClasspathUploadEnabled()).toBe(false)
  })
})
