// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest'

import { consumeProjectSecretsSettingsProjectId, openProjectSecretsSettings, projectSecretsHref } from './projectSecretsNavigation'

afterEach(() => {
  window.localStorage.clear()
  window.location.hash = ''
})

describe('Project secrets navigation', () => {
  it('goes to the Project secrets destination, not bare Settings, keeping the workspace', () => {
    window.location.hash = '#/scripts?projectId=p1&applicationId=a1'
    expect(projectSecretsHref()).toBe('#/access?setting=secrets&projectId=p1&applicationId=a1')
  })

  it('opens it for the project that asked', () => {
    window.location.hash = '#/scripts?projectId=p1'
    openProjectSecretsSettings('p2')
    expect(window.location.hash).toBe('#/access?setting=secrets&projectId=p1')
    expect(consumeProjectSecretsSettingsProjectId()).toBe('p2')
  })
})
