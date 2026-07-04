// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { CorrelationDiagnosePanel } from './CorrelationDiagnosePanel'
import type { CorrelationDiagnoseResponse, ScriptReplayActionEvent } from '../types/scripts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const events = [
  { phase: 'action', action_index: 0, action_name: 'login', action_type: 'POST', target: '/login', started_at: '', completed_at: '', duration_ms: 1, success: true,
    snapshot: { request_url: '/login', response_body: '{"t":"new"}' } },
] as ScriptReplayActionEvent[]

const diagnoseResponse: CorrelationDiagnoseResponse = {
  blocking: true,
  counts: { errors: 1, warnings: 0 },
  suggestions: [{
    rule_id: 'json.auth_token',
    severity: 'error',
    title: 'Stale token replayed',
    detail: 'Step 2 replays a token that rotated at step 1.',
    source_step_id: 0,
    failing_step_id: 1,
    suggestion: 'Extract the token from step 1.',
    recorded_value: 'OLD', replay_value: 'NEW',
    confidence: 0.92, confidence_band: 'high', review_action: 'auto_apply_available',
    extractor: { name: 'csrf', source: 'body', extract_type: 'jsonpath', expression: '$.t' },
  }],
}

describe('CorrelationDiagnosePanel', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    vi.clearAllMocks()
  })

  it('runs a baseline diagnosis and applies a suggested extractor to the source action', async () => {
    const diagnoseWithBaseline = vi.fn().mockResolvedValue(diagnoseResponse)
    const onAddExtractor = vi.fn()

    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          events={events}
          recordingStatus={{ exists: true, request_count: 2 }}
          actionCount={2}
          diagnoseWithBaseline={diagnoseWithBaseline}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={onAddExtractor}
        />,
      )
    })

    const diagnoseButton = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Diagnose correlations')!
    await act(async () => { diagnoseButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    expect(diagnoseWithBaseline).toHaveBeenCalledWith('s1', expect.arrayContaining([expect.objectContaining({ id: 0 })]))
    expect(container.textContent).toContain('Stale token replayed')
    expect(container.textContent).toContain('OLD')
    expect(container.textContent).toContain('NEW')

    const addButton = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Add as extractor')!
    await act(async () => { addButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    expect(onAddExtractor).toHaveBeenCalledWith(
      0,
      { name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.t' },
      { failingActionIndex: 1, recordedValue: 'OLD', replacement: '{{var.csrf}}' },
    )
    expect(container.textContent).toContain('Added')
  })

  it('offers a HAR upload when no baseline exists', async () => {
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId={null}
          events={events}
          recordingStatus={{ exists: false }}
          actionCount={1}
          diagnoseWithBaseline={vi.fn()}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={vi.fn()}
        />,
      )
    })
    expect(container.textContent).toContain('Diagnose from recording (HAR)')
    expect(container.textContent).toContain('No stored baseline')
    expect(container.querySelector('input[type="file"]')).not.toBeNull()
  })
})
