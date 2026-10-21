import { request } from '../hooks/apiClient'
import type { BrowserPanelInputs, DashboardDefinition } from '../hooks/api/useDashboardDefinitionsApi'

type Definition = Pick<DashboardDefinition, 'name' | 'tabs'>

// Freeze from the actual evaluated publications, never a client-calculated
// substitute. Ordinary reusable panels keep their existing query semantics.
export async function freezeBrowserPanelDefinition(runId: string | undefined, definition: Definition, signal?: AbortSignal): Promise<Definition> {
  signal?.throwIfAborted()
  const frozen = structuredClone(definition)
  const panels = frozen.tabs.flatMap(tab => tab.panels).filter(panel => panel.query?.query_version === 4)
  if (!panels.length) return frozen
  if (!runId || panels.length > 20) throw new Error('Select an archived run and at most 20 frozen browser panels.')
  for (const panel of panels) {
    signal?.throwIfAborted()
    const query = panel.query!
    const result: { browser_inputs?: BrowserPanelInputs } = await request<{ browser_inputs?: BrowserPanelInputs }>(`/tests/${encodeURIComponent(runId)}/archive/native-series`, { method: 'POST', body: JSON.stringify({ ...query, browser_inputs: undefined }), timeoutMs: 35000, signal })
    signal?.throwIfAborted()
    const pins: BrowserPanelInputs | undefined = result.browser_inputs
    if (!pins || pins.version !== 1 || pins.current?.run_id !== runId || (pins.baseline?.run_id || '') !== (query.comparison_run_id || '')) throw new Error('The server did not return matching immutable browser inputs.')
    query.browser_inputs = pins
  }
  return frozen
}
