// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

import ImportDropzone from './ImportDropzone'

function dropEvent(files: File[]): Event {
  const event = new Event('drop', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'dataTransfer', { value: { files } })
  return event
}

describe('ImportDropzone drop feedback', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    vi.useFakeTimers()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
    vi.useRealTimers()
  })

  function overlayText(): string {
    return container.querySelector('[data-testid="drop-ingest-overlay"]')?.textContent || ''
  }

  it('animates reading then confirms the file after a drop', () => {
    const onFile = vi.fn()
    act(() => {
      root.render(<ImportDropzone accept=".jmx" file={null} onFile={onFile} dropHint="Drop a plan" />)
    })

    const zone = container.querySelector<HTMLElement>('.import-dropzone')
    expect(zone).toBeTruthy()
    expect(overlayText()).toBe('')

    act(() => {
      zone?.dispatchEvent(dropEvent([new File(['<jmeterTestPlan/>'], 'plan.jmx')]))
    })

    // The file is handed over synchronously, so without the animation floor the
    // drop would land with no visible change at all.
    expect(onFile).toHaveBeenCalledTimes(1)
    expect(zone?.className).toContain('is-ingesting')
    expect(overlayText()).toContain('Reading file…')

    act(() => { vi.advanceTimersByTime(500) })
    expect(zone?.className).toContain('is-accepted')
    expect(overlayText()).toContain('Added plan.jmx')

    act(() => { vi.advanceTimersByTime(1400) })
    expect(container.querySelector('[data-testid="drop-ingest-overlay"]')).toBeNull()
    expect(zone?.className).not.toContain('is-accepted')
  })

  it('announces drop progress through a live region that outlives the overlay', () => {
    act(() => {
      root.render(<ImportDropzone accept=".jmx" file={null} onFile={vi.fn()} dropHint="Drop a plan" />)
    })

    // The region must already exist before the text arrives — a live region
    // inserted together with its text is announced inconsistently.
    const live = container.querySelector<HTMLElement>('.drop-ingest-live')
    expect(live).toBeTruthy()
    expect(live?.getAttribute('aria-live')).toBe('polite')
    expect(live?.textContent).toBe('')

    const zone = container.querySelector<HTMLElement>('.import-dropzone')
    act(() => {
      zone?.dispatchEvent(dropEvent([new File(['<jmeterTestPlan/>'], 'plan.jmx')]))
    })
    expect(container.querySelector('.drop-ingest-live')).toBe(live)
    expect(live?.textContent).toContain('Reading file…')

    act(() => { vi.advanceTimersByTime(500) })
    expect(live?.textContent).toContain('Added plan.jmx')

    // The visual layer is aria-hidden so the label is not announced twice.
    expect(container.querySelector('[data-testid="drop-ingest-overlay"]')?.getAttribute('aria-hidden')).toBe('true')

    act(() => { vi.advanceTimersByTime(1400) })
    expect(live?.textContent).toBe('')
  })

  it('keeps the dropzone name stable and does not fold status text into it', () => {
    act(() => {
      root.render(<ImportDropzone accept=".jmx" file={null} onFile={vi.fn()} dropHint="Drop a plan" />)
    })

    const zone = container.querySelector<HTMLElement>('.import-dropzone')
    expect(zone?.getAttribute('aria-label')).toBe('Drop a plan')

    act(() => {
      zone?.dispatchEvent(dropEvent([new File(['<jmeterTestPlan/>'], 'plan.jmx')]))
    })
    expect(zone?.getAttribute('aria-label')).toBe('Drop a plan')
  })

  it('clears the animation when the dropped file is rejected', () => {
    const onFile = vi.fn()
    const onError = vi.fn()
    act(() => {
      root.render(
        <ImportDropzone
          accept=".jmx"
          file={null}
          onFile={onFile}
          dropHint="Drop a plan"
          validate={() => 'Only .jmx plans are supported.'}
          onError={onError}
        />,
      )
    })

    const zone = container.querySelector<HTMLElement>('.import-dropzone')
    act(() => {
      zone?.dispatchEvent(dropEvent([new File(['nope'], 'notes.txt')]))
    })

    expect(onFile).not.toHaveBeenCalled()
    expect(onError).toHaveBeenCalledWith('Only .jmx plans are supported.')
    expect(container.querySelector('[data-testid="drop-ingest-overlay"]')).toBeNull()
    expect(zone?.className).not.toContain('is-ingesting')
  })

  it('shows the importer\'s work on the selected file and leaves the file in view', () => {
    const file = new File(['{}'], 'spec.json')
    act(() => {
      root.render(<ImportDropzone accept=".json" file={file} onFile={vi.fn()} dropHint="Drop a spec" workingLabel="Inspecting spec.json…" />)
    })

    const zone = container.querySelector<HTMLElement>('.import-dropzone')!
    expect(zone.className).toContain('is-working')
    expect(overlayText()).toBe('Inspecting spec.json…')
    expect(container.querySelector('.drop-ingest-bar')).toBeTruthy()
    // Not a cover: the file name and its controls are still in the zone.
    expect(zone.querySelector('.import-dropzone-name')?.textContent).toBe('spec.json')
    expect(Array.from(zone.querySelectorAll('button')).map(button => button.textContent)).toEqual(['Change', 'Remove'])
    // The zone keeps its own accessible name; the live region carries the status.
    expect(zone.getAttribute('aria-label')).toBe('Selected file spec.json')
    expect(container.querySelector('.drop-ingest-live')?.textContent).toBe('Inspecting spec.json…')

    act(() => {
      root.render(<ImportDropzone accept=".json" file={file} onFile={vi.fn()} dropHint="Drop a spec" />)
    })
    // The importer finished, so the zone returns to rest at once — no timer.
    expect(zone.className).not.toContain('is-working')
    expect(container.querySelector('[data-testid="drop-ingest-overlay"]')).toBeNull()
    expect(container.querySelector('.drop-ingest-live')?.textContent).toBe('')
  })

  it('shows the importer\'s work on a source the zone never holds', () => {
    // A dropped dashboard export is parsed at once, so the zone keeps showing
    // its empty prompt while the importer works.
    act(() => {
      root.render(<ImportDropzone accept=".json" file={null} onFile={vi.fn()} dropHint="Drop a dashboard" workingLabel="Parsing infra.json…" />)
    })

    const zone = container.querySelector<HTMLElement>('.import-dropzone')!
    expect(zone.className).toContain('is-working')
    expect(overlayText()).toBe('Parsing infra.json…')
    expect(zone.getAttribute('role')).toBe('button')
    expect(zone.getAttribute('aria-label')).toBe('Drop a dashboard')
    expect(zone.textContent).toContain('Drop a dashboard')
  })

  it('hands the zone to a drop being read, then back to the importer\'s work', () => {
    const file = new File(['{}'], 'spec.json')
    act(() => {
      root.render(<ImportDropzone accept=".json" file={file} onFile={vi.fn()} dropHint="Drop a spec" workingLabel="Inspecting spec.json…" />)
    })
    const zone = container.querySelector<HTMLElement>('.import-dropzone')!

    act(() => { zone.dispatchEvent(dropEvent([new File(['{}'], 'next.json')])) })
    expect(zone.className).toContain('is-ingesting')
    expect(zone.className).not.toContain('is-working')
    expect(overlayText()).toContain('Reading file…')

    act(() => { vi.advanceTimersByTime(500) })
    expect(zone.className).toContain('is-working')
    expect(overlayText()).toBe('Inspecting spec.json…')
  })
})
