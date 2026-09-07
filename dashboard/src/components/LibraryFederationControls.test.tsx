// @vitest-environment jsdom

import { createRoot } from 'react-dom/client'
import { act } from 'react'
import { afterEach, describe, expect, it } from 'vitest'

import LibraryFederationControls from './LibraryFederationControls'
import { type UseLibraryFederationResult } from './useLibraryFederation'

function federation(projectMapping = 'dbcecb40-d493-49a1-b3af-78e547009786') {
  return {
    remotes: [],
    selectedRemoteId: '',
    setSelectedRemoteId: () => undefined,
    sourceFilter: 'host',
    setSourceFilter: () => undefined,
    projectMapping,
    setProjectMapping: () => undefined,
    includeDependencies: true,
    setIncludeDependencies: () => undefined,
    localItems: [],
    remoteItems: [],
    loading: false,
    actionMessage: '',
    actionError: '',
    diffMessage: '',
    selectedRemote: undefined,
    credentialsReady: false,
    effectiveProjectID: projectMapping,
    actionBlocked: true,
    actionBlockedReason: 'Select a Remote Workspace first.',
    transferBlockReasonForItem: () => 'Select a Remote Workspace first.',
    refreshCatalogs: async () => undefined,
    pullRemoteItem: async () => undefined,
    pushLocalItem: async () => undefined,
    compareItems: async () => undefined,
    hostItemForLocal: () => undefined,
    syncStatusForLocal: () => 'local only',
    remoteItemHref: () => undefined,
  } as unknown as UseLibraryFederationResult
}

describe('LibraryFederationControls', () => {
  let container: HTMLDivElement | undefined

  afterEach(() => {
    container?.remove()
    container = undefined
  })

  it.each(['script', 'scenario', 'result'] as const)('shows the friendly project name for the %s library while retaining the internal mapping', kind => {
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)

    act(() => {
      root.render(
        <LibraryFederationControls
          kind={kind}
          federation={federation()}
          projectName="Checkout"
        />,
      )
    })

    const input = Array.from(container.querySelectorAll('input')).find(candidate => candidate.previousElementSibling?.textContent === 'Project Mapping')
    expect(input?.value).toBe('Checkout')
    expect(input?.readOnly).toBe(true)
    expect(input?.title).toBe('Project ID: dbcecb40-d493-49a1-b3af-78e547009786')
    expect(container.textContent).not.toContain('dbcecb40-d493-49a1-b3af-78e547009786')

    act(() => root.unmount())
  })

  it('falls back to the mapping when a friendly project name is unavailable', () => {
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)

    act(() => {
      root.render(<LibraryFederationControls kind="script" federation={federation('legacy-project')} />)
    })

    const input = Array.from(container.querySelectorAll('input')).find(candidate => candidate.previousElementSibling?.textContent === 'Project Mapping')
    expect(input?.value).toBe('legacy-project')

    act(() => root.unmount())
  })

  it.each([
    { kind: 'script', visible: false },
    { kind: 'scenario', visible: true },
    { kind: 'result', visible: false },
  ] as const)('shows the referenced-script option only for the $kind library', ({ kind, visible }) => {
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)

    act(() => {
      root.render(<LibraryFederationControls kind={kind} federation={federation()} projectName="Checkout" />)
    })

    const checkbox = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(candidate => candidate.parentElement?.textContent?.includes('Include referenced scripts'))
    expect(Boolean(checkbox)).toBe(visible)
    if (visible) expect(checkbox?.checked).toBe(true)
    expect(container.textContent).not.toContain('Include dependencies')

    act(() => root.unmount())
  })
})
