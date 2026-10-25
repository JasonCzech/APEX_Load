// @vitest-environment jsdom
import { act, type ComponentProps } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import { authoringFixture, savedAuthoringFixture } from '../test/scriptAuthoringFixtures'

// The real script API over a mocked transport, so a repair is sent exactly as
// the editor sends it.
vi.mock('../hooks/apiClient', async importOriginal => ({ ...await importOriginal<typeof import('../hooks/apiClient')>(), requestWithResponse: vi.fn() }))
vi.mock('../hooks/useApi', async () => {
  const { useScriptApi } = await import('../hooks/api/useScriptApi')
  return { useApi: () => useScriptApi() }
})

import { ApiError, requestWithResponse } from '../hooks/apiClient'
import ScriptBlockerRepairPanel from './ScriptBlockerRepairPanel'
import { storedScriptImpactText } from './replayReadinessIssues'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const tlsStep: ScriptActionDefinition = {
  name: 'web_set_sockets_option_15', type: 'source_review', target: 'web_set_sockets_option',
  metadata: { execution_blocker: 'legacy_tls_replacement', source_function: 'web_set_sockets_option', source_file: 'vuser_init.c', source_line: 3 },
}
const script = { name: 'Imported', version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, init: [tlsStep], actions: [{ name: 'Home', type: 'GET', target: '/' }], metadata: { import: { source: 'enterprise_script_archive' } } } as ScriptDefinition
const baseline = authoringFixture(script, 'script-1', 'project-1')
const repaired = { ...script, config: { ...script.config, http: { tls: { min_version: '1.2', max_version: '1.3' } } }, init: [{ ...tlsStep, metadata: { source_function: 'web_set_sockets_option', migration_change: 'legacy TLS replaced' } }] } as ScriptDefinition
// What both routes report. A preview adds the two scripts it compares; an
// apply adds the persisted script and its validation instead.
const outcome = { script_id: 'script-1', content_revision: baseline.projection.content_revision, findings: [], readiness: { replay_ready: true, promotion_ready: false }, secret_binding_requirements: [], fidelity_report: {} }
const evaluation = { ...outcome, script: repaired, before: script }

