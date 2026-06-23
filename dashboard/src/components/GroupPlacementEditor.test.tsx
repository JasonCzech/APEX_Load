// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import GroupPlacementEditor from './GroupPlacementEditor'
import { GeneratorAssignmentSettings } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLElement
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

// Controlled harness mirroring how the editor is used (assignment + onChange owned by
// the parent), so mode transitions exercise the real serialize -> re-derive round-trip.
function Harness({ availableLabels, availableTargets, protocol }: {
  availableLabels?: Record<string, string[]>
  availableTargets?: string[]
  protocol?: string
}) {
  const [assignment, setAssignment] = useState<GeneratorAssignmentSettings | undefined>(undefined)
  return (
    <GroupPlacementEditor
      assignment={assignment}
      onChange={setAssignment}
      availableLabels={availableLabels}
      availableTargets={availableTargets}
      protocol={protocol}
      allowInherit
    />
  )
}

function radio(text: string): HTMLInputElement {
  const labels = Array.from(container.querySelectorAll('.new-test-placement-toggle label'))
  const label = labels.find(node => node.textContent?.includes(text))
  if (!label) throw new Error(`radio not found: ${text}`)
  return label.querySelector('input[type=radio]') as HTMLInputElement
}

describe('GroupPlacementEditor', () => {
  it('keeps the Require capability panel open before a label is chosen (mode does not snap back to auto)', () => {
    act(() => root.render(<Harness availableLabels={{ jvm: ['11', '17'] }} />))

    act(() => radio('Require capability').click())

    // The bug: required_labels serializes as { mode:'auto', required_labels:{} } and
    // the radio derived straight back to "Automatic placement", hiding the panel.
    expect(radio('Require capability').checked).toBe(true)
    expect(radio('Automatic placement').checked).toBe(false)
    const select = container.querySelector('select.settings-input') as HTMLSelectElement | null
    expect(select).not.toBeNull()
  })

  it('serializes a chosen capability as auto + required_labels', () => {
    act(() => root.render(<Harness availableLabels={{ jvm: ['11', '17'] }} />))
    act(() => radio('Require capability').click())

    const select = container.querySelector('select.settings-input') as HTMLSelectElement
    act(() => {
      select.value = '17'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(container.textContent).toContain('jvm=17')
    expect(radio('Require capability').checked).toBe(true)
  })

  it('emits specific_hosts and capacity-weighted sharding for multi-host pins', () => {
    act(() => root.render(<Harness availableTargets={['gen-a:9090', 'gen-b:9090']} protocol="http" />))
    act(() => radio('Pin to host(s)').click())

    const checkboxes = Array.from(container.querySelectorAll('.new-test-generator-option input[type=checkbox]')) as HTMLInputElement[]
    expect(checkboxes.length).toBe(2)
    // Fallback rows (no live inventory) must remain selectable (M3).
    expect(checkboxes.every(box => !box.disabled)).toBe(true)

    act(() => checkboxes[0].click())
    act(() => {
      const boxes = Array.from(container.querySelectorAll('.new-test-generator-option input[type=checkbox]')) as HTMLInputElement[]
      boxes[1].click()
    })
    expect(container.textContent).toContain('capacity weighted')
  })

  it('inherit clears the per-group assignment', () => {
    act(() => root.render(<Harness availableTargets={['gen-a:9090']} protocol="http" />))
    act(() => radio('Pin to host(s)').click())
    expect(radio('Pin to host(s)').checked).toBe(true)

    act(() => radio('Inherit scenario default').click())
    expect(radio('Inherit scenario default').checked).toBe(true)
    expect(container.textContent).toContain('Inherits the scenario')
  })
})
