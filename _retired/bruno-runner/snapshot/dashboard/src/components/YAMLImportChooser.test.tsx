// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import YAMLImportChooser from './YAMLImportChooser'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('./BrunoImport', () => ({
  default: (props: {
    folderId?: string | null
    onFolderChange?: (folderId: string) => void
    destinationFolderManagedExternally?: boolean
    onImported?: (result: { scripts: Array<{ name: string; folder_id?: string | null }> }) => void
    onClose?: () => void
  }) => (
    <div>
      Bruno importer selected
      <span data-testid="bruno-folder">{props.folderId || 'project-root'}</span>
      <span data-testid="bruno-destination-owner">{props.destinationFolderManagedExternally ? 'external' : 'internal'}</span>
      <button onClick={() => props.onFolderChange?.('folder-from-bruno')}>Choose shared folder from Bruno</button>
      <button onClick={() => props.onImported?.({ scripts: [{ name: 'Bruno saved', folder_id: props.folderId }] })}>Save Bruno mock</button>
      <button onClick={props.onClose}>Back from Bruno</button>
    </div>
  ),
}))
vi.mock('./ProtocolAssetImport', () => ({
  default: (props: {
    folderId?: string | null
    onImported?: (result: { scripts: Array<{ name: string; folder_id?: string | null }> }) => void
    onClose?: () => void
  }) => (
    <div>
      OpenAPI importer selected
      <span data-testid="openapi-folder">{props.folderId || 'project-root'}</span>
      <button onClick={() => props.onImported?.({ scripts: [{ name: 'OpenAPI saved', folder_id: props.folderId }] })}>Save OpenAPI mock</button>
      <button onClick={props.onClose}>Back from OpenAPI</button>
    </div>
  ),
}))

describe('YAMLImportChooser', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it.each([
    ['Use Bruno importer', 'Bruno importer selected'],
    ['Use OpenAPI importer', 'OpenAPI importer selected'],
  ])('routes %s explicitly', (buttonLabel, expected) => {
    act(() => root.render(<YAMLImportChooser />))
    const button = [...container.querySelectorAll('button')].find((candidate) => candidate.textContent === buttonLabel)
    expect(button).toBeTruthy()
    act(() => button?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(container.textContent).toContain(expected)
  })

  it('returns to the chooser when a different file selection arrives', () => {
    const first = [new File(['title: First'], 'first.yml')]
    const second = [new File(['title: Second'], 'second.yml')]
    act(() => root.render(<YAMLImportChooser initialFiles={first} />))
    const brunoButton = [...container.querySelectorAll('button')].find((candidate) => candidate.textContent === 'Use Bruno importer')
    act(() => brunoButton?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(container.textContent).toContain('Bruno importer selected')

    act(() => root.render(<YAMLImportChooser initialFiles={second} />))

    expect(container.textContent).toContain('Choose an importer')
    expect(container.textContent).not.toContain('Bruno importer selected')
    expect(container.textContent).not.toContain('OpenAPI importer selected')
  })

  it('keeps one shared destination authoritative across OpenAPI and Bruno saves', () => {
    const onImported = vi.fn()
    function Harness() {
      const [folderId, setFolderId] = useState('folder-shared')
      return (
        <YAMLImportChooser
          folderId={folderId}
          onFolderChange={setFolderId}
          onImported={onImported}
        />
      )
    }
    act(() => root.render(<Harness />))

    const click = (label: string) => act(() => {
      [...container.querySelectorAll<HTMLButtonElement>('button')]
        .find(button => button.textContent === label)
        ?.click()
    })

    click('Use OpenAPI importer')
    expect(container.querySelector('[data-testid="openapi-folder"]')?.textContent).toBe('folder-shared')
    click('Save OpenAPI mock')
    expect(onImported).toHaveBeenLastCalledWith({
      scripts: [{ name: 'OpenAPI saved', folder_id: 'folder-shared' }],
    })
    click('Back from OpenAPI')

    click('Use Bruno importer')
    expect(container.querySelector('[data-testid="bruno-folder"]')?.textContent).toBe('folder-shared')
    expect(container.querySelector('[data-testid="bruno-destination-owner"]')?.textContent).toBe('external')
    click('Choose shared folder from Bruno')
    expect(container.querySelector('[data-testid="bruno-folder"]')?.textContent).toBe('folder-from-bruno')
    click('Save Bruno mock')
    expect(onImported).toHaveBeenLastCalledWith({
      scripts: [{ name: 'Bruno saved', folder_id: 'folder-from-bruno' }],
    })
    click('Back from Bruno')

    click('Use OpenAPI importer')
    expect(container.querySelector('[data-testid="openapi-folder"]')?.textContent).toBe('folder-from-bruno')
  })
})
