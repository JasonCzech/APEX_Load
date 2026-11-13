import { describe, expect, it } from 'vitest'

import type { StoredScenario } from '../types'
import { pageTimingCoverage } from './browserCaptureSettings'

type Scenario = Pick<StoredScenario, 'scripts' | 'runtime_settings'>
const group = (name: string, scriptId: string, captured?: boolean): Scenario['scripts'][number] => ({
  order: 0, name, script_id: scriptId, vusers: 1,
  runtime_settings: captured === undefined ? undefined : { browser_config: { capture_document_metrics: captured } },
})
const without = (scenario: Scenario) => pageTimingCoverage(scenario, scripts).without.map(item => [item.group.name, item.source])
const scripts = [
  { id: 'shop', protocol: 'browser', documentCapture: true },
  { id: 'search', protocol: 'browser', documentCapture: false },
  { id: 'api', protocol: 'http' },
]

describe('pageTimingCoverage', () => {
  it('takes the group\'s browser section, else the scenario\'s, else the script\'s setting, and says which', () => {
    const scenario: Scenario = { scripts: [group('Shop', 'shop'), group('Search', 'search'), group('Search captured', 'search', true), group('Shop off', 'shop', false), group('API', 'api')] }
    expect(pageTimingCoverage(scenario, scripts).browserGroups.map(item => item.name)).toEqual(['Shop', 'Search', 'Search captured', 'Shop off'])
    expect(without(scenario)).toEqual([['Search', 'script'], ['Shop off', 'group']])
    // A scenario browser section replaces the script's, so Shop loses capture.
    const withDefault: Scenario = { ...scenario, runtime_settings: { browser_config: { headless: true } } }
    expect(without(withDefault)).toEqual([['Shop', 'scenario'], ['Search', 'scenario'], ['Shop off', 'group']])
  })

  it('lets a partial group row decide only the browser paths it lists', () => {
    const partialGroup = (name: string, scriptId: string): Scenario['scripts'][number] => ({
      order: 0, name, script_id: scriptId, vusers: 1,
      runtime_settings: { browser_config: { headless: true }, override_fields: ['browser.headless'] } as Scenario['scripts'][number]['runtime_settings'],
    })
    // The group lists only headless, so the script's capture setting still applies.
    expect(without({ scripts: [partialGroup('Shop', 'shop'), partialGroup('Search', 'search')] })).toEqual([['Search', 'script']])
    // A scenario that sets capture applies under a partial group that does not list it.
    const scenario: Scenario = {
      scripts: [partialGroup('Search', 'search')],
      runtime_settings: { browser_config: { capture_document_metrics: true }, override_fields: ['browser.capture_document_metrics'] } as Scenario['runtime_settings'],
    }
    expect(without(scenario)).toEqual([])
  })

  it('leaves out groups whose script was not reviewed', () => {
    expect(pageTimingCoverage({ scripts: [group('Unknown', 'missing')] }, scripts)).toEqual({ browserGroups: [], without: [] })
  })
})
