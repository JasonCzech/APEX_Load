// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import type { SystemInventoryGenerator } from '../types'
import ConfiguredGeneratorChips from './ConfiguredGeneratorChips'

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

describe('ConfiguredGeneratorChips', () => {
  it('names configured generators and marks one the live inventory cannot reach', () => {
    const inventory = [
      { target: '127.0.0.1:19091', generator_id: 'gen-a', region: 'us-east', status: 'connected' },
      { target: '127.0.0.1:19092', generator_id: 'gen-b', region: 'us-west', status: 'unreachable' },
      { target: '127.0.0.1:19093', generator_id: 'gen-c', status: 'ready' },
    ] as SystemInventoryGenerator[]
    act(() => root.render(<ConfiguredGeneratorChips targets={['127.0.0.1:19091', '127.0.0.1:19092', '127.0.0.1:19093', '10.0.0.9:9091']} inventory={inventory} rowClassName="row" chipClassName="chip" labelClassName="label" />))
    expect(container.querySelector('[role="group"]')?.getAttribute('aria-label')).toBe('Configured generators')
    // Ready and healthy count as online, as on the Monitoring page.
    expect(Array.from(container.querySelectorAll('.chip')).map(chip => chip.textContent)).toEqual(['gen-a · us-east', 'gen-b · us-west · unreachable', 'gen-c', '10.0.0.9:9091 · unreachable'])
  })

  it('makes no claim about reachability without an inventory, and renders nothing for no targets', () => {
    act(() => root.render(<ConfiguredGeneratorChips targets={['10.0.0.9:9091']} inventory={[]} rowClassName="row" chipClassName="chip" labelClassName="label" />))
    expect(container.querySelector('.chip')?.textContent).toBe('10.0.0.9:9091')
    act(() => root.render(<ConfiguredGeneratorChips targets={[]} inventory={[]} rowClassName="row" chipClassName="chip" labelClassName="label" />))
    expect(container.innerHTML).toBe('')
  })
})
