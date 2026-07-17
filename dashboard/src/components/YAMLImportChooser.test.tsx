// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import YAMLImportChooser from './YAMLImportChooser'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('./BrunoImport', () => ({ default: () => <div>Bruno importer selected</div> }))
vi.mock('./ProtocolAssetImport', () => ({ default: () => <div>OpenAPI importer selected</div> }))

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
})
