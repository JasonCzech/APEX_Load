// @vitest-environment jsdom
import { act, type ComponentProps } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import { authoringFixture } from '../test/scriptAuthoringFixtures'

vi.mock('../hooks/apiClient', async importOriginal => ({ ...await importOriginal<typeof import('../hooks/apiClient')>(), requestWithResponse: vi.fn() }))
vi.mock('../hooks/useApi', async () => {
  const { useScriptApi } = await import('../hooks/api/useScriptApi')
  return { useApi: () => useScriptApi() }
})
vi.mock('./importReviewIssues', async importOriginal => {
  const actual = await importOriginal<typeof import('./importReviewIssues')>()
  return { ...actual, groupImportFindings: vi.fn(actual.groupImportFindings), changedRepairPaths: vi.fn(actual.changedRepairPaths) }
})

import { requestWithResponse } from '../hooks/apiClient'
import ScriptBlockerRepairPanel from './ScriptBlockerRepairPanel'
import { changedRepairPaths, groupImportFindings } from './importReviewIssues'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const tlsStep: ScriptActionDefinition = {
  name: 'web_set_sockets_option_15', type: 'source_review', target: 'web_set_sockets_option',
  metadata: { execution_blocker: 'legacy_tls_replacement', source_file: 'vuser_init.c', source_line: 3 },
}
const script = { name: 'Imported', version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, init: [tlsStep], actions: [{ name: 'Home', type: 'GET', target: '/' }] } as ScriptDefinition
const repaired = { ...script, config: { ...script.config, http: { tls: { min_version: '1.2', max_version: '1.3' } } }, init: [{ ...tlsStep, metadata: { migration_change: 'legacy TLS replaced' } }] } as ScriptDefinition
const baseline = authoringFixture(script, 'script-1', 'project-1')
const evaluation = { script_id: 'script-1', content_revision: baseline.projection.content_revision, findings: [], readiness: { replay_ready: true, promotion_ready: false }, secret_binding_requirements: [], fidelity_report: {}, script: repaired, before: script }

// The editor renders the open repair panel on every editor render. Grouping
// the repaired copy and diffing it against the saved one walk the whole
// script, so they run once per preview.
describe('ScriptBlockerRepairPanel preview analysis', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.mocked(groupImportFindings).mockClear()
    vi.mocked(changedRepairPaths).mockClear()
  })
  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    vi.clearAllMocks()
  })

  it('groups and diffs a preview once, not once per render', async () => {
    const props: ComponentProps<typeof ScriptBlockerRepairPanel> = {
      scriptId: 'script-1', projectId: 'project-1', baseline, script, action: tlsStep, actionPath: 'init[0]',
      dirty: false, saving: false, onSaveFirst: vi.fn(), onApplied: vi.fn(), onReconcile: vi.fn(),
    }
    ;(requestWithResponse as Mock).mockResolvedValueOnce({ body: evaluation, headers: new Headers() })
    await act(async () => { root.render(<ScriptBlockerRepairPanel {...props} />) })
    await act(async () => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Preview repair')!.click() })
    expect(container.querySelector('[aria-label="Repair outcome"]')?.textContent).toMatch(/Blocking issues0/)
    expect(groupImportFindings).toHaveBeenCalledTimes(1)
    expect(changedRepairPaths).toHaveBeenCalledTimes(1)

    // The editor renders again (an edit elsewhere, its own state) with the same preview open.
    for (const saving of [false, true, false]) {
      await act(async () => { root.render(<ScriptBlockerRepairPanel {...props} saving={saving} onApplied={vi.fn()} />) })
    }
    expect(container.querySelector('.lr-repair-preview-fields')?.textContent).toContain('config.http')
    expect(groupImportFindings).toHaveBeenCalledTimes(1)
    expect(changedRepairPaths).toHaveBeenCalledTimes(1)
  })
})
