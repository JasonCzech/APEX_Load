import { describe, expect, it } from 'vitest'

// Adapter retirement (AUTH-3). Each retired seam lists the names that must not
// come back in application code: once a seam's callers resolve through its
// canonical owner, reintroducing the adapter (or a caller of it) fails here.
// Tests are not scanned, so they can still name an old value to prove it is
// refused or translated.
const sources = import.meta.glob<string>(
  ['./**/*.{ts,tsx}', '!./**/*.test.{ts,tsx}', '!./types/generated/**', '!./test/**'],
  { query: '?raw', import: 'default', eager: true },
)

const RETIRED: { seam: string; owner: string; forbidden: (string | RegExp)[]; allowedIn?: string[] }[] = [
  {
    seam: 'aliasImportState / route selection',
    owner: 'scriptImportLaunchers.ts (launcher descriptors); appRouting.ts translates the old `mode=import` address',
    forbidden: [
      'aliasImportState',
      // Import sub-views that only aliased a launcher; importers are opened by
      // launcher descriptor and their sub-view is derived.
      ...['java-http-import', 'salesforce-import', 'har-import', 'recorder-import', 'devweb-import', 'openapi-import', 'raml-import', 'postman-import', 'wsdl-import', 'asyncapi-import']
        .map(name => new RegExp(`['"\`]${name}['"\`]`)),
    ],
  },
  {
    seam: 'TestHub flat ScenarioFormState mirrors / scenarioFormModel runtime reconstruction',
    owner: 'ScenarioFormState.runtime_settings, goal_config and assignment (the canonical values every editor writes); scenarioFormModel reads them',
    forbidden: [/\bruntime_(pacing|think_time)_\w+/, 'runtime_ignore_think_time', 'runtime_log_level', 'runtime_auto_transactions', 'runtime_iterations', 'browserRuntimeFromForm', 'pacingFromForm', 'thinkTimeFromForm',
      // The goal and placement mirrors (goal_config and assignment own them).
      /\bgoal_(target_average_tps|min_vusers|max_vusers|p95_latency_ms|max_error_pct)\b/, /\bassignment_(mode|targets)\b/],
    // The version 2 browser-checkpoint reader composes the old mirrors once.
    allowedIn: ['./components/scenarioAuthoringDraftV2.ts'],
  },
  {
    seam: 'duplicated workload calculations (newTestRunModel, scenarioWizardUtils, testExecutionScenarioModel, CreateTestModal)',
    owner: 'workloadStages.ts (stage peak, levels and boundaries); newTestRunModel.buildRunLoadProfilePreview (run and execution previews)',
    forbidden: [/function maxStageVUsers\b/, /function stagedValue\b/, /function stageTimelineSeconds\b/, /function stagedRunGroupValue[\s\S]{0,200}?for \(const stage of/, 'RESOURCE_BASELINE_SECONDS = 5 * 60\n\nexport interface ScenarioScriptDetail',
      // CreateTestModal's own ramp curve (it draws scenarioLoadPreview).
      'const baselineSeconds = 5 * 60'],
    allowedIn: ['./components/workloadStages.ts'],
  },
  {
    seam: 'CompatibleDebugStartRequest',
    owner: 'types/replay.ts ScriptDebugStartRequest (the generated request body; openapiContract.test.ts pins it)',
    forbidden: ['CompatibleDebugStartRequest'],
  },
]

describe('adapter retirement', () => {
  it('scans the application sources', () => {
    expect(Object.keys(sources).length).toBeGreaterThan(100)
  })

  for (const { seam, owner, forbidden, allowedIn = [] } of RETIRED) {
    it(`keeps ${seam} retired (owner: ${owner})`, () => {
      const offenders: string[] = []
      for (const [path, source] of Object.entries(sources)) {
        if (allowedIn.includes(path)) continue
        for (const pattern of forbidden) {
          const found = typeof pattern === 'string' ? source.includes(pattern) : pattern.test(source)
          if (found) offenders.push(`${path}: ${pattern}`)
        }
      }
      expect(offenders).toEqual([])
    })
  }
})
