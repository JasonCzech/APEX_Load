// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { BrowserReplayEvent } from '../types/replay'
import type { ScriptDefinition } from '../types/scripts'
import InspectorCorrelationTable, { type InspectorCorrelationTableProps } from './InspectorCorrelationTable'

const script: ScriptDefinition = {
  name: 'Store', version: '1.0', protocol: 'http', config: { base_url: 'https://api.example.test' },
  actions: [
    { name: 'Login', type: 'POST', target: '/login', headers: { 'X-Csrf': '{{var.csrf}}' }, extractors: [{ name: 'token', source: '', type: 'regex', expression: 'token=(\\w+)' }] },
    { name: 'Me', type: 'GET', target: '/me?t={{var.token}}' },
  ],
}

function event(overrides: Partial<BrowserReplayEvent> = {}): BrowserReplayEvent {
  return {
    phase: 'action', action_index: 0, action_name: 'Login', action_type: 'request', method: 'POST', target: '/login',
    success: true, started_at: '2026-11-10T12:00:00Z', completed_at: '2026-11-10T12:00:01Z', duration_ms: 10,
    ...overrides,
  } as BrowserReplayEvent
}

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

function render(props: Partial<InspectorCorrelationTableProps> = {}) {
  act(() => root.render(<InspectorCorrelationTable event={event()} variableDiff={{}} script={null} copyKeyPrefix="0:correlation" copiedKey={null} onCopy={() => undefined} {...props} />))
}

const cells = (row: Element) => [...row.querySelectorAll('th, td')].map(cell => cell.textContent)

