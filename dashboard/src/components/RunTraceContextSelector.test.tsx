// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import RunTraceContextSelector from './RunTraceContextSelector'
import {
  parseTraceContextOrigins,
  traceContextErrors,
  traceContextPayload,
  traceContextSummary,
  type RunTraceContextSettings,
} from './runTraceContext'

describe('trace context helpers', () => {
  it('parses one origin per line, trims trailing slashes and drops duplicates', () => {
    expect(parseTraceContextOrigins(' https://a.test/ \n\nhttps://b.test,https://a.test')).toEqual(['https://a.test', 'https://b.test'])
  })

  it('reports origins the controller would reject', () => {
    expect(traceContextErrors(undefined)).toEqual([])
    expect(traceContextErrors({ enabled: false, origins: ['not a url'] })).toEqual([])
    expect(traceContextErrors({ enabled: true, origins: [] })).toEqual(['Add at least one approved origin.'])
    const errors = traceContextErrors({ enabled: true, origins: ['https://user@a.test', 'https://a.test/api', 'ftp://a.test', 'https://ok.test'] })
    expect(errors).toHaveLength(3)
    expect(traceContextErrors({ transaction_marker: true, enabled: false, origins: Array.from({ length: 33 }, (_, i) => `https://h${i}.test`) }))
      .toContain('At most 32 approved origins are allowed.')
  })

  it('omits the setting from the payload unless a carrier is on', () => {
    expect(traceContextPayload({ enabled: false, origins: ['https://a.test'] })).toBeUndefined()
    expect(traceContextPayload({ enabled: true, origins: ['https://a.test'] })).toEqual({ enabled: true, transaction_marker: false, origins: ['https://a.test'] })
    expect(traceContextSummary(undefined)).toBe('Off for this run')
    expect(traceContextSummary({ enabled: true, transaction_marker: true, origins: ['https://a.test'] })).toBe('traceparent and transaction markers to 1 approved origin')
  })
})

describe('RunTraceContextSelector', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    ;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  function render(value: RunTraceContextSettings | undefined, onChange = vi.fn(), suggest = false) {
    act(() => root.render(<RunTraceContextSelector value={value} onChange={onChange} suggest={suggest} />))
    return onChange
  }

  it('suggests trace context when monitoring capture is on', () => {
    render(undefined, vi.fn(), true)
    expect(container.textContent).toContain('Monitoring capture is on for this run')
    expect(container.querySelector('textarea')).toBeNull()
  })

  it('says root-cause exemplars need trace context until it is on', () => {
    render(undefined)
    expect(container.textContent).toContain('Root-cause exemplars and their trace links need W3C trace context')
    render({ enabled: true, origins: ['https://shop.example.com'] })
    expect(container.textContent).not.toContain('Root-cause exemplars')
  })

  it('keeps typed text while reporting parsed origins', () => {
    const onChange = render({ enabled: true, origins: [] })
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement
    expect(textarea).toBeTruthy()
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
      setter?.call(textarea, 'https://shop.test/\n')
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onChange).toHaveBeenLastCalledWith({ enabled: true, transaction_marker: false, origins: ['https://shop.test'] })
    expect(textarea.value).toBe('https://shop.test/\n')
  })

  it('shows validation errors for rejected origins', () => {
    render({ enabled: true, origins: ['https://shop.test/api'] })
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('origin only')
  })
})
