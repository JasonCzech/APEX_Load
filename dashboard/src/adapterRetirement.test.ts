import { describe, expect, it } from 'vitest'

import { LEGACY_FLAT_FORM_FIELDS } from './components/scenarioAuthoringDraftV2'

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
    // Structural: a ramp curve (a division by a ramp duration, whether a
    // variable, a member or a parsed field) outside the projections that own
    // one. Comments are not division.
    seam: 'simple ramp curves',
    owner: 'newTestRunModel.buildRunLoadProfilePreview and workloadStages.ts; the wizard model chart and the Command Center live model keep theirs (docs/qa/ADAPTER_RETIREMENT.md)',
    forbidden: [/(?<![/*])\/(?![/*])\s*\(?\s*(?:[\w.]*\(\s*)?[\w.]*ramp_?(up|down)/i],
    allowedIn: ['./components/newTestRunModel.ts', './components/workloadStages.ts', './components/scenarioWizardUtils.ts', './components/commandCenterLoadModel.ts'],
  },
  {
    seam: 'CompatibleDebugStartRequest',
    owner: 'types/replay.ts ScriptDebugStartRequest (the generated request body; openapiContract.test.ts pins it)',
    forbidden: ['CompatibleDebugStartRequest'],
  },
]

// The callers each retired copy served, and the owner each must import from.
const OWNED_CALLERS: { path: string; owner: RegExp }[] = [
  { path: './components/CreateTestModal.tsx', owner: /import \{[^}]*\bscenarioLoadPreview\b[^}]*\} from '\.\/scenarioAssembly'/ },
  { path: './components/scenarioAssembly.ts', owner: /import \{[^}]*\bbuildRunLoadProfilePreview\b[^}]*\} from '\.\/newTestRunModel'/ },
  { path: './components/testExecutionScenarioModel.ts', owner: /import \{[^}]*\bbuildRunLoadProfilePreview\b[^}]*\} from '\.\/newTestRunModel'/ },
  { path: './components/newTestRunModel.ts', owner: /from '\.\/workloadStages'/ },
  { path: './components/scenarioWizardUtils.ts', owner: /from '\.\/workloadStages'/ },
]

describe('adapter retirement', () => {
  it('scans the application sources', () => {
    expect(Object.keys(sources).length).toBeGreaterThan(100)
  })

  // Structural: some mirror names (the error flags, the browser fields) are
  // legitimate elsewhere, so the form's own members are checked by name.
  it('keeps every version 2 mirror field off the scenario form', () => {
    const source = sources['./components/TestHub.tsx'] ?? ''
    const start = source.indexOf('export interface ScenarioFormState {')
    expect(start).toBeGreaterThan(-1)
    const body = source.slice(start, source.indexOf('\n}\n', start))
    const members = LEGACY_FLAT_FORM_FIELDS.filter(name => new RegExp(`^\\s*${name}\\??\\s*:`, 'm').test(body))
    expect(members).toEqual([])
    expect(LEGACY_FLAT_FORM_FIELDS.length).toBe(34)
  })

  // The script editor's session state (the paired baseline, the dirty
  // calculation, the initial-save flag and guarded navigation) has one owner,
  // useScriptAuthoringSession, and its Save button follows the shared save
  // rule; the editor keeps no copy of either.
  it('keeps the script editor\'s session state in useScriptAuthoringSession', () => {
    const source = sources['./components/ScriptEditor.tsx'] ?? ''
    expect(source).toMatch(/import \{[^}]*\buseScriptAuthoringSession\b[^}]*\} from '\.\/useScriptAuthoringSession'/)
    expect(source).toMatch(/\} = scriptSaveAvailability\(/)
    const copies: (string | RegExp)[] = [
      // The baseline, the initial-save flag and the guard.
      'useNavigationGuard', 'useNavigationGuardRunner', 'admitScriptAuthoringBaseline', 'setAuthoringBaseline', 'setAuthoringRevisionState', 'setReviewedAuthoringBaseline', 'setInitialSaveRequired', 'getScriptAuthoring(',
      // The dirty calculation and the save rule.
      /!==\s*baselineJsonRef\.current/, /jsonDirty\s*\|\|\s*pendingParameterDataCellEditCount/, 'const saveScriptDisabled =', 'const saveScriptTitle =',
    ]
    expect(copies.filter(copy => typeof copy === 'string' ? source.includes(copy) : copy.test(source))).toEqual([])
  })

  it('resolves each retired copy\'s callers through its owner', () => {
    const missing = OWNED_CALLERS.filter(({ path, owner }) => !owner.test(sources[path] ?? '')).map(({ path, owner }) => `${path}: ${owner}`)
    expect(missing).toEqual([])
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
