import { describe, expect, it } from 'vitest'

import { SCENARIO_PARITY_FIXTURES } from '../test/scenarioAssemblyParityFixtures'
import golden from '../test/scenarioAssemblyParity.golden.json'
import { scenarioDraftFromStoredScenario, scenarioEstimateRequest, scenarioLoadPreview, scenarioTimelineGroups, scenarioUpsertPayload } from './scenarioAssembly'

// Scenario assembly parity (AUTH-3). Every stored scenario in the corpus is
// loaded into a draft and projected into its save request, capacity estimate,
// composition timeline and load preview. The golden file was first recorded
// from TestHub's flat-form reconstruction (runtime mirrors rebuilt into the
// override at save) before it was retired. Retirement changed two things in
// the save and estimate, both corrections pinned below: a think-time section
// saved without `ignore` is no longer given `ignore: false`, and an unchanged
// save no longer re-normalizes a stored section (random_percentage kept its
// `mean_ms`). The load preview was re-recorded with the shared run projection
// (A8), which draws group start offsets and takes the next stage's level at a
// stage boundary; nothing else changed, browser fixtures included.
// Re-record only for an intended change: APEXLOAD_UPDATE_SCENARIO_PARITY=1.
function projections() {
  const out: Record<string, unknown> = {}
  for (const scenario of SCENARIO_PARITY_FIXTURES) {
    const draft = scenarioDraftFromStoredScenario(scenario, { fallbackScriptId: 'fallback-script' })
    out[scenario.id] = {
      upsert: scenarioUpsertPayload(draft, { projectId: 'project-1', workloadShapeId: 'shape-1', fallbackName: 'Draft scenario' }),
      estimate: scenarioEstimateRequest(draft, { projectId: 'project-1' }),
      timeline: scenarioTimelineGroups(draft.groups),
      preview: scenarioLoadPreview(draft),
    }
  }
  return JSON.parse(JSON.stringify(out))
}

describe('scenario assembly parity corpus', () => {
  it('reproduces the recorded save, estimate, timeline and preview projections', async () => {
    const actual = projections()
    const env = (globalThis as { process?: { env: Record<string, string | undefined> } }).process?.env
    if (env?.APEXLOAD_UPDATE_SCENARIO_PARITY === '1') {
      // @ts-expect-error node:fs is available in the Vitest runtime.
      const { writeFileSync } = await import('node:fs')
      writeFileSync(new URL('../test/scenarioAssemblyParity.golden.json', import.meta.url), JSON.stringify(actual, null, 2) + '\n')
      return
    }
    expect(Object.keys(actual).length).toBe(SCENARIO_PARITY_FIXTURES.length)
    expect(actual).toEqual(golden)
  })

  it('saves an unchanged scenario runtime exactly as stored', () => {
    for (const scenario of SCENARIO_PARITY_FIXTURES.filter(fixture => fixture.id !== 'conflicting-aliases')) {
      const payload = scenarioUpsertPayload(scenarioDraftFromStoredScenario(scenario), { fallbackName: 'Draft scenario' })
      expect(JSON.parse(JSON.stringify(payload.runtime_settings ?? null)), scenario.id).toEqual(JSON.parse(JSON.stringify(scenario.runtime_settings ?? null)))
    }
  })

  it('drops a legacy runtime alias whose canonical section is authored', () => {
    const scenario = SCENARIO_PARITY_FIXTURES.find(fixture => fixture.id === 'conflicting-aliases')!
    const runtime = JSON.parse(JSON.stringify(scenarioUpsertPayload(scenarioDraftFromStoredScenario(scenario)).runtime_settings))
    expect(runtime).toEqual({
      run_logic: { iterations: 4, auto_transactions: 'per_action' },
      log: { level: 'extended' },
      browser_config: { device_profile: 'pixel_8', headless: false },
    })
  })
})
