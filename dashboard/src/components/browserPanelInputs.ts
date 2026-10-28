import { request } from '../hooks/apiClient'
import type { BrowserPanelInputs, BrowserPanelPolicyPin, DashboardAnalyticsQuery, DashboardDefinition } from '../hooks/api/useDashboardDefinitionsApi'

type Definition = Pick<DashboardDefinition, 'name' | 'tabs'>

const hex64 = /^[a-f0-9]{64}$/

function validPolicyPinList(list: BrowserPanelPolicyPin[] | undefined): boolean {
  if (!list || list.length === 0) return true
  if (list.length > 2) return false
  for (let i = 0; i < list.length; i++) {
    const pin = list[i]
    if ((pin.kind !== 'browser_policy' && pin.kind !== 'journey_policy') || !pin.id || pin.id.trim() !== pin.id || pin.id.length > 128 || !Number.isInteger(pin.revision) || pin.revision < 1 || !hex64.test(pin.sha256)) return false
    if (i > 0 && list[i - 1].kind >= pin.kind) return false
  }
  return true
}

function samePolicyPins(a: BrowserPanelPolicyPin[] | undefined, b: BrowserPanelPolicyPin[] | undefined): boolean {
  const left = a ?? []
  const right = b ?? []
  if (left.length !== right.length) return false
  for (let i = 0; i < left.length; i++) {
    if (left[i].kind !== right[i].kind || left[i].id !== right[i].id || left[i].revision !== right[i].revision || left[i].sha256 !== right[i].sha256) return false
  }
  return true
}

function validTupleList(list: string[] | undefined, max: number): boolean {
  return Array.isArray(list) && list.length <= max && list.every((tuple, index) => hex64.test(tuple) && (index === 0 || list[index - 1] < tuple))
}

export function verifyBrowserPanelInputs(runId: string, query: DashboardAnalyticsQuery, pins: BrowserPanelInputs | undefined): pins is BrowserPanelInputs {
  if (!pins) return false
  const expectedVersion = query.query_version === 5 ? 2 : query.query_version === 4 ? 1 : 0
  if (expectedVersion === 0 || pins.version !== expectedVersion || !hex64.test(pins.query_sha256)) return false
  const max = query.max_series || 20
  if (pins.current?.run_id !== runId || !hex64.test(pins.current.manifest_sha256) || !validTupleList(pins.current.tuple_ids, max) || (pins.baseline?.run_id || '') !== (query.comparison_run_id || '') || (pins.baseline && (!hex64.test(pins.baseline.manifest_sha256) || !validTupleList(pins.baseline.tuple_ids, max)))) return false
  if (expectedVersion === 1) {
    if (pins.policy_pins?.length || pins.selection_receipt_sha256 || pins.current.policy_pins?.length || pins.baseline?.policy_pins?.length) return false
  } else {
    if (pins.selection_receipt_sha256 && !hex64.test(pins.selection_receipt_sha256)) return false
    if (!validPolicyPinList(pins.policy_pins) || !validPolicyPinList(pins.current.policy_pins) || !validPolicyPinList(pins.baseline?.policy_pins)) return false
    if (!samePolicyPins(pins.policy_pins, pins.current.policy_pins)) return false
  }
  return true
}

// Freeze from the actual evaluated publications, never a client-calculated
// substitute. Ordinary reusable panels keep their existing query semantics.
export async function freezeBrowserPanelDefinition(runId: string | undefined, definition: Definition, signal?: AbortSignal): Promise<Definition> {
  signal?.throwIfAborted()
  const frozen = structuredClone(definition)
  const panels = frozen.tabs.flatMap(tab => tab.panels).filter(panel => panel.query?.query_version === 4 || panel.query?.query_version === 5)
  if (!panels.length) return frozen
  if (!runId || panels.length > 20) throw new Error('Select an archived run and at most 20 frozen browser panels.')
  for (const panel of panels) {
    signal?.throwIfAborted()
    const query = panel.query!
    const result: { browser_inputs?: BrowserPanelInputs } = await request<{ browser_inputs?: BrowserPanelInputs }>(`/tests/${encodeURIComponent(runId)}/archive/native-series`, { method: 'POST', body: JSON.stringify({ ...query, browser_inputs: undefined }), timeoutMs: 35000, signal })
    signal?.throwIfAborted()
    const pins: BrowserPanelInputs | undefined = result.browser_inputs
    if (!verifyBrowserPanelInputs(runId, query, pins)) throw new Error('The server did not return matching immutable browser inputs.')
    query.browser_inputs = pins
  }
  return frozen
}
