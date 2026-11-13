import type { StoredScenario } from '../types'
import { resolveRuntimeRowsValue } from './runtimeSettingsModel'

// Where browser evidence is turned on, for the empty states it would have
// filled. Both settings are off unless a browser script or a scenario group
// chooses them (config.browser.capture_document_metrics and
// capture_interactions).

export const DOCUMENT_CAPTURE_SETTING_HINT = "Page timings and Web Vitals are recorded only when they are turned on in the browser script's or scenario group's Runtime Settings › Browser."

export const INTERACTION_CAPTURE_SETTING_HINT = "Interaction evidence is recorded only when it is turned on in the browser script's or scenario group's Runtime Settings › Browser."

type ScenarioCapture = Pick<StoredScenario, 'scripts' | 'runtime_settings'>
type ScenarioGroup = ScenarioCapture['scripts'][number]

/** Where a group's browser settings come from: the first of these that has a browser section. */
export type BrowserSettingsSource = 'group' | 'scenario' | 'script'

export interface PageTimingCoverage {
  /** The scenario's browser groups. */
  browserGroups: ScenarioGroup[]
  /** The browser groups that will record no page timings or Web Vitals, and where that is decided. */
  without: { group: ScenarioGroup; source: BrowserSettingsSource }[]
}

/**
 * Which browser groups will record page timings and Web Vitals: the group's
 * row, else the scenario's, else the script's own setting decides, as the
 * controller merges them (a v1 browser section replaces the whole one below; a
 * partial row decides only the paths it lists). A group whose script was not reviewed is left out.
 */
export function pageTimingCoverage(
  scenario: ScenarioCapture,
  scripts: readonly { id: string; protocol?: string; documentCapture?: boolean }[],
): PageTimingCoverage {
  const byId = new Map(scripts.map(script => [script.id, script]))
  const coverage: PageTimingCoverage = { browserGroups: [], without: [] }
  for (const group of scenario.scripts) {
    const script = byId.get(group.script_id)
    if (script?.protocol !== 'browser') continue
    coverage.browserGroups.push(group)
    // Resolved per path: a partial row (override_fields) decides only when it lists
    // browser.capture_document_metrics (or browser); a v1 browser_config replaces the section.
    const { index, value } = resolveRuntimeRowsValue('browser.capture_document_metrics', [group.runtime_settings, scenario.runtime_settings])
    if (index >= 0 ? value === true : script.documentCapture === true) continue
    coverage.without.push({ group, source: index === 0 ? 'group' : index === 1 ? 'scenario' : 'script' })
  }
  return coverage
}
