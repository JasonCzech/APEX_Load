// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

import DropIngestOverlay from './DropIngestOverlay'
import type { DropIngestPhase } from './dropIngestFeedback'

describe('DropIngestOverlay', () => {
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

  function render(phase: DropIngestPhase, label: string, inline?: boolean) {
    act(() => {
      root.render(
        <div className="drop-ingest-host">
          <DropIngestOverlay phase={phase} label={label} inline={inline} />
        </div>,
      )
    })
  }

  const overlay = () => container.querySelector<HTMLElement>('[data-testid="drop-ingest-overlay"]')
  const live = () => container.querySelector<HTMLElement>('.drop-ingest-live')

  it('draws nothing while idle but keeps its live region mounted', () => {
    render('idle', '')
    expect(overlay()).toBeNull()
    expect(live()).toBeTruthy()
    expect(live()?.textContent).toBe('')
  })

  it('shows the host\'s work as a light on the edge and a chip, not a cover', () => {
    render('working', 'Validating plan.jmx…')

    const layer = overlay()
    expect(layer?.className).toContain('drop-ingest-working')
    expect(layer?.getAttribute('aria-hidden')).toBe('true')
    expect(layer?.querySelector('.drop-ingest-bar')).toBeTruthy()
    const chip = layer?.querySelector('.drop-ingest-chip')
    expect(chip?.querySelector('.drop-ingest-spinner')).toBeTruthy()
    expect(chip?.querySelector('.drop-ingest-label')?.textContent).toBe('Validating plan.jmx…')
    // The only text in the layer is the chip's, so the file row beneath is not
    // competing with a second message.
    expect(layer?.textContent).toBe('Validating plan.jmx…')
    expect(layer?.querySelector('svg')).toBeNull()
  })

  it('announces the host\'s work once, through the live region, not the visual layer', () => {
    render('idle', '')
    const region = live()
    render('working', 'Validating plan.jmx…')

    expect(live()).toBe(region)
    expect(live()?.textContent).toBe('Validating plan.jmx…')
    expect(live()?.getAttribute('aria-live')).toBe('polite')
    expect(overlay()?.getAttribute('aria-hidden')).toBe('true')

    render('idle', '')
    expect(live()?.textContent).toBe('')
  })

  it('keeps the cover for reading a drop and the pill for confirming it', () => {
    render('ingesting', 'Reading file…')
    expect(overlay()?.className).toContain('drop-ingest-ingesting')
    expect(overlay()?.querySelector('.drop-ingest-spinner')).toBeTruthy()
    expect(overlay()?.querySelector('.drop-ingest-bar')).toBeNull()
    expect(overlay()?.querySelector('.drop-ingest-chip')).toBeNull()

    render('accepted', 'Added plan.jmx')
    expect(overlay()?.className).toContain('drop-ingest-accepted')
    expect(overlay()?.querySelector('.drop-ingest-check')).toBeTruthy()
    expect(overlay()?.querySelector('.drop-ingest-spinner')).toBeNull()
    expect(overlay()?.querySelector('.drop-ingest-bar')).toBeNull()
  })

  it('marks the work layer of a single-row host', () => {
    render('working', 'Validating plan.jmx…', true)
    expect(overlay()?.className).toContain('is-inline')
    render('working', 'Validating plan.jmx…', false)
    expect(overlay()?.className).not.toContain('is-inline')
  })
})
