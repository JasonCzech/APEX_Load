// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { CorrelationDiagnosePanel } from './CorrelationDiagnosePanel'
import type { CorrelationDiagnoseResponse, ScriptActionDefinition, ScriptExtractorDefinition, ScriptReplayActionEvent } from '../types/scripts'
import { ApiError } from '../hooks/apiClient'
import { addCorrelationExtractorIfMissing, applyCorrelationRepairToAction, type CorrelationExtractorRepair } from './scriptCorrelationRepair'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const events = [
  { phase: 'action', action_index: 0, action_name: 'login', action_type: 'POST', target: '/login', started_at: '', completed_at: '', duration_ms: 1, success: true,
    snapshot: { request_url: '/login', response_body: '{"t":"new"}' } },
  { phase: 'action', action_index: 1, action_name: 'submit', action_type: 'POST', target: '/submit', started_at: '', completed_at: '', duration_ms: 1, success: false,
    snapshot: { request_url: '/submit', request_body: 'csrf=OLD', response_status: 403 } },
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
    repair_locations: [{ field: 'request_body', start: 5, end: 8, editable_surface_exact: true }],
    confidence: 0.92, confidence_band: 'high', review_action: 'auto_apply_available',
    extractor: { name: 'csrf', source: 'body', extract_type: 'jsonpath', expression: '$.t', on_miss: 'fail' },
  }],
}