describe('ScriptBlockerRepairPanel', () => {
  let container: HTMLDivElement
  let root: Root
  const props = (overrides: Partial<ComponentProps<typeof ScriptBlockerRepairPanel>> = {}): ComponentProps<typeof ScriptBlockerRepairPanel> => ({
    scriptId: 'script-1', projectId: 'project-1', baseline, script, action: tlsStep, actionPath: 'init[0]',
    dirty: false, saving: false, onSaveFirst: vi.fn(), onApplied: vi.fn(), onReconcile: vi.fn(), ...overrides,
  })
  const render = async (overrides: Partial<ComponentProps<typeof ScriptBlockerRepairPanel>> = {}) => {
    const given = props(overrides)
    await act(async () => { root.render(<ScriptBlockerRepairPanel {...given} />) })
    return given
  }
  const button = (text: string) => Array.from(container.querySelectorAll('button')).find(item => item.textContent === text)
  const panel = () => container.querySelector<HTMLElement>('[data-blocker-repair]')!
  const click = async (text: string) => { await act(async () => { button(text)!.click() }) }

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

  it('says why the step blocks replay and offers the TLS repair', async () => {
    await render()
    expect(panel().getAttribute('aria-labelledby')).toBeTruthy()
    expect(panel().querySelector('h4')?.textContent).toBe('This step blocks replay')
    const reason = panel().querySelector('.script-blocker-repair-reasons li')!
    expect(reason.querySelector('strong')?.textContent).toBe('Choose supported TLS settings')
    expect(reason.textContent).toContain('Source vuser_init.c:3')
    expect(reason.querySelector('p')?.textContent).toBe('The source uses legacy TLS behavior. Choose a supported replacement and preview the migration. Debug and test runs refuse this script until it is resolved.')
    const operation = panel().querySelector<HTMLSelectElement>('[aria-label="Repair operation"]')!
    expect(Array.from(operation.options).map(option => option.value)).toEqual(['select_tls'])
    const minimum = Array.from(panel().querySelectorAll<HTMLSelectElement>('select')).find(select => select.closest('label')?.textContent?.startsWith('Minimum TLS'))!
    expect(Array.from(minimum.options).map(option => option.textContent)).toEqual(['TLS 1.2 (up to 1.3)', 'TLS 1.3 only'])
    expect(panel().querySelector('.lr-subtitle')?.textContent).toContain('every legacy TLS step in this script')
  })

  it('previews against the saved copy with the baseline\'s If-Match tag and persists nothing', async () => {
    ;(requestWithResponse as Mock).mockResolvedValueOnce({ body: evaluation, headers: new Headers() })
    const given = await render()
    await click('Preview repair')
    expect(requestWithResponse).toHaveBeenCalledTimes(1)
    const [url, options] = (requestWithResponse as Mock).mock.calls[0]
    expect(url).toBe('/scripts/script-1/repairs/preview')
    expect(options.method).toBe('POST')
    expect(options.headers).toEqual({ 'If-Match': baseline.etag })
    expect(JSON.parse(options.body)).toEqual({ finding: 'legacy_tls_replacement', action_path: 'init[0]', operation: { type: 'select_tls', tls_min: '1.2', tls_max: '1.3' } })
    const outcome = panel().querySelector('[aria-label="Repair outcome"]')!
    expect(outcome.textContent).toContain('Passed')
    expect(outcome.textContent).toMatch(/Blocking issues0/)
    expect(panel().querySelector('.lr-repair-preview-fields')?.textContent).toContain('config.http')
    expect(panel().textContent).toContain('Preview only. Apply saves the repaired script as a new revision.')
    expect(given.onApplied).not.toHaveBeenCalled()
  })

  it('reads replay checks as blocked while the repaired copy still has a configuration reference the server readiness does not see', async () => {
    // The server's evaluation never sees protocol configuration, so it can call
    // the repaired copy replay-ready while the editor counts the reference.
    const withAuth = { ...repaired, config: { ...repaired.config, http: { ...repaired.config.http, auth: { username: '{{var.pUser}}' } } } } as ScriptDefinition
    ;(requestWithResponse as Mock).mockResolvedValueOnce({ body: { ...evaluation, script: withAuth }, headers: new Headers() })
    await render()
    await click('Preview repair')
    const [replay, blocking] = Array.from(panel().querySelector('[aria-label="Repair outcome"]')!.children)
    expect(replay.querySelector('strong')?.textContent).toBe('Blocked')
    expect(replay.querySelector('strong')?.className).toBe('danger')
    expect(replay.querySelector('small')?.textContent).toBe('Execution remains blocked')
    expect(blocking.querySelector('strong')?.textContent).toBe('1')
  })

  it('counts a gated value the repaired copy still misses in two control scopes once, as the start gate does', async () => {
    const consumer = (name: string, target: string) => ({ name, type: 'GET', method: 'GET', target, metadata: { execution_blockers: ['missing_producer:sid'] } })
    const scoped = { ...repaired, actions: [consumer('Confirm', '/confirm/{{param.sid}}'), { name: 'Retry', type: 'control_loop', target: '', control: { count: 2, steps: [consumer('Poll', '/poll/{{param.sid}}')] } }] } as ScriptDefinition
    const findings = ['actions[0]', 'actions[1].control.steps[0]'].map(action_path => ({ code: 'missing_producer:sid', action_path, detail: '', gate: 'execution' }))
    ;(requestWithResponse as Mock).mockResolvedValueOnce({ body: { ...evaluation, script: scoped, findings, readiness: { replay_ready: false, promotion_ready: false } }, headers: new Headers() })
    await render()
    await click('Preview repair')
    const [replay, blocking] = Array.from(panel().querySelector('[aria-label="Repair outcome"]')!.children)
    expect(replay.querySelector('strong')?.textContent).toBe('Blocked')
    expect(blocking.querySelector('strong')?.textContent).toBe('1')
  })

  it('lists the fields the repair changes, not the review records the server restamps', async () => {
    // The saved copy and the repaired one each carry the import's evaluation,
    // which the repair route rebuilds for the repaired script, and the
    // protocol report the import wrote, which no repair rewrites.
    const stamped = (value: ScriptDefinition, replayReady: boolean) => ({ ...value, metadata: { import: { source: 'enterprise_script_archive', preserved: {
      fidelity_report: { promotion_status: replayReady ? 'promotable' : 'blocked', blockers: replayReady ? [] : ['legacy_tls_replacement'], secret_gated: 0 },
      replay_preflight: { valid: replayReady, action_count: 2, expected_action_count: 2, issues: replayReady ? [] : ['init[0] "web_set_sockets_option_15": execution blocker legacy_tls_replacement blocks replay until the imported behavior is resolved'] },
      protocol_compat_report: { promotion_status: 'review_required', review_gated_steps: 1 },
    } } } }) as ScriptDefinition
    const fields = () => panel().querySelector('.lr-repair-preview-fields')!
    ;(requestWithResponse as Mock).mockResolvedValueOnce({ body: { ...evaluation, before: stamped(script, false), script: stamped(repaired, true) }, headers: new Headers() })
    await render()
    await click('Preview repair')
    expect(Array.from(fields().querySelectorAll('li')).map(item => item.textContent)).toEqual([
      'config.http',
      'init[0].metadata.execution_blocker',
      'init[0].metadata.source_file',
      'init[0].metadata.source_line',
      'init[0].metadata.migration_change',
    ])
    expect(fields().querySelector('.lr-repair-preview-section-heading span')?.textContent).toBe('5 shown')
    expect(fields().textContent).not.toContain('metadata.import')

    // A preview in which only those records change lists no field.
    ;(requestWithResponse as Mock).mockResolvedValueOnce({ body: { ...evaluation, before: stamped(script, false), script: stamped(script, true) }, headers: new Headers() })
    await act(async () => { root.unmount() })
    root = createRoot(container)
    await render()
    await click('Preview repair')
    expect(fields().querySelector('li')).toBeNull()
    expect(fields().querySelector('.lr-repair-preview-section-heading span')?.textContent).toBe('0 shown')
    expect(fields().textContent).toContain('No field paths changed in this preview.')
  })

  it('applies the previewed repair and hands the saved script to the editor', async () => {
    // The receipt has no before/after scripts, which would repeat a large
    // script past the response cap: the saved copy is all the editor adopts.
    const applied = { ...outcome, stored: savedAuthoringFixture(repaired, 'script-1', 'project-1', 'c'), validation: { script_id: 'script-1', valid: true, issues: [], warnings: [] } }
    ;(requestWithResponse as Mock)
      .mockResolvedValueOnce({ body: evaluation, headers: new Headers() })
      .mockResolvedValueOnce({ body: applied, headers: new Headers({ 'X-APEX-Script-Save-Mode': 'conditioned' }) })
    const given = await render()
    await click('Preview repair')
    await click('Apply reviewed repair')
    const [url, options] = (requestWithResponse as Mock).mock.calls[1]
    expect(url).toBe('/scripts/script-1/repairs/apply')
    expect(options.headers).toEqual({ 'If-Match': baseline.etag })
    expect(JSON.parse(options.body).operation).toEqual({ type: 'select_tls', tls_min: '1.2', tls_max: '1.3' })
    expect(given.onApplied).toHaveBeenCalledWith(applied)
    // The editor says it in its own live region: this panel unmounts once
    // the step's last blocker clears, and two regions would say it twice.
    expect(panel().querySelector('[role="status"]')?.textContent).toBe('')
    expect(button('Apply reviewed repair')).toBeUndefined()
  })

  it('previews and applies only for a clean editor, and offers Save while there are unsaved changes', async () => {
    ;(requestWithResponse as Mock).mockResolvedValueOnce({ body: evaluation, headers: new Headers() })
    const given = await render({ dirty: true })
    expect(panel().querySelector('[role="note"]')?.textContent).toContain('Repairs are saved directly to the script. Save or discard your changes first.')
    // The saved copy is read at the editor's step path, which unsaved edits
    // may have moved to another step, so Preview waits as Apply does.
    const preview = button('Preview repair')!
    expect(preview.disabled).toBe(true)
    expect(preview.title).toBe('Save or discard your changes first')
    await click('Preview repair')
    expect(requestWithResponse).not.toHaveBeenCalled()
    // A preview taken while clean is not applied over edits made since.
    await act(async () => { root.render(<ScriptBlockerRepairPanel {...given} dirty={false} />) })
    await click('Preview repair')
    await act(async () => { root.render(<ScriptBlockerRepairPanel {...given} />) })
    const apply = button('Apply reviewed repair')!
    expect(apply.disabled).toBe(true)
    expect(apply.title).toBe('Save or discard your changes first')
    await click('Save changes')
    expect(given.onSaveFirst).toHaveBeenCalledTimes(1)
    expect(requestWithResponse).toHaveBeenCalledTimes(1)
    await act(async () => { root.render(<ScriptBlockerRepairPanel {...given} saving />) })
    expect(button('Saving…')?.disabled).toBe(true)
  })

  it('never claims a repair was applied, or asks for a reload, once the editor has unsaved edits', async () => {
    let refuse!: (cause: unknown) => void
    ;(requestWithResponse as Mock).mockReturnValueOnce(new Promise((_, reject) => { refuse = reject }))
    const given = await render()
    await click('Preview repair')
    await act(async () => { root.render(<ScriptBlockerRepairPanel {...given} dirty />) })
    await act(async () => { refuse(new ApiError('Finding is no longer present', 400)) })
    expect(panel().querySelector('[role="status"]')?.textContent).toBe('')
    expect(panel().querySelector('[role="alert"]')?.textContent).toBe('The saved script no longer matches this editor. Save your changes first, then preview the repair again.')
  })

  it('starts reconciliation when the saved copy changed, and applies nothing', async () => {
    const conflict = new ApiError('script authored content changed; reload before repairing', 412)
    ;(requestWithResponse as Mock)
      .mockResolvedValueOnce({ body: evaluation, headers: new Headers() })
      .mockRejectedValueOnce(conflict)
    const given = await render()
    await click('Preview repair')
    await click('Apply reviewed repair')
    expect(given.onReconcile).toHaveBeenCalledWith(conflict)
    expect(given.onApplied).not.toHaveBeenCalled()
    expect(panel().querySelector('[role="alert"]')?.textContent).toContain('The saved script changed since this editor loaded it, so the repair was not applied.')
  })

  it('treats an unconfirmed apply as an unknown outcome to reconcile', async () => {
    ;(requestWithResponse as Mock)
      .mockResolvedValueOnce({ body: evaluation, headers: new Headers() })
      .mockResolvedValueOnce({ body: { ...outcome, stored: savedAuthoringFixture(repaired, 'script-1', 'project-1', 'c') }, headers: new Headers() })
    const given = await render()
    await click('Preview repair')
    await click('Apply reviewed repair')
    expect(given.onReconcile).toHaveBeenCalledTimes(1)
    expect(given.onApplied).not.toHaveBeenCalled()
    expect(panel().querySelector('[role="alert"]')?.textContent).toContain('We could not confirm whether this repair was saved.')
  })

  it('reconciles an apply answered with a server error, which can follow the save, and keeps the form', async () => {
    // The route answers 503 after the save when it cannot read the library
    // assignments; nothing in the masked body says whether it saved.
    const failure = new ApiError('internal server error', 503, { error: 'internal server error' })
    ;(requestWithResponse as Mock)
      .mockResolvedValueOnce({ body: evaluation, headers: new Headers() })
      .mockRejectedValueOnce(failure)
    const given = await render()
    await click('Preview repair')
    await click('Apply reviewed repair')
    expect(given.onReconcile).toHaveBeenCalledWith(failure)
    expect(given.onApplied).not.toHaveBeenCalled()
    expect(panel().querySelector('[role="alert"]')?.textContent).toContain('We could not confirm whether this repair was saved.')
    expect(panel().querySelector('[aria-label="Repair operation"]')).not.toBeNull()
    expect(panel().textContent).not.toContain('keeps no script revisions')
  })

  it('shows a server failure on Preview as one to retry, and keeps the form', async () => {
    ;(requestWithResponse as Mock)
      .mockRejectedValueOnce(new ApiError('internal server error', 503, { error: 'internal server error' }))
      .mockResolvedValueOnce({ body: evaluation, headers: new Headers() })
    const given = await render()
    await click('Preview repair')
    expect(panel().querySelector('[role="alert"]')?.textContent).toContain('Previewing the repair failed on the server. Choose Preview repair to try again.')
    expect(panel().querySelector('[aria-label="Repair operation"]')).not.toBeNull()
    expect(given.onReconcile).not.toHaveBeenCalled()
    await click('Preview repair')
    expect(panel().querySelector('[role="alert"]')).toBeNull()
    expect(button('Apply reviewed repair')).toBeTruthy()
  })

  it('says a preview too large to load is too large, not that the repair failed or may have been saved', async () => {
    // The real transport and the script API's own cap: a large script's
    // preview carries it twice and passes the cap although the request worked.
    const { requestWithResponse: transport } = await vi.importActual<typeof import('../hooks/apiClient')>('../hooks/apiClient')
    const fetchMock = vi.fn().mockResolvedValue(new Response(`{"script":"${'x'.repeat(12 << 20)}"}`, { headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    try {
      ;(requestWithResponse as Mock).mockImplementationOnce(transport)
      const given = await render()
      await click('Preview repair')
      expect(fetchMock).toHaveBeenCalledTimes(1)
      const alert = panel().querySelector('[role="alert"]')?.textContent || ''
      expect(alert).toBe('This repair preview is too large to load: it holds the whole script before and after the repair, which is more than the editor accepts in one response. Nothing was changed. Re-import the source to repair this blocker in the import review.')
      expect(alert).not.toMatch(/could not confirm|approved size limit|failed/i)
      expect(given.onReconcile).not.toHaveBeenCalled()
      expect(given.onApplied).not.toHaveBeenCalled()
      expect(button('Apply reviewed repair')).toBeUndefined()
      expect(panel().querySelector('[aria-label="Repair operation"]')).not.toBeNull()
    } finally {
      vi.unstubAllGlobals()
    }
  })

  it('hides the form only when the route says this controller keeps no script revisions', async () => {
    ;(requestWithResponse as Mock).mockRejectedValueOnce(new ApiError('durable script content revisions unavailable', 503, { error: 'durable script content revisions unavailable', code: 'script_revisions_unavailable' }))
    const given = await render()
    await click('Preview repair')
    expect(panel().querySelector('[aria-label="Repair operation"]')).toBeNull()
    expect(panel().textContent).toContain('This controller keeps no script revisions, so it cannot repair saved scripts.')
    expect(given.onReconcile).not.toHaveBeenCalled()
  })

  it('reads a finding the saved copy no longer has as a repair already applied', async () => {
    ;(requestWithResponse as Mock).mockRejectedValueOnce(new ApiError('Finding is no longer present', 400))
    const given = await render()
    await click('Preview repair')
    expect(panel().querySelector('[role="status"]')?.textContent).toBe('Repair already applied: the saved script no longer has this finding. Reload the script to see it.')
    expect(panel().querySelector('[role="alert"]')).toBeNull()
    expect(given.onReconcile).not.toHaveBeenCalled()
  })

  it('hides the form when the controller has no saved-script repairs', async () => {
    ;(requestWithResponse as Mock).mockRejectedValueOnce(new ApiError('404 page not found', 404))
    await render()
    await click('Preview repair')
    expect(panel().querySelector('[aria-label="Repair operation"]')).toBeNull()
    expect(panel().textContent).toContain('This controller cannot repair saved scripts. Re-import the source to resolve this blocker.')
  })

  it('asks for a reload when the route cannot find the script, and keeps the form', async () => {
    // The route's own 404, for a script deleted or moved to another project
    // since the editor loaded it, is not an older controller without the route.
    ;(requestWithResponse as Mock).mockRejectedValueOnce(new ApiError('script not found', 404, { error: 'script not found' }))
    await render()
    await click('Preview repair')
    expect(panel().querySelector('[role="alert"]')?.textContent).toBe('This script was deleted or moved to another project since the editor loaded it. Reload the script.')
    expect(panel().querySelector('[aria-label="Repair operation"]')).not.toBeNull()
    expect(panel().textContent).not.toContain('This controller cannot repair saved scripts.')
  })

  it('explains a blocker no guided repair applies to without a form', async () => {
    const step = { ...tlsStep, metadata: { execution_blockers: ['restored_body_required', 'html_navigation_requires_page_context'], source_file: 'Action.c', source_line: 12 } }
    await render({ action: step })
    expect(panel().querySelector('h4')?.textContent).toBe('2 problems on this step block replay')
    expect(Array.from(panel().querySelectorAll('.script-blocker-repair-reasons strong')).map(item => item.textContent)).toEqual(['Replace HTML-mode click with an explicit request', 'Restore protected request body'])
    expect(panel().querySelector('[aria-label="Repair operation"]')).toBeNull()
    expect(panel().textContent).toContain('Needs a source change or re-import.')
    // The import's wording asks for a body below; a saved script has no such
    // form. The reason is worded as Author Problems words it, and says to
    // import the source again once.
    const reason = 'The original request body could not be restored from the source. A protected request body can be restored only while importing: import the source again to restore it.'
    expect(reason).toBe(storedScriptImpactText('restored_body_required'))
    expect(panel().textContent).toContain(`${reason} Debug and test runs refuse this script until it is resolved.`)
    expect(panel().textContent?.split('A protected request body can be restored only while importing').length).toBe(2)
    expect(panel().textContent).not.toContain('Supply the body below')
  })

  it('sends an unsaved draft opened from the import review back there for a protected body, not to import the source again', async () => {
    const step = { ...tlsStep, metadata: { execution_blockers: ['restored_body_required'], source_file: 'Action.c', source_line: 12 } }
    await render({ scriptId: undefined, baseline: null, bodySource: 'import-review', action: step })
    const reason = 'The original request body could not be restored from the source. A protected request body can be restored only while importing: return to the import review to supply the protected body.'
    expect(reason).toBe(storedScriptImpactText('restored_body_required', '', 'import-review'))
    expect(panel().querySelector('.script-blocker-repair-reasons p')?.textContent).toBe(`${reason} Debug and test runs refuse this script until it is resolved.`)
    expect(panel().querySelector('.script-blocker-repair-note')?.textContent).toBe('Needs the body repair in the import review.')
    expect(panel().textContent).not.toContain('import the source again')
    expect(panel().textContent).not.toContain('re-import')
    // The same draft once saved has no import review behind it: it imports the source again.
    await render({ action: step })
    expect(panel().querySelector('.script-blocker-repair-reasons p')?.textContent).toBe(`${storedScriptImpactText('restored_body_required')} Debug and test runs refuse this script until it is resolved.`)
    expect(panel().querySelector('.script-blocker-repair-note')?.textContent).toBe('Needs a source change or re-import.')
  })

  it('sends an unsaved draft opened from the import review back there for a guided repair, since saving it returns there', async () => {
    await render({ scriptId: undefined, baseline: null, bodySource: 'import-review' })
    expect(panel().querySelector('[aria-label="Repair operation"]')).toBeNull()
    expect(panel().querySelector('.script-blocker-repair-note')?.textContent).toBe('Return to the import review to apply this repair to the draft before saving, or save the script and then repair it from the script library.')
    expect(panel().textContent).not.toContain('Save the script to apply this repair')
  })

  it('counts the review beside a body restoration as that restoration, as the start gate does', async () => {
    const step = { ...tlsStep, metadata: { execution_blockers: ['import_review_required', 'restored_body_required'], source_file: 'Action.c', source_line: 20 } }
    await render({ action: step })
    expect(panel().querySelector('h4')?.textContent).toBe('This step blocks replay')
    expect(Array.from(panel().querySelectorAll('.script-blocker-repair-reasons strong')).map(item => item.textContent)).toEqual(['Restore protected request body'])
  })

  it('says why a saved script without its baseline cannot be repaired, not that it is unsaved', async () => {
    // A retained draft held for review: the editor has no baseline to repair from.
    await render({ baseline: null, blockedReason: 'Load and review the current version of the script before repairing it.' })
    expect(panel().querySelector('[aria-label="Repair operation"]')).toBeNull()
    expect(panel().querySelector('.script-blocker-repair-note')?.textContent).toBe('Load and review the current version of the script before repairing it.')
    expect(panel().textContent).not.toContain('not saved yet')
    await render({ baseline: null, blockedReason: 'Loading the saved script. Its repairs are available once it has loaded.' })
    expect(panel().querySelector('.script-blocker-repair-note')?.textContent).toBe('Loading the saved script. Its repairs are available once it has loaded.')
    // With no reason given, a saved script still is not called unsaved.
    await render({ baseline: null })
    expect(panel().querySelector('[aria-label="Repair operation"]')).toBeNull()
    expect(panel().querySelector('.script-blocker-repair-note')?.textContent).toBe('Load and review the current version of the script before repairing it.')
  })

  it('asks for a save before a script that is not saved yet can be repaired', async () => {
    await render({ scriptId: undefined, baseline: null })
    expect(panel().querySelector('[aria-label="Repair operation"]')).toBeNull()
    expect(panel().textContent).toContain('Save the script to apply this repair.')
    await render({ blockedReason: 'Load and review the current version of the script before repairing it.' })
    expect(panel().querySelector('[aria-label="Repair operation"]')).toBeNull()
    expect(panel().textContent).toContain('Load and review the current version of the script before repairing it.')
  })
})
