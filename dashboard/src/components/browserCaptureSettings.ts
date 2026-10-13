import type { StoredScenario } from '../types'

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
 * browser section, else the scenario's, else the script's own setting
 * decides, as the controller merges them (each section replaces the whole
 * one below). A group whose script was not reviewed is left out.
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
    const groupSection = group.runtime_settings?.browser_config
    const section = groupSection ?? scenario.runtime_settings?.browser_config
    if (section ? section.capture_document_metrics === true : script.documentCapture === true) continue
    coverage.without.push({ group, source: groupSection ? 'group' : section ? 'scenario' : 'script' })
  }
  return coverage
}