describe('InspectorCorrelationTable', () => {
  it('renders a captioned table with column and row headers', () => {
    render({ event: event({ extractions: { token: 'abc' } }), variableDiff: { token: { previous: 'old', next: 'abc' } }, script })
    const table = container.querySelector('table')!
    expect(table.querySelector('caption')?.textContent).toBe('Values this action extracts, reads and changes')
    expect([...table.querySelectorAll('thead th')].map(th => [th.textContent, th.getAttribute('scope')])).toEqual([
      ['Direction', 'col'], ['Variable', 'col'], ['Where', 'col'], ['Value (before → after)', 'col'], ['Result', 'col'], ['Copy', 'col'],
    ])
    const rows = [...table.querySelectorAll('tbody tr')]
    expect(rows[0].querySelector('th')?.getAttribute('scope')).toBe('row')
    // Copy is an icon button; its name carries the word.
    expect(cells(rows[0])).toEqual(['Extracts', 'token', 'body · regex', 'old→changed toabc', 'ExtractedUsed by 1 later step', ''])
    expect(rows[0].querySelector('button')?.getAttribute('aria-label')).toBe('Copy token value')
    // The variable cell carries the direction for the narrow layout, which drops the Direction column.
    expect(rows[0].querySelector('th')?.getAttribute('data-direction')).toBe('Extracts')
    // The authored request's read of csrf has no recorded resolution: one quiet line, not a row of Unknowns.
    expect(rows).toHaveLength(1)
    expect(container.querySelector('.rr-correlation-unknown-reads')?.textContent).toBe('Reads {{var.csrf}} (resolution not recorded)')
    // No horizontal scroll box: Result stays in the pane, and nothing needs a keyboard-unreachable scroll.
    expect(container.querySelector('.rr-correlation-scroll')).toBeNull()
  })

  it('copies a value from its row, and offers no Copy for a masked or redacted one', () => {
    const onCopy = vi.fn()
    render({
      event: event({ extractions: { token: 'abc', secret: '[masked]', auth: 'Bearer [REDACTED]' } }),
      variableDiff: { changed: { previous: 'x', next: 'y' } },
      onCopy,
      copiedKey: '0:correlation:set:changed',
    })
    const rowFor = (variable: string) => [...container.querySelectorAll('tbody tr')].find(row => row.querySelector('th')?.textContent === variable)!
    expect(rowFor('secret').querySelector('button')).toBeNull()
    expect(rowFor('auth').querySelector('button')).toBeNull()
    const copy = rowFor('token').querySelector('button')!
    expect(copy.getAttribute('aria-label')).toBe('Copy token value')
    act(() => copy.click())
    expect(onCopy).toHaveBeenCalledWith('0:correlation:extract:token', 'abc')
    const copied = rowFor('changed').querySelector('button')!
    expect(copied.getAttribute('aria-label')).toBe('Copied changed value')
    expect(copied.getAttribute('title')).toBe('Copied')
    expect(copy.getAttribute('title')).toBe('Copy value')
    // The icon is decorative: the name is the only text.
    expect(copied.querySelector('svg')?.getAttribute('aria-hidden')).toBe('true')
    expect(copied.textContent).toBe('')
  })

  it('never claims Resolved while runtime evidence is withheld', () => {
    const failed = event({ success: false, error_message: 'boom', diagnosis: { category: 'correlation', severity: 'error', cause: 'x', unresolved_variables: ['other'] } })
    render({ event: failed, script })
    const csrf = () => [...container.querySelectorAll('tbody tr')].find(row => row.querySelector('th')?.textContent === 'csrf')
    expect(csrf()?.querySelector('.rr-correlation-badge')?.textContent).toBe('Resolved')
    render({ event: failed, script, runtimeVariablesUnavailable: true })
    expect(csrf()).toBeUndefined()
    expect(container.querySelector('.rr-correlation-unknown-reads')?.textContent).toBe('Reads {{var.csrf}} (resolution not recorded)')
  })

  it('cuts long values to one line with the full text on hover, and leaves masked values as they came', () => {
    const long = 'x'.repeat(2000)
    render({ event: event({ extractions: { big: long, secret: '[masked]' } }) })
    const values = [...container.querySelectorAll('.rr-correlation-value .rr-correlation-one-line')]
    expect(values[0].getAttribute('title')).toBe(long)
    expect(values[1].textContent).toBe('[masked]')
    // Without the script Where is a quiet Unknown and later uses are left out.
    expect(container.querySelector('.rr-correlation-where')?.textContent).toBe('Unknown')
    expect(container.querySelector('.rr-correlation-later')).toBeNull()
  })

  it('shows a failing extractor and an unset read as failures in words, not colour alone', () => {
    render({
      event: event({ success: false, error_message: 'extractor "token" found no match', diagnosis: { category: 'correlation', severity: 'error', cause: 'x', unresolved_variables: ['csrf'] } }),
      script,
    })
    const rows = [...container.querySelectorAll('tbody tr')]
    expect(cells(rows[0])).toEqual(['Extracts', 'token', 'body · regex', 'No value', 'No matchUsed by 1 later step', ''])
    expect(cells(rows[1])).toEqual(['Reads', 'csrf', 'Header X-Csrf', 'Left as {{var.csrf}}', 'Never set this run', ''])
  })

  it('keeps every retention note and the streamed children', () => {
    render({
      event: event({
        credential_classification_error: 'timeout',
        unapplied_extractions_omitted_count: 1,
        streamed_children: [{ action_name: 'Chunk', success: false, error_message: 'boom' }],
        streamed_children_omitted_count: 2,
      }),
    })
    const statuses = [...container.querySelectorAll('[role="status"]')].map(node => node.textContent)
    expect(statuses).toEqual([
      'Credential classification: timeout',
      '1 not applied extraction was omitted from the retained debugger evidence.',
      '2 streamed child events were omitted from retained evidence.',
    ])
    expect(container.querySelector('.rr-correlation-children li')?.textContent).toBe('Chunk · Failedboom')
    expect(container.querySelector('table')).toBeNull()
  })

  it('says nothing was recorded, unless told not to', () => {
    render()
    expect(container.textContent).toBe('No extractions or variable changes were recorded for this action.')
    render({ showEmptyState: false })
    expect(container.textContent).toBe('')
  })
})
