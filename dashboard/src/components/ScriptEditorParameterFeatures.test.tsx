// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import { ParameterDefinitionsSection, ParameterSettingsBody } from './ScriptEditorParameterFeatures'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

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

function setTextareaValue(textarea: HTMLTextAreaElement, value: string) {
  const valueSetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!
  act(() => {
    valueSetter.call(textarea, value)
    textarea.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

describe('ParameterSettingsBody inline values', () => {
  it('preserves blank entries, surrounding whitespace, and a trailing newline', () => {
    const onUpdate = vi.fn()
    act(() => root.render(
      <ParameterSettingsBody
        parameter={{ name: 'customer_name', type: 'list', values: ['Smith, John', '', ' padded '] }}
        onUpdate={onUpdate}
      />,
    ))
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement
    expect(textarea.value).toBe('Smith, John\n\n padded ')

    // Operator presses Enter at the end of the box to start a new value.
    setTextareaValue(textarea, 'Smith, John\n\n padded \n')
    expect(onUpdate).toHaveBeenCalledWith('values', ['Smith, John', '', ' padded ', ''])
  })

  it('clears the values list when the box is emptied', () => {
    const onUpdate = vi.fn()
    act(() => root.render(
      <ParameterSettingsBody parameter={{ name: 'customer_name', type: 'list', values: ['alpha'] }} onUpdate={onUpdate} />,
    ))
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement
    setTextareaValue(textarea, '')
    expect(onUpdate).toHaveBeenCalledWith('values', [])
  })
})

function scriptWithPathOnlySource(): ScriptDefinition {
  return {
    name: 'jmx-import',
    version: '1',
    protocol: 'http',
    config: { base_url: 'https://example.test' },
    actions: [],
    data_sources: [
      { name: 'users', type: 'csv', path: 'users.csv', inline_data: '', strategy: 'sequential' },
    ],
    parameters: [
      { name: 'user_id', type: 'file', data_source: 'users', column: 'user_id' },
    ],
  }
}

describe('ParameterDefinitionsSection row counts', () => {
  it('shows an unknown row count for a path-backed source and keeps it under User-Defined Files', () => {
    act(() => root.render(
      <ParameterDefinitionsSection
        focusedSourceName={null}
        isOpen
        onAddParameter={vi.fn()}
        onRemoveParameter={vi.fn()}
        onSelectParameter={vi.fn()}
        onToggleOpen={vi.fn()}
        onUpdateParameter={vi.fn()}
        selectedParameterIdx={null}
        script={scriptWithPathOnlySource()}
      />,
    ))

    const sourceRow = container.querySelector('tr.parameter-source-row') as HTMLTableRowElement
    expect(sourceRow.cells[2].textContent).toBe('—')

    const categoryRow = container.querySelector('tr.parameter-category-row') as HTMLTableRowElement
    expect(categoryRow.textContent).toContain('User-Defined Files')
  })

  it('still shows a real row count when the source carries inline data', () => {
    const script = scriptWithPathOnlySource()
    script.data_sources![0].inline_data = 'user_id\n1\n2\n'
    act(() => root.render(
      <ParameterDefinitionsSection
        focusedSourceName={null}
        isOpen
        onAddParameter={vi.fn()}
        onRemoveParameter={vi.fn()}
        onSelectParameter={vi.fn()}
        onToggleOpen={vi.fn()}
        onUpdateParameter={vi.fn()}
        selectedParameterIdx={null}
        script={script}
      />,
    ))

    const sourceRow = container.querySelector('tr.parameter-source-row') as HTMLTableRowElement
    expect(sourceRow.cells[2].textContent).toBe('2')
  })
})