const appliedResult = { extractorAdded: true, extractorAvailable: true, requestRepaired: true }
const extractorOnlyResult = { extractorAdded: true, extractorAvailable: true, requestRepaired: false }

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
    vi.unstubAllGlobals()
  })

  it('runs a baseline diagnosis and applies a suggested extractor to the source action', async () => {
    const diagnoseWithBaseline = vi.fn().mockResolvedValue(diagnoseResponse)
    const onAddExtractor = vi.fn().mockReturnValue(appliedResult)

    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript
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

    expect(diagnoseWithBaseline).toHaveBeenCalledWith('s1', expect.arrayContaining([expect.objectContaining({ id: 0 })]), 'replay-1')
    expect(container.textContent).toContain('Stale token replayed')
    expect(container.textContent).not.toContain('OLD')

    const addButton = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Add as extractor')!
    await act(async () => { addButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    expect(onAddExtractor).toHaveBeenCalledWith(
      0,
      { name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.t', on_miss: 'fail' },
      { failingActionIndex: 1, requestLocations: [{ field: 'request_body', start: 5, end: 8 }], replacement: '{{var.csrf}}' },
    )
    expect(container.textContent).toContain('Fix applied')
  })

  it('does not apply the same suggestion twice on rapid clicks', async () => {
    const onAddExtractor = vi.fn().mockReturnValue(appliedResult)
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript
          events={events}
          recordingStatus={{ exists: true, request_count: 2 }}
          actionCount={2}
          diagnoseWithBaseline={vi.fn().mockResolvedValue(diagnoseResponse)}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={onAddExtractor}
        />,
      )
    })
    await act(async () => {
      Array.from(container.querySelectorAll('button'))
        .find(button => button.textContent === 'Diagnose correlations')
        ?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const addButton = Array.from(container.querySelectorAll('button'))
      .find(button => button.textContent === 'Add as extractor')!
    act(() => {
      addButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      addButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onAddExtractor).toHaveBeenCalledTimes(1)
  })

  it('does not pretend a redacted baseline literal was repaired', async () => {
    const diagnoseWithBaseline = vi.fn().mockResolvedValue({
      ...diagnoseResponse,
      suggestions: [{
        ...diagnoseResponse.suggestions[0],
        recorded_value: undefined,
        replay_value: undefined,
        repair_locations: undefined,
        review_action: 'manual_value_replacement_required',
      }],
    })
    const onAddExtractor = vi.fn().mockReturnValue(extractorOnlyResult)

    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript
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
    expect(container.textContent).toContain('recorded value is hidden')

    const addButton = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Add as extractor')!
    await act(async () => { addButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    expect(onAddExtractor).toHaveBeenCalledWith(
      0,
      { name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.t', on_miss: 'fail' },
      undefined,
    )
    expect(container.textContent).toContain('Extractor added')
  })

  it('does not diagnose a stored baseline without a trusted raw replay session', async () => {
    const diagnoseWithBaseline = vi.fn().mockResolvedValue(diagnoseResponse)
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replayTerminal
          replayMatchesCurrentScript
          events={events}
          recordingStatus={{ exists: true, request_count: 2 }}
          actionCount={2}
          diagnoseWithBaseline={diagnoseWithBaseline}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={vi.fn()}
        />,
      )
    })
    const diagnoseButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Diagnose correlations')!
    expect(diagnoseButton.disabled).toBe(true)
    await act(async () => { diagnoseButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(diagnoseWithBaseline).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Run a new saved-script replay')
  })

  it('tracks fixes for different failing actions independently', async () => {
    const diagnoseWithBaseline = vi.fn().mockResolvedValue({
      ...diagnoseResponse,
      suggestions: [
        diagnoseResponse.suggestions[0],
        { ...diagnoseResponse.suggestions[0], failing_step_id: 2, title: 'Second stale use' },
      ],
    })

    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript
          events={events}
          recordingStatus={{ exists: true, request_count: 3 }}
          actionCount={3}
          diagnoseWithBaseline={diagnoseWithBaseline}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={vi.fn().mockReturnValue(appliedResult)}
        />,
      )
    })

    const diagnoseButton = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Diagnose correlations')!
    await act(async () => { diagnoseButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const addButtons = Array.from(container.querySelectorAll('button')).filter(b => b.textContent === 'Add as extractor')
    expect(addButtons).toHaveLength(2)
    await act(async () => { addButtons[0].dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(Array.from(container.querySelectorAll('button')).filter(b => b.textContent === 'Add as extractor')).toHaveLength(1)
  })

  it('applies two suggestions in a row from one diagnosis and keeps both applied statuses', async () => {
    const diagnoseWithBaseline = vi.fn().mockResolvedValue({
      ...diagnoseResponse,
      suggestions: [
        diagnoseResponse.suggestions[0],
        { ...diagnoseResponse.suggestions[0], failing_step_id: 2, title: 'Second stale use' },
      ],
    })
    const onAddExtractor = vi.fn().mockReturnValue(appliedResult)
    const threeStepEvents = [
      ...events,
      { ...events[1], action_index: 2, action_name: 'confirm', target: '/confirm' },
    ] as ScriptReplayActionEvent[]
    const renderPanel = (currentEditableActionsKey: string, replayMatchesCurrentScript: boolean) => root.render(
      <CorrelationDiagnosePanel
        scriptId="s1"
        replaySessionId="replay-1"
        replayTerminal
        replayMatchesCurrentScript={replayMatchesCurrentScript}
        events={threeStepEvents}
        diagnosisKey="replay-1/replay-snapshot-1"
        currentEditableActionsKey={currentEditableActionsKey}
        recordingStatus={{ exists: true, request_count: 3 }}
        actionCount={3}
        diagnoseWithBaseline={diagnoseWithBaseline}
        diagnoseWithUpload={vi.fn()}
        onAddExtractor={onAddExtractor}
      />,
    )

    await act(async () => { renderPanel('draft-1', true) })
    const diagnoseButton = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Diagnose correlations')!
    await act(async () => { diagnoseButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const addButtons = Array.from(container.querySelectorAll('button')).filter(b => b.textContent === 'Add as extractor')
    expect(addButtons).toHaveLength(2)

    await act(async () => { addButtons[0].dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    // The apply edited the draft: new editable-actions key, and the finished
    // replay no longer matches the current script.
    await act(async () => { renderPanel('draft-2', false) })

    // The presented diagnosis survives the panel-driven edit.
    expect(container.textContent).toContain('Stale token replayed')
    expect(container.textContent).toContain('Second stale use')
    expect(container.textContent).toContain('remaining suggestions stay applicable')
    const remaining = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).filter(b => b.textContent === 'Add as extractor')
    expect(remaining).toHaveLength(1)
    expect(remaining[0].disabled).toBe(false)

    await act(async () => { remaining[0].dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await act(async () => { renderPanel('draft-3', false) })

    expect(onAddExtractor).toHaveBeenCalledTimes(2)
    expect(onAddExtractor).toHaveBeenNthCalledWith(1, 0, expect.anything(), expect.objectContaining({ failingActionIndex: 1 }))
    expect(onAddExtractor).toHaveBeenNthCalledWith(2, 0, expect.anything(), expect.objectContaining({ failingActionIndex: 2 }))
    expect(Array.from(container.querySelectorAll('button')).filter(b => b.textContent === 'Fix applied')).toHaveLength(2)
  })

  it('disables remaining applies after an external edit without destroying the diagnosis', async () => {
    const diagnoseWithBaseline = vi.fn().mockResolvedValue({
      ...diagnoseResponse,
      suggestions: [
        diagnoseResponse.suggestions[0],
        { ...diagnoseResponse.suggestions[0], failing_step_id: 2, title: 'Second stale use' },
      ],
    })
    const onAddExtractor = vi.fn().mockReturnValue(appliedResult)
    const renderPanel = (currentEditableActionsKey: string, replayMatchesCurrentScript: boolean) => root.render(
      <CorrelationDiagnosePanel
        scriptId="s1"
        replaySessionId="replay-1"
        replayTerminal
        replayMatchesCurrentScript={replayMatchesCurrentScript}
        events={events}
        diagnosisKey="replay-1/replay-snapshot-1"
        currentEditableActionsKey={currentEditableActionsKey}
        recordingStatus={{ exists: true, request_count: 3 }}
        actionCount={3}
        diagnoseWithBaseline={diagnoseWithBaseline}
        diagnoseWithUpload={vi.fn()}
        onAddExtractor={onAddExtractor}
      />,
    )

    await act(async () => { renderPanel('draft-1', true) })
    const diagnoseButton = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Diagnose correlations')!
    await act(async () => { diagnoseButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const addButtons = Array.from(container.querySelectorAll('button')).filter(b => b.textContent === 'Add as extractor')
    await act(async () => { addButtons[0].dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await act(async () => { renderPanel('draft-2', false) })

    // A draft change the panel did not cause disables further applies but
    // keeps the presented list and the first suggestion's applied status.
    await act(async () => { renderPanel('draft-external', false) })
    expect(container.textContent).toContain('changed outside this diagnosis')
    expect(container.textContent).toContain('Stale token replayed')
    expect(container.textContent).toContain('Fix applied')
    const remaining = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).filter(b => b.textContent === 'Add as extractor')
    expect(remaining).toHaveLength(1)
    expect(remaining[0].disabled).toBe(true)
    await act(async () => { remaining[0].dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(onAddExtractor).toHaveBeenCalledTimes(1)
  })

  it('falls back to extractor-only when a later suggestion targets an already repaired action', async () => {
    const diagnoseWithBaseline = vi.fn().mockResolvedValue({
      ...diagnoseResponse,
      suggestions: [
        diagnoseResponse.suggestions[0],
        { ...diagnoseResponse.suggestions[0], title: 'Same action, second value', extractor: { ...diagnoseResponse.suggestions[0].extractor!, name: 'csrf_b', expression: '$.u' } },
      ],
    })
    const onAddExtractor = vi.fn().mockReturnValue(appliedResult)
    const renderPanel = (currentEditableActionsKey: string, replayMatchesCurrentScript: boolean) => root.render(
      <CorrelationDiagnosePanel
        scriptId="s1"
        replaySessionId="replay-1"
        replayTerminal
        replayMatchesCurrentScript={replayMatchesCurrentScript}
        events={events}
        diagnosisKey="replay-1/replay-snapshot-1"
        currentEditableActionsKey={currentEditableActionsKey}
        recordingStatus={{ exists: true, request_count: 2 }}
        actionCount={2}
        diagnoseWithBaseline={diagnoseWithBaseline}
        diagnoseWithUpload={vi.fn()}
        onAddExtractor={onAddExtractor}
      />,
    )

    await act(async () => { renderPanel('draft-1', true) })
    const diagnoseButton = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Diagnose correlations')!
    await act(async () => { diagnoseButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const addButtons = Array.from(container.querySelectorAll('button')).filter(b => b.textContent === 'Add as extractor')
    expect(addButtons).toHaveLength(2)
    await act(async () => { addButtons[0].dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await act(async () => { renderPanel('draft-2', false) })

    const remaining = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).filter(b => b.textContent === 'Add as extractor')
    await act(async () => { remaining[0].dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    // The first apply rewrote failing action 1, so the second suggestion's
    // offsets (computed against the pre-repair content) must not be replayed.
    expect(onAddExtractor).toHaveBeenNthCalledWith(1, 0, expect.anything(), expect.objectContaining({ failingActionIndex: 1 }))
    expect(onAddExtractor).toHaveBeenNthCalledWith(2, 0, expect.objectContaining({ name: 'csrf_b' }), undefined)
  })

  it('does not claim a request repair when only the extractor was added', async () => {
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript
          events={events}
          recordingStatus={{ exists: true, request_count: 2 }}
          actionCount={2}
          diagnoseWithBaseline={vi.fn().mockResolvedValue(diagnoseResponse)}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={vi.fn().mockReturnValue(extractorOnlyResult)}
        />,
      )
    })
    const diagnoseButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Diagnose correlations')!
    await act(async () => { diagnoseButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const addButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Add as extractor')!
    await act(async () => { addButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    expect(container.textContent).toContain('Extractor added only')
    expect(container.textContent).toContain('mapped editable request span could not be updated')
    expect(container.textContent).not.toContain('Fix applied')
  })

  it('reports extractor-only when an Aura template contract blocks automatic body replacement', async () => {
    let sourceAction: ScriptActionDefinition = { name: 'login', type: 'POST', target: '/login' }
    const originalRequestAction: ScriptActionDefinition = {
      name: 'submit', type: 'POST', target: '/submit', body: 'csrf=OLD',
      metadata: { body_aura_template_contract: { version: 1, form_wire_occurrences: [], literal_occurrences: [] } },
    }
    let requestAction = originalRequestAction
    const onAddExtractor = vi.fn((_index: number, extractor: ScriptExtractorDefinition, repair?: CorrelationExtractorRepair) => {
      const previousSource = sourceAction
      sourceAction = addCorrelationExtractorIfMissing(sourceAction, extractor)
      const previousRequest = requestAction
      if (repair) requestAction = applyCorrelationRepairToAction(requestAction, repair)
      return {
        extractorAdded: sourceAction !== previousSource,
        extractorAvailable: true,
        requestRepaired: requestAction !== previousRequest,
      }
    })
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript
          events={events}
          recordingStatus={{ exists: true, request_count: 2 }}
          actionCount={2}
          diagnoseWithBaseline={vi.fn().mockResolvedValue(diagnoseResponse)}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={onAddExtractor}
        />,
      )
    })
    const diagnoseButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Diagnose correlations')!
    await act(async () => { diagnoseButton.click() })
    const addButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Add as extractor')!
    await act(async () => { addButton.click() })

    expect(sourceAction.extractors).toEqual([{ name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.t', on_miss: 'fail' }])
    expect(requestAction).toBe(originalRequestAction)
    expect(container.textContent).toContain('Extractor added only')
    expect(container.textContent).not.toContain('Fix applied')
  })

  it('adds only the extractor when the server cannot prove the wire offsets match the editable surface', async () => {
    const onAddExtractor = vi.fn().mockReturnValue(extractorOnlyResult)
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript
          events={events}
          recordingStatus={{ exists: true, request_count: 2 }}
          actionCount={2}
          diagnoseWithBaseline={vi.fn().mockResolvedValue({
            ...diagnoseResponse,
            suggestions: [{
              ...diagnoseResponse.suggestions[0],
              repair_locations: [
                { field: 'request_body', start: 5, end: 8, editable_surface_exact: true },
                { field: 'request_url', start: 1, end: 4 },
              ],
            }],
          })}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={onAddExtractor}
        />,
      )
    })
    const diagnoseButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Diagnose correlations')!
    await act(async () => { diagnoseButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const addButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Add as extractor')!
    await act(async () => { addButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(onAddExtractor).toHaveBeenCalledWith(
      0,
      { name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.t', on_miss: 'fail' },
      undefined,
    )
    expect(container.textContent).toContain('does not have raw evidence for a safe automatic request replacement')
  })

  it('invalidates suggestions when the replay generation changes', async () => {
    const onAddExtractor = vi.fn().mockReturnValue(appliedResult)
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript
          events={events}
          diagnosisKey="replay-1/script-revision-1"
          recordingStatus={{ exists: true, request_count: 2 }}
          actionCount={2}
          diagnoseWithBaseline={vi.fn().mockResolvedValue(diagnoseResponse)}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={onAddExtractor}
        />,
      )
    })
    const diagnoseButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Diagnose correlations')!
    await act(async () => { diagnoseButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(container.textContent).toContain('Stale token replayed')

    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript
          events={events}
          diagnosisKey="replay-1/script-revision-2"
          recordingStatus={{ exists: true, request_count: 2 }}
          actionCount={2}
          diagnoseWithBaseline={vi.fn().mockResolvedValue(diagnoseResponse)}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={onAddExtractor}
        />,
      )
    })

    expect(container.textContent).not.toContain('Stale token replayed')
    expect(container.textContent).toContain('Diagnose correlations')
    expect(onAddExtractor).not.toHaveBeenCalled()
  })

  it('discards a diagnosis response that finishes after the replay changes', async () => {
    let resolveDiagnosis: (response: CorrelationDiagnoseResponse) => void = () => undefined
    const pendingDiagnosis = new Promise<CorrelationDiagnoseResponse>(resolve => { resolveDiagnosis = resolve })
    const diagnoseWithBaseline = vi.fn().mockReturnValue(pendingDiagnosis)
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-2"
          replayTerminal
          replayMatchesCurrentScript
          events={events}
          diagnosisKey="replay-1/script-revision-1"
          recordingStatus={{ exists: true, request_count: 2 }}
          actionCount={2}
          diagnoseWithBaseline={diagnoseWithBaseline}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={vi.fn().mockReturnValue(appliedResult)}
        />,
      )
    })
    const diagnoseButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Diagnose correlations')!
    await act(async () => { diagnoseButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript
          events={events}
          diagnosisKey="replay-2/script-revision-1"
          recordingStatus={{ exists: true, request_count: 2 }}
          actionCount={2}
          diagnoseWithBaseline={diagnoseWithBaseline}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={vi.fn().mockReturnValue(appliedResult)}
        />,
      )
      resolveDiagnosis(diagnoseResponse)
      await pendingDiagnosis
    })

    expect(container.textContent).not.toContain('Stale token replayed')
    expect(container.textContent).toContain('Diagnose correlations')
  })

  it('blocks diagnosis when replay observations share an action ID', async () => {
    const diagnoseWithBaseline = vi.fn()
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-2"
          replayTerminal
          replayMatchesCurrentScript
          events={[events[0], { ...events[0], action_name: 'second sequence request' }]}
          recordingStatus={{ exists: true, request_count: 2 }}
          actionCount={2}
          diagnoseWithBaseline={diagnoseWithBaseline}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={vi.fn().mockReturnValue(appliedResult)}
        />,
      )
    })
    const diagnoseButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Diagnose correlations')!
    expect(diagnoseButton.disabled).toBe(true)
    expect(container.textContent).toContain('repeated observations for action 1')
    await act(async () => { diagnoseButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(diagnoseWithBaseline).not.toHaveBeenCalled()
  })

  it('blocks diagnosis for a single nested replay leaf', async () => {
    const diagnoseWithBaseline = vi.fn()
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-2"
          replayTerminal
          replayMatchesCurrentScript
          events={[{ ...events[0], parent_action_name: 'sequence', request_index: 0, step_path: ['requests[0]'] }]}
          recordingStatus={{ exists: true, request_count: 1 }}
          actionCount={1}
          diagnoseWithBaseline={diagnoseWithBaseline}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={vi.fn().mockReturnValue(appliedResult)}
        />,
      )
    })
    const diagnoseButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Diagnose correlations')!
    expect(diagnoseButton.disabled).toBe(true)
    expect(container.textContent).toContain('nested request evidence for action 1')
    await act(async () => { diagnoseButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(diagnoseWithBaseline).not.toHaveBeenCalled()
  })

  it('describes a clean result as limited to the compared scope', async () => {
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript
          events={events}
          recordingStatus={{ exists: true, request_count: 900, correlation_request_count: 500, correlation_truncated: true }}
          actionCount={2}
          diagnoseWithBaseline={vi.fn().mockResolvedValue({
            blocking: false,
            counts: { errors: 0, warnings: 0 },
            suggestions: [],
            compared_recorded_steps: 500,
            total_recorded_steps: 900,
            truncated: true,
          })}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={vi.fn().mockReturnValue(appliedResult)}
        />,
      )
    })
    const diagnoseButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Diagnose correlations')!
    await act(async () => { diagnoseButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(container.textContent).toContain('No correlation issues detected in the compared recording/replay scope')
    expect(container.textContent).toContain('compared 500 of 900')
    expect(container.textContent).not.toContain('replay matches the recording')
  })

  it('distinguishes a recording-status error from an absent baseline and offers retry', async () => {
    const retry = vi.fn()
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript
          events={events}
          recordingStatus={null}
          recordingStatusState="error"
          onRetryRecordingStatus={retry}
          actionCount={2}
          diagnoseWithBaseline={vi.fn()}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={vi.fn().mockReturnValue(appliedResult)}
        />,
      )
    })
    expect(container.textContent).toContain('Could not check whether this script has a stored baseline')
    expect(container.textContent).not.toContain('No recorded baseline')
    const retryButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Retry')!
    await act(async () => { retryButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(retry).toHaveBeenCalledTimes(1)
  })

  it('deduplicates one extractor reused by multiple failing steps when saving a ruleset', async () => {
    const diagnoseWithBaseline = vi.fn().mockResolvedValue({
      ...diagnoseResponse,
      suggestions: [
        diagnoseResponse.suggestions[0],
        { ...diagnoseResponse.suggestions[0], failing_step_id: 2, title: 'Second stale use' },
      ],
    })
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)

    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript
          projectId="project-1"
          events={events}
          recordingStatus={{ exists: true, request_count: 3 }}
          actionCount={3}
          diagnoseWithBaseline={diagnoseWithBaseline}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={vi.fn()}
        />,
      )
    })
    const diagnoseButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Diagnose correlations')!
    await act(async () => { diagnoseButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save as ruleset')!
    await act(async () => { saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true })); await Promise.resolve() })

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const options = fetchMock.mock.calls[0][1] as RequestInit
    const payload = JSON.parse(String(options.body))
    expect(payload.rules).toHaveLength(1)
    expect(payload.rules[0]).toMatchObject({ name: 'csrf', expression: '$.t' })
  })

  it('does not create duplicate rulesets on rapid save clicks', async () => {
    const diagnoseWithBaseline = vi.fn().mockResolvedValue(diagnoseResponse)
    let resolveSave!: (response: Response) => void
    const fetchMock = vi.fn().mockReturnValue(new Promise(resolve => { resolveSave = resolve }))
    vi.stubGlobal('fetch', fetchMock)

    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript
          projectId="project-1"
          events={events}
          recordingStatus={{ exists: true, request_count: 2 }}
          actionCount={2}
          diagnoseWithBaseline={diagnoseWithBaseline}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={vi.fn()}
        />,
      )
    })
    const diagnoseButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Diagnose correlations')!
    await act(async () => { diagnoseButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save as ruleset')!

    act(() => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('Saving ruleset…')
    await act(async () => {
      resolveSave(new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }))
      await Promise.resolve()
    })
    expect(container.textContent).toContain('Saved as ruleset')
  })

  it('invalidates an in-flight ruleset save when the diagnosis is reset', async () => {
    let resolveSave!: (response: Response) => void
    vi.stubGlobal('fetch', vi.fn().mockReturnValue(new Promise(resolve => { resolveSave = resolve })))
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript
          projectId="project-1"
          events={events}
          recordingStatus={{ exists: true, request_count: 2 }}
          actionCount={2}
          diagnoseWithBaseline={vi.fn().mockResolvedValue(diagnoseResponse)}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={vi.fn()}
        />,
      )
    })
    await act(async () => {
      Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Diagnose correlations')?.click()
    })
    act(() => {
      Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save as ruleset')?.click()
    })
    expect(container.textContent).toContain('Saving ruleset…')
    act(() => {
      Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Reset')?.click()
    })
    expect(container.textContent).toContain('Diagnose correlations')

    await act(async () => {
      resolveSave(new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }))
      await Promise.resolve()
    })
    expect(container.textContent).not.toContain('Saved as ruleset')
    expect(container.textContent).not.toContain('Saving ruleset…')
  })

  it('does not resubmit a ruleset for the same evidence while a reset save is still in flight', async () => {
    let resolveFirstSave!: (response: Response) => void
    const fetchMock = vi.fn()
      .mockReturnValueOnce(new Promise(resolve => { resolveFirstSave = resolve }))
      .mockResolvedValue(new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    const diagnoseWithBaseline = vi.fn().mockResolvedValue(diagnoseResponse)
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript
          projectId="project-1"
          diagnosisKey="stable-evidence"
          events={events}
          recordingStatus={{ exists: true, request_count: 2 }}
          actionCount={2}
          diagnoseWithBaseline={diagnoseWithBaseline}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={vi.fn()}
        />,
      )
    })
    await act(async () => {
      Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Diagnose correlations')?.click()
    })
    act(() => {
      Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save as ruleset')?.click()
      Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Reset')?.click()
    })
    await act(async () => {
      Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Diagnose correlations')?.click()
    })
    act(() => {
      Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save as ruleset')?.click()
    })
    expect(fetchMock).toHaveBeenCalledTimes(1)

    await act(async () => {
      resolveFirstSave(new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }))
      await Promise.resolve()
    })
    await act(async () => {
      Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save as ruleset')?.click()
      await Promise.resolve()
    })
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('does not carry diagnosis or save completion across project scope changes', async () => {
    let resolveFirstSave!: (response: Response) => void
    const fetchMock = vi.fn()
      .mockReturnValueOnce(new Promise(resolve => { resolveFirstSave = resolve }))
      .mockResolvedValueOnce(new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    const diagnoseWithBaseline = vi.fn().mockResolvedValue(diagnoseResponse)
    const renderProject = async (projectId: string) => {
      await act(async () => {
        root.render(
          <CorrelationDiagnosePanel
            scriptId="s1"
            replaySessionId="replay-1"
            replayTerminal
            replayMatchesCurrentScript
            projectId={projectId}
            diagnosisKey="same-evidence-revision"
            events={events}
            recordingStatus={{ exists: true, request_count: 2 }}
            actionCount={2}
            diagnoseWithBaseline={diagnoseWithBaseline}
            diagnoseWithUpload={vi.fn()}
            onAddExtractor={vi.fn()}
          />,
        )
      })
    }

    await renderProject('project-1')
    await act(async () => {
      Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Diagnose correlations')?.click()
    })
    act(() => {
      Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save as ruleset')?.click()
    })
    await renderProject('project-2')
    expect(container.textContent).not.toContain('Stale token replayed')

    await act(async () => {
      resolveFirstSave(new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }))
      await Promise.resolve()
    })
    expect(container.textContent).not.toContain('Saved as ruleset')

    await act(async () => {
      Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Diagnose correlations')?.click()
    })
    await act(async () => {
      Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save as ruleset')?.click()
      await Promise.resolve()
    })
    expect(fetchMock).toHaveBeenCalledTimes(2)
    const secondPayload = JSON.parse(String((fetchMock.mock.calls[1][1] as RequestInit).body))
    expect(secondPayload.project_id).toBe('project-2')
  })

  it('offers a HAR upload when no baseline exists', async () => {
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId={null}
		  replayTerminal
		  replayMatchesCurrentScript
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
    expect(container.textContent).toContain('No recorded baseline')
	const input = container.querySelector<HTMLInputElement>('input[type="file"]')!
	const uploadButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Diagnose from recording'))!
	expect(uploadButton.tagName).toBe('BUTTON')
	expect(uploadButton.getAttribute('tabindex')).not.toBe('-1')
	expect(input.hidden).toBe(false)
	expect(input.tabIndex).toBe(-1)
	expect(input.classList.contains('correlation-diagnose-file-input')).toBe(true)
	const clickInput = vi.spyOn(input, 'click').mockImplementation(() => undefined)
	await act(async () => { uploadButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
	expect(clickInput).toHaveBeenCalledTimes(1)
  })

  it('disables HAR diagnosis until a terminal replay has request steps', async () => {
    const diagnoseWithUpload = vi.fn()
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId={null}
          replayTerminal
          replayMatchesCurrentScript
          events={[]}
          recordingStatus={{ exists: false }}
          actionCount={1}
          diagnoseWithBaseline={vi.fn()}
          diagnoseWithUpload={diagnoseWithUpload}
          onAddExtractor={vi.fn()}
        />,
      )
    })
    const input = container.querySelector<HTMLInputElement>('input[type="file"]')!
    expect(input.disabled).toBe(true)
    expect(container.textContent).toContain('Run a replay first')
    const file = { text: vi.fn().mockResolvedValue('{}') } as unknown as File
    Object.defineProperty(input, 'files', { configurable: true, value: [file] })
    await act(async () => {
      input.dispatchEvent(new Event('change', { bubbles: true }))
      await Promise.resolve()
    })
    expect(diagnoseWithUpload).not.toHaveBeenCalled()
  })

  it('keeps uploaded HAR diagnosis disclosure-safe even when a raw replay session is available', async () => {
    const diagnoseWithBaseline = vi.fn().mockResolvedValue({
      blocking: false,
      counts: { errors: 0, warnings: 0 },
      suggestions: [],
    })
    const diagnoseWithUpload = vi.fn().mockResolvedValue({
      blocking: false,
      counts: { errors: 0, warnings: 0 },
      suggestions: [],
      compared_recorded_steps: 1,
      total_recorded_steps: 1,
      truncated: false,
    })
    const file = {
      text: vi.fn().mockResolvedValue(JSON.stringify({
        log: { entries: [{ request: { method: 'GET', url: '/login' }, response: { status: 200 } }] },
      })),
    } as unknown as File
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript
          events={events}
          recordingStatus={{ exists: false }}
          actionCount={2}
          diagnoseWithBaseline={diagnoseWithBaseline}
          diagnoseWithUpload={diagnoseWithUpload}
          onAddExtractor={vi.fn().mockReturnValue(appliedResult)}
        />,
      )
    })
    const input = container.querySelector<HTMLInputElement>('input[type="file"]')!
    Object.defineProperty(input, 'files', { configurable: true, value: [file] })
    await act(async () => {
      input.dispatchEvent(new Event('change', { bubbles: true }))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(diagnoseWithBaseline).not.toHaveBeenCalled()
    expect(diagnoseWithUpload).toHaveBeenCalledWith(
      [expect.objectContaining({ id: 0, request_url: '/login' })],
      expect.arrayContaining([expect.objectContaining({ id: 0 })]),
    )
    expect(container.textContent).toContain('disclosure-safe only')
  })

  it('rejects oversized HAR uploads before reading or parsing the file', async () => {
    const diagnoseWithUpload = vi.fn()
    const text = vi.fn().mockResolvedValue('{}')
    const file = { size: 3 * 1024 * 1024, text } as unknown as File
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId={null}
          replayTerminal
          replayMatchesCurrentScript
          events={events}
          recordingStatus={{ exists: false }}
          actionCount={2}
          diagnoseWithBaseline={vi.fn()}
          diagnoseWithUpload={diagnoseWithUpload}
          onAddExtractor={vi.fn()}
        />,
      )
    })
    const input = container.querySelector<HTMLInputElement>('input[type="file"]')!
    Object.defineProperty(input, 'files', { configurable: true, value: [file] })
    await act(async () => {
      input.dispatchEvent(new Event('change', { bubbles: true }))
      await Promise.resolve()
    })

    expect(text).not.toHaveBeenCalled()
    expect(diagnoseWithUpload).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Upload a file no larger than 2 MiB')
  })

  it('blocks diagnosis until the replay session is terminal', async () => {
    const diagnoseWithBaseline = vi.fn()
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal={false}
          replayMatchesCurrentScript
          events={events}
          recordingStatus={{ exists: true, request_count: 2 }}
          actionCount={2}
          diagnoseWithBaseline={diagnoseWithBaseline}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={vi.fn()}
        />,
      )
    })
    const button = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(candidate => candidate.textContent === 'Diagnose correlations')!
    expect(button.disabled).toBe(true)
    expect(container.textContent).toContain('Wait for the replay session to finish')
    await act(async () => { button.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(diagnoseWithBaseline).not.toHaveBeenCalled()
  })

  it('blocks diagnosis after the editable action snapshot changes', async () => {
    const diagnoseWithBaseline = vi.fn()
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript={false}
          events={events}
          recordingStatus={{ exists: true, request_count: 2 }}
          actionCount={2}
          diagnoseWithBaseline={diagnoseWithBaseline}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={vi.fn()}
        />,
      )
    })
    const button = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(candidate => candidate.textContent === 'Diagnose correlations')!
    expect(button.disabled).toBe(true)
    expect(container.textContent).toContain('editable actions changed after this replay started')
    await act(async () => { button.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(diagnoseWithBaseline).not.toHaveBeenCalled()
  })

  it('treats zero compared steps as inconclusive', async () => {
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          replaySessionId="replay-1"
          replayTerminal
          replayMatchesCurrentScript
          events={events}
          recordingStatus={{ exists: true, request_count: 2 }}
          actionCount={2}
          diagnoseWithBaseline={vi.fn().mockResolvedValue({
            blocking: false,
            counts: { errors: 0, warnings: 0 },
            suggestions: [],
            compared_recorded_steps: 0,
            total_recorded_steps: 2,
            truncated: false,
          })}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={vi.fn()}
        />,
      )
    })
    const button = Array.from(container.querySelectorAll('button')).find(candidate => candidate.textContent === 'Diagnose correlations')!
    await act(async () => { button.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(container.textContent).toContain('inconclusive because no recorded request steps were compared')
    expect(container.textContent).not.toContain('No correlation issues detected')
    const retry = Array.from(container.querySelectorAll('button')).find(candidate => candidate.textContent === 'Try another diagnosis')!
    await act(async () => { retry.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(container.textContent).toContain('Diagnose correlations')
    expect(container.textContent).not.toContain('inconclusive because no recorded request steps were compared')
  })

  it('preserves bounded backend errors separately from HAR parse failures', async () => {
    const diagnoseWithUpload = vi.fn().mockRejectedValue(new ApiError('the script action graph changed; save, recapture the baseline, and replay before diagnosing again', 409))
    const file = {
      text: vi.fn().mockResolvedValue(JSON.stringify({
        log: { entries: [{ request: { method: 'GET', url: '/login' }, response: { status: 200 } }] },
      })),
    } as unknown as File
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId={null}
          replayTerminal
          replayMatchesCurrentScript
          events={events}
          recordingStatus={{ exists: false }}
          actionCount={2}
          diagnoseWithBaseline={vi.fn()}
          diagnoseWithUpload={diagnoseWithUpload}
          onAddExtractor={vi.fn()}
        />,
      )
    })
    const input = container.querySelector<HTMLInputElement>('input[type="file"]')!
    Object.defineProperty(input, 'files', { configurable: true, value: [file] })
    await act(async () => {
      input.dispatchEvent(new Event('change', { bubbles: true }))
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(container.textContent).toContain('Diagnosis failed: the script action graph changed; save, recapture the baseline, and replay before diagnosing again')
    expect(container.textContent).not.toContain('Could not read that file as a HAR recording')
  })

  it('labels invalid JSON as a HAR parse failure without calling the API', async () => {
    const diagnoseWithUpload = vi.fn()
    const file = { text: vi.fn().mockResolvedValue('{not-json') } as unknown as File
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId={null}
          replayTerminal
          replayMatchesCurrentScript
          events={events}
          recordingStatus={{ exists: false }}
          actionCount={2}
          diagnoseWithBaseline={vi.fn()}
          diagnoseWithUpload={diagnoseWithUpload}
          onAddExtractor={vi.fn()}
        />,
      )
    })
    const input = container.querySelector<HTMLInputElement>('input[type="file"]')!
    Object.defineProperty(input, 'files', { configurable: true, value: [file] })
    await act(async () => {
      input.dispatchEvent(new Event('change', { bubbles: true }))
      await Promise.resolve()
    })
    expect(container.textContent).toContain('Could not read that file as a HAR recording')
    expect(diagnoseWithUpload).not.toHaveBeenCalled()
  })

  it('does not offer unusable keyless baselines as diagnosable', async () => {
    await act(async () => {
      root.render(
        <CorrelationDiagnosePanel
          scriptId="s1"
          events={events}
          recordingStatus={{ exists: true, correlation_usable: false, correlation_unavailable_reason: 'Raw baseline encryption is not configured.' }}
          actionCount={2}
          diagnoseWithBaseline={vi.fn()}
          diagnoseWithUpload={vi.fn()}
          onAddExtractor={vi.fn()}
        />,
      )
    })
    expect(container.textContent).not.toContain('Diagnose correlations')
    expect(container.textContent).toContain('Raw baseline encryption is not configured.')
    expect(container.textContent).toContain('Diagnose from recording (HAR)')
  })
  it('shows incomplete relationship evidence when diagnosis has no suggestions and clears it for a new replay', async () => {
    const relationship = { complete: false, producer_count: 0, consumer_count: 0, missing_producers: 0, unavailable_extractors: 0, missing_consumers: 0, state_counts: {}, details_complete: false, details_omitted: 3, detail_limit: 256, relationships: [] }
    const diagnose = vi.fn().mockResolvedValue({ suggestions: [], blocking: false, counts: { errors: 0, warnings: 0 }, compared_recorded_steps: 2, total_recorded_steps: 2, relationship_coverage: relationship })
    const props = { scriptId: 's1', replaySessionId: 'replay-1', replayTerminal: true, replayMatchesCurrentScript: true, events, recordingStatus: { exists: true, request_count: 2 }, actionCount: 2, diagnoseWithBaseline: diagnose, diagnoseWithUpload: vi.fn(), onAddExtractor: vi.fn() }
    await act(async () => { root.render(<CorrelationDiagnosePanel {...props} />) })
    await act(async () => { [...container.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === 'Diagnose correlations')!.click() })
    expect(container.textContent).toContain('Correlation relationship evidence is incomplete')
    expect(container.textContent).toContain('No producer bindings detected')
    expect(container.textContent).toContain('3 relationship rows not shown')
    expect(container.textContent).not.toContain('No correlation issues detected')
    await act(async () => { root.render(<CorrelationDiagnosePanel {...props} replaySessionId="replay-2" />) })
    expect(container.querySelector('[aria-label="Correlation relationship evidence"]')).toBeNull()
    expect(container.textContent).not.toContain('3 relationship rows not shown')
  })

  it.each([
    { relationships: [{ producer_step_id: 0, consumer_step_id: 1, state: 'unsupported-private-state', producer_observed: true, extractor_available: true, consumer_observed: true }] },
    { relationships: [null] },
    { relationships: [{ producer_step_id: 0, consumer_step_id: 1, state: { toString: 1, valueOf: 1 }, producer_observed: true, extractor_available: true, consumer_observed: true }] },
    { state_counts: { producer_missing: 'private-count' } },
    { state_counts: { unknown_future_state: 1 } },
  ])('keeps malformed relationship evidence inconclusive when diagnosis has no suggestions', async malformed => {
    const relationship = { complete: true, producer_count: 1, consumer_count: 1, missing_producers: 0, unavailable_extractors: 0, missing_consumers: 0, state_counts: {}, details_complete: true, details_omitted: 0, detail_limit: 256, relationships: [], ...malformed }
    const diagnose = vi.fn().mockResolvedValue({ suggestions: [], blocking: false, compared_recorded_steps: 2, total_recorded_steps: 2, relationship_coverage: relationship })
    await act(async () => { root.render(<CorrelationDiagnosePanel scriptId="s1" replaySessionId="replay" replayTerminal replayMatchesCurrentScript events={events} recordingStatus={{ exists: true, request_count: 2 }} actionCount={2} diagnoseWithBaseline={diagnose} diagnoseWithUpload={vi.fn()} onAddExtractor={vi.fn()} />) })
    await act(async () => { [...container.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === 'Diagnose correlations')!.click() })
    expect(container.textContent).toContain('Relationship inspection incomplete')
    expect(container.textContent).toContain('Correlation relationship evidence is incomplete')
    expect(container.textContent).not.toContain('No correlation issues detected')
    expect(container.innerHTML).not.toContain('unsupported-private-state')
    expect(container.innerHTML).not.toContain('private-count')
    expect(container.innerHTML).not.toContain('unknown_future_state')
  })

})
