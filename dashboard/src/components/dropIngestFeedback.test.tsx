// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

import { describeWorking, useDropIngestFeedback, type DropIngestFeedback } from './dropIngestFeedback'

describe('useDropIngestFeedback', () => {
  let container: HTMLDivElement
  let root: Root
  let feedback: DropIngestFeedback

  function Harness({ workingLabel }: { workingLabel?: string }) {
    feedback = useDropIngestFeedback({ workingLabel })
    return null
  }

  function render(workingLabel?: string) {
    act(() => { root.render(<Harness workingLabel={workingLabel} />) })
  }

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

  it('is idle until a drop lands or the host reports work', () => {
    render()
    expect(feedback.phase).toBe('idle')
    expect(feedback.label).toBe('')
    expect(feedback.hostClassName).toBe('drop-ingest-host')
  })

  it('reports the host\'s work for exactly as long as the host supplies a label', () => {
    render('Validating plan.jmx…')
    expect(feedback.phase).toBe('working')
    expect(feedback.label).toBe('Validating plan.jmx…')
    expect(feedback.hostClassName).toBe('drop-ingest-host is-working')

    // The host owns this state. No timer ends it, so a slow validation never
    // reads as finished while the importer is still busy.
    act(() => { vi.advanceTimersByTime(10 * 60_000) })
    expect(feedback.phase).toBe('working')

    render(undefined)
    expect(feedback.phase).toBe('idle')
    expect(feedback.label).toBe('')
    expect(feedback.hostClassName).toBe('drop-ingest-host')
  })

  it('follows the host\'s label as it changes', () => {
    render('Validating 2 archives…')
    expect(feedback.label).toBe('Validating 2 archives…')
    render('Validating first.zip…')
    expect(feedback.phase).toBe('working')
    expect(feedback.label).toBe('Validating first.zip…')
  })

  it('treats an empty label as no work', () => {
    render('')
    expect(feedback.phase).toBe('idle')
    expect(feedback.hostClassName).toBe('drop-ingest-host')
  })

  it('lets a newly dropped file take the zone from the host\'s work, then hands it back', () => {
    render('Validating plan.jmx…')

    act(() => { feedback.begin('Reading file…') })
    // The drop replaces the source the host was working on.
    expect(feedback.phase).toBe('ingesting')
    expect(feedback.label).toBe('Reading file…')
    expect(feedback.hostClassName).toContain('is-ingesting')
    expect(feedback.hostClassName).not.toContain('is-working')

    act(() => { feedback.fail() })
    expect(feedback.phase).toBe('working')
    expect(feedback.label).toBe('Validating plan.jmx…')
  })

  it('shows the host\'s work over the confirmation pill, then what remains of the pill', () => {
    render()
    act(() => {
      feedback.begin()
      vi.advanceTimersByTime(500)
      feedback.succeed('Added plan.jmx')
    })
    expect(feedback.phase).toBe('accepted')

    render('Inspecting plan.jmx…')
    expect(feedback.phase).toBe('working')
    expect(feedback.label).toBe('Inspecting plan.jmx…')
    expect(feedback.hostClassName).toContain('is-working')
    expect(feedback.hostClassName).not.toContain('is-accepted')

    render(undefined)
    expect(feedback.phase).toBe('accepted')
    expect(feedback.label).toBe('Added plan.jmx')

    act(() => { vi.advanceTimersByTime(1400) })
    expect(feedback.phase).toBe('idle')
  })

  it('keeps begin, succeed and fail stable while the host\'s work comes and goes', () => {
    // The native drop-target registration depends on the handler's identity.
    render()
    const { begin, succeed, fail } = feedback
    render('Validating plan.jmx…')
    render(undefined)
    expect(feedback.begin).toBe(begin)
    expect(feedback.succeed).toBe(succeed)
    expect(feedback.fail).toBe(fail)
  })
})

describe('describeWorking', () => {
  it('words the host\'s work like the other drop labels', () => {
    expect(describeWorking('Validating', 'plan.jmx')).toBe('Validating plan.jmx…')
    expect(describeWorking('Inspecting', '12 files')).toBe('Inspecting 12 files…')
  })
})
