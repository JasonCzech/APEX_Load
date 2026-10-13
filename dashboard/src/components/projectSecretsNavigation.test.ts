// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest'

import { consumeProjectSecretsSettingsProjectId, openProjectSecretsPage, prepareProjectSecretsNavigation, PROJECT_SECRETS_REQUEST_TTL_MS, PROJECT_SECRETS_SETTINGS_PROJECT_KEY, projectSecretsHref, requestedProjectSecretsProjectId } from './projectSecretsNavigation'

afterEach(() => {
  window.localStorage.clear()
  window.location.hash = ''
})

describe('Project secrets navigation', () => {
  it('goes to Shared assets › Secrets, not bare Settings, keeping the workspace', () => {
    window.location.hash = '#/scripts?projectId=p1&applicationId=a1'
    expect(projectSecretsHref()).toBe('#/shared-assets?section=secrets&projectId=p1&applicationId=a1')
  })

  it('opens it for the project that asked', () => {
    window.location.hash = '#/scripts?projectId=p1'
    openProjectSecretsPage('p2')
    expect(window.location.hash).toBe('#/shared-assets?section=secrets&projectId=p1')
    expect(consumeProjectSecretsSettingsProjectId()).toBe('p2')
    // A request is used once.
    expect(requestedProjectSecretsProjectId()).toBe('')
  })

  it('forgets a request whose navigation never arrived', () => {
    prepareProjectSecretsNavigation('p2', 1_000)
    expect(requestedProjectSecretsProjectId(1_000 + PROJECT_SECRETS_REQUEST_TTL_MS)).toBe('p2')
    expect(requestedProjectSecretsProjectId(1_001 + PROJECT_SECRETS_REQUEST_TTL_MS)).toBe('')
    expect(consumeProjectSecretsSettingsProjectId(1_001 + PROJECT_SECRETS_REQUEST_TTL_MS)).toBe('')
    expect(window.localStorage.getItem(PROJECT_SECRETS_SETTINGS_PROJECT_KEY)).toBeNull()
  })

  it('ignores a request stored in another shape', () => {
    window.localStorage.setItem(PROJECT_SECRETS_SETTINGS_PROJECT_KEY, 'p2')
    expect(requestedProjectSecretsProjectId()).toBe('')
    window.localStorage.setItem(PROJECT_SECRETS_SETTINGS_PROJECT_KEY, JSON.stringify({ projectId: 7, at: Date.now() }))
    expect(requestedProjectSecretsProjectId()).toBe('')
  })
})
