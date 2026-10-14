// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ImportSaveConfirmation from './ImportSaveConfirmation'
import type { StoredScript } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLDivElement
let root: Root
beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})
afterEach(async () => {
  await act(async () => root.unmount())
  container.remove()
})

const saved = (count: number) => Array.from({ length: count }, (_, index) => ({
  id: `script-${index + 1}`, name: `Request ${index + 1}`, protocol: 'http', version: '1.0', created_at: '2026-10-14T00:00:00Z',
}) as StoredScript)
const button = (name: string) => [...container.querySelectorAll('button')].find(item => item.textContent?.trim() === name)

describe('ImportSaveConfirmation', () => {
  it('leads to a scenario and a debug run, and keeps a large batch out of the way', async () => {
    const onOpen = vi.fn()
    const onCreateScenario = vi.fn()
    await act(async () => root.render(<ImportSaveConfirmation result={{ scripts: saved(16) }} onImportMore={vi.fn()} onOpen={onOpen} onCreateScenario={onCreateScenario} />))
    expect(button('Create a scenario from these 16 scripts')?.className).toContain('btn-primary')
    await act(async () => button('Create a scenario from these 16 scripts')!.click())
    expect(onCreateScenario).toHaveBeenCalledOnce()
    await act(async () => button('Debug Request 1')!.click())
    expect(onOpen).toHaveBeenCalledWith({ scripts: [saved(1)[0]], editorWorkspace: 'debug' })
    // Every script still opens, from a disclosure rather than 16 buttons.
    expect(container.querySelector('.import-save-more-scripts summary')?.textContent).toBe('Open a saved script in Visual Editor (16)')
    expect(container.querySelectorAll('.import-save-more-scripts button')).toHaveLength(16)
  })

  it('lists a few saved scripts directly and keeps Import More Files primary without a scenario action', async () => {
    await act(async () => root.render(<ImportSaveConfirmation result={{ scripts: saved(2) }} onImportMore={vi.fn()} onOpen={vi.fn()} />))
    expect(button('Import More Files')?.className).toContain('btn-primary')
    expect(button('Open Request 2 in Visual Editor')).toBeDefined()
    expect(container.querySelector('.import-save-more-scripts')).toBeNull()
  })

  it('shows why a scenario could not be created', async () => {
    await act(async () => root.render(<ImportSaveConfirmation result={{ scripts: saved(1) }} onImportMore={vi.fn()} onOpen={vi.fn()} onCreateScenario={vi.fn()} scenarioError="Your role cannot create scenarios in this project." />))
    expect(button('Create a scenario from this script')).toBeDefined()
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('Your role cannot create scenarios in this project.')
  })
})
