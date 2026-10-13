// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it } from 'vitest'

import type { BrowserReplayEvent } from '../types/replay'
import LiveTableStepResult from './LiveTableStepResult'

let container: HTMLDivElement
let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(async () => { await act(async () => root.unmount()); container.remove() })
const event = (extra: Partial<BrowserReplayEvent>): BrowserReplayEvent => ({ phase: 'action', action_index: 0, action_name: 'Take', action_type: 'live_table', target: '', success: true, started_at: '', completed_at: '', duration_ms: 1, ...extra })
const action = { name: 'Take', type: 'live_table', target: '', params: { alias: 'work', operation: 'pop', result_prefix: 'taken' } }

it('shows the row a step got, with its metadata, empty and absent cells apart', async () => {
  await act(async () => root.render(<LiveTableStepResult action={action} event={event({ extractions: { 'taken.status': 'ok', 'taken.revision': '3', 'taken.record_id': 'r1', 'taken.record_version': '1', 'taken.count': '1', 'taken.values.id': 'a', 'taken.values.note': '' } })} />))
  expect([...container.querySelectorAll('dt')].map(node => node.textContent)).toEqual(['Status', 'Record ID', 'Version', 'Table revision', 'Count'])
  // A zero count beside one row is left out.
  await act(async () => root.render(<LiveTableStepResult action={action} event={event({ extractions: { 'taken.status': 'ok', 'taken.revision': '3', 'taken.record_id': 'r1', 'taken.count': '0', 'taken.values.id': 'a' } })} />))
  expect([...container.querySelectorAll('dt')].map(node => node.textContent)).toEqual(['Status', 'Record ID', 'Table revision'])
  expect(container.querySelector('table[aria-label="Row for Take"]')).not.toBeNull()
  expect(container.textContent).toContain('saved as taken')
})

it('explains a failure beside the step and offers no retry', async () => {
  await act(async () => root.render(<LiveTableStepResult action={action} event={event({ success: false, error_message: 'live table operation outcome is unknown' })} />))
  expect(container.querySelector('[role="note"]')!.textContent).toContain('running it again could make the change twice')
  expect(container.textContent).toContain('A failed step clears its results.')
  expect(container.querySelector('button')).toBeNull()
})

it('says how to see a step’s row when it saves none', async () => {
  await act(async () => root.render(<LiveTableStepResult action={{ ...action, params: { alias: 'work', operation: 'push' } }} event={event({})} />))
  expect(container.textContent).toContain('Name where it saves them (Save results as) to see its row here.')
})
