import { describe, expect, it } from 'vitest'

import {
  applyGroupErrorHandlingDraftEdit,
  applyGroupErrorHandlingSelect,
  buildEffectiveScenarioGroups,
  buildGroupErrorHandlingSectionForRun,
  buildPartialGroupRunRuntimeOverride,
  buildGroupsForRunPayload,
  buildGroupEditDraft,
  buildGroupRuntimeTableValues,
  buildRunDefaultCards,
  buildRunDefaultEditDraft,
  buildRunLoadProfilePreview,
  buildRuntimeSettingsForRun,
  CLEARABLE_RUNTIME_SECTIONS,
  cloneRuntimeSettingsForRun,
  errorHandlingSectionIsPinned,
  errorHandlingSurvivesBooleanClear,
  formatGroupPlacement,
  generatorCanRunGroup,
  generatorDisabledReason,
  getEffectiveRuntimeDefaults,
  groupHiddenPartialTiming,
  groupOverrideCount,
  groupOverrideKey,
  groupPlacementSummary,
  hasOwnKey,
  mergeRuntimeSettingsForRun,
  selectedGeneratorCapacity,
  setRuntimeBrowser,
  stagedGroupPeakVUsers,
  stampClearedRuntimeSections,
  type ClearableRuntimeSection,
  type GroupRunOverride,
} from './newTestRunModel'
import { type RuntimeSettingsOverride, type StoredScenario, type SystemInventoryGenerator, type TrafficProfile } from '../types'
import { effectiveRuntimeSection, resolveRuntimeOverrideFields } from './runtimeSettingsModel'
import { buildEmptyRunDefaultOverrides } from './newTestRunModel'

function scenarioFixture(): StoredScenario {
  return {
    id: 'scenario-1',
    name: 'Checkout',
    duration: '10m',
    scenario_mode: 'vuser_driven',
    script_shakeout: false,
    resource_baseline: false,
    runtime_settings: {
      iterations: 3,
      log_level: 'info',
      auto_transactions: 'actions',
      error_handling: {
        continue_on_error: false,
        snapshot_on_error: true,
      },
    },
    scripts: [
      {
        order: 0,
        name: 'Browse',
        script_id: 'script-1',
        script_name: 'Browse Script',
        protocol: 'http',
        vusers: 10,
        ramp_up: '1m',
        ramp_down: '30s',
        initialization: 'at_runtime',
      },
      {
        order: 1,
        name: 'Checkout',
        script_id: 'script-2',
        script_name: 'Checkout Script',
        protocol: 'browser',
        role: 'observer',
        vusers: 5,
        ramp_up: '30s',
        ramp_down: '30s',
        iterations: 7,
        runtime_settings: {
          log_level: 'debug',
        },
      },
    ],
    script_count: 2,
    created_at: '2026-05-01T00:00:00Z',
    updated_at: '2026-05-01T00:00:00Z',
  }
}

function generatorFixture(overrides: Partial<SystemInventoryGenerator> = {}): SystemInventoryGenerator {
  return {
    target: 'gen-a:9090',
    generator_id: 'gen-a',
    region: 'east',
    status: 'connected',
    supported_protocols: ['http', 'browser'],
    compatibility_state: 'compatible',
    drain_state: 'active',
    capacity_state: 'available',
    health_score: 100,
    available_capacity: 100,
    active_vusers: 0,
    cpu_percent: 0,
    memory_bytes: 0,
    goroutine_count: 1,
    cpu_limit_cores: 4,
    memory_limit_bytes: 8_589_934_592,
    max_vusers: 100,
    ...overrides,
  }
}

describe('newTestRunModel', () => {
  it('applies run defaults without mutating scenario defaults', () => {
    const scenario = scenarioFixture()
    const effective = getEffectiveRuntimeDefaults(scenario, {
      duration: '15m',
      runtime_settings: {
        iterations: 8,
        log_level: 'trace',
        continue_on_error: true,
      },
    })

    expect(effective.iterations).toBe(8)
    expect(effective.log_level).toBe('trace')
    expect(effective.auto_transactions).toBe('actions')
    expect(effective.continue_on_error).toBe(true)
    expect(scenario.runtime_settings?.iterations).toBe(3)
    expect(scenario.runtime_settings?.log_level).toBe('info')
  })

  it('builds runtime settings payload only when run defaults are overridden', () => {
    const scenario = scenarioFixture()

    expect(buildRuntimeSettingsForRun(scenario, { runtime_settings: {} })).toBeUndefined()
    expect(buildRuntimeSettingsForRun(scenario, {
      runtime_settings: {
        iterations: 9,
        snapshot_on_error: false,
      },
    })).toEqual({
      iterations: 9,
      log_level: 'info',
      auto_transactions: 'actions',
      error_handling: {
        continue_on_error: false,
        snapshot_on_error: false,
      },
    })
  })

  it('preserves every advanced root runtime sibling when changing one run setting', () => {
    const scenario = scenarioFixture()
    scenario.runtime_settings = {
      run_logic: { iterations: 3, auto_transactions: 'per_iteration', workload_model: 'closed', max_transactions: 500 },
      log: { level: 'standard', on_error_only: true, extended_options: ['advanced_trace'] },
      error_handling: { continue_on_error: true, snapshot_on_error: true, max_retries: 4, retry_delay_ms: 250, retry_backoff: 'exponential' },
      http: { version: '2', request_timeout_ms: 45_000, tls: { min_version: '1.3' } },
      web_runtime: { source_ips: ['10.0.0.1'], additional_attributes: { tenant: 'blue' } },
      browser_config: { engine: 'chromium', client_metrics: true, viewport: { width: 1440, height: 900 } },
      base_url: 'https://example.test',
      env: { API_TOKEN: '[REDACTED]' },
      redaction_rules: [{ pattern: 'customer-[0-9]+' }],
    }

    const payload = buildRuntimeSettingsForRun(scenario, {
      runtime_settings: { iterations: 9, snapshot_on_error: false },
    })

    expect(payload).toMatchObject({
      run_logic: { iterations: 9, auto_transactions: 'per_iteration', workload_model: 'closed', max_transactions: 500 },
      log: { level: 'standard', on_error_only: true, extended_options: ['advanced_trace'] },
      error_handling: { continue_on_error: true, snapshot_on_error: false, max_retries: 4, retry_delay_ms: 250, retry_backoff: 'exponential' },
      http: { version: '2', request_timeout_ms: 45_000, tls: { min_version: '1.3' } },
      web_runtime: { source_ips: ['10.0.0.1'], additional_attributes: { tenant: 'blue' } },
      browser_config: { engine: 'chromium', client_metrics: true, viewport: { width: 1440, height: 900 } },
      base_url: 'https://example.test',
      env: { API_TOKEN: '[REDACTED]' },
      redaction_rules: [{ pattern: 'customer-[0-9]+' }],
    })
    expect(payload?.iterations).toBeUndefined()
    expect(scenario.runtime_settings.run_logic?.iterations).toBe(3)
  })

  it('reviews group runtime with backend precedence and canonical section aliases', () => {
    const scenario = scenarioFixture()
    scenario.runtime_settings = {
      pacing: { type: 'fixed_delay', delay_ms: 100 },
      run_logic: { iterations: 6, auto_transactions: 'per_iteration' },
      log: { level: 'extended' },
      browser_config: { device_profile: 'pixel_8', headless: false },
    }
    scenario.scripts[0].runtime_settings = { pacing: { type: 'fixed_delay', delay_ms: 500 } }
    const runDefaults = { runtime_settings: { pacing: { type: 'fixed_delay', delay_ms: 200 } } }

    expect(buildGroupRuntimeTableValues(scenario.scripts[0], scenario, runDefaults).pacing).toBe('Fixed delay · 0.5 seconds')
    expect(buildGroupRuntimeTableValues(scenario.scripts[0], scenario, runDefaults, {
      runtime_settings: { pacing: { type: 'fixed_delay', delay_ms: 750 } },
    }).pacing).toBe('Fixed delay · 0.75 seconds')
    const cards = buildRunDefaultCards(scenario, { runtime_settings: {} })
    expect(cards.find(card => card.key === 'iterations')?.value).toBe('6')
    expect(cards.find(card => card.key === 'auto_transactions')?.value).toBe('per_iteration')
    expect(cards.find(card => card.key === 'log_level')?.value).toBe('extended')
  })

  it('reports inherited error handling as a script default instead of Stop / No snapshot', () => {
    const scenario = scenarioFixture()
    delete scenario.runtime_settings?.error_handling
    const group = scenario.scripts[0]
    const runDefaults = { runtime_settings: {} }

    // Nothing in the run/scenario/group chain pins error handling, so the run
    // inherits whatever the script's own config.error_handling says.
    expect(buildGroupRuntimeTableValues(group, scenario, runDefaults).errorHandling).toBe('Script default')
    expect(buildGroupEditDraft(group, scenario, runDefaults).continue_on_error).toBe('')
    expect(buildGroupEditDraft(group, scenario, runDefaults).snapshot_on_error).toBe('')

    // A half-set section still pins both halves: the backend replaces
    // script.Config.ErrorHandling wholesale, so the omitted key executes as false.
    expect(buildGroupRuntimeTableValues(group, scenario, runDefaults, {
      runtime_settings: { error_handling: { snapshot_on_error: true } },
    }).errorHandling).toBe('Stop / Snapshot')

    // A retry-only section names neither boolean but is still a section, so the
    // same wholesale replacement executes Stop / No snapshot. Reporting it as an
    // inherited script default would claim an inheritance the run never performs.
    expect(buildGroupRuntimeTableValues(group, scenario, runDefaults, {
      runtime_settings: { error_handling: { max_retries: 3 } },
    }).errorHandling).toBe('Stop / No snapshot')
    const retryOnlyGroup = { ...group, runtime_settings: { error_handling: { retry_delay_ms: 250 } } }
    expect(buildGroupRuntimeTableValues(retryOnlyGroup, scenario, runDefaults).errorHandling).toBe('Stop / No snapshot')
    const retryOnlyScenario = { ...scenario, runtime_settings: { ...scenario.runtime_settings, error_handling: { max_retries: 2 } } }
    expect(buildGroupRuntimeTableValues(group, retryOnlyScenario, runDefaults).errorHandling).toBe('Stop / No snapshot')

    // An EMPTY section still pins. Every dsl.ErrorHandlingConfig field is omitempty and the
    // override is a pointer, so a section whose only content is continue_on_error:false /
    // snapshot_on_error:false comes back from the API as a non-nil `{}` — and the backend applies
    // it whole all the same. It is also what the group override carries verbatim onto the wire:
    // the create payload below is the proof, so reporting 'Script default' here would promise an
    // inheritance the run never performs. (This assertion previously expected 'Script default'
    // on the strength of a comment about buildRuntimeSettingsForRun, which builds the RUN-level
    // section and never sees a group override.)
    const emptySectionOverride: GroupRunOverride = { runtime_settings: { error_handling: {} } }
    expect(buildGroupsForRunPayload(scenario, { [groupOverrideKey(group)]: emptySectionOverride })?.[0].runtime_settings)
      .toEqual({ error_handling: {} })
    expect(buildGroupRuntimeTableValues(group, scenario, runDefaults, emptySectionOverride).errorHandling).toBe('Stop / No snapshot')
    expect(buildGroupEditDraft(group, scenario, runDefaults, emptySectionOverride).continue_on_error).toBe('false')
    // Same shape read back off the API on the STORED group — the most common section there is.
    const allFalseGroup = { ...group, runtime_settings: { error_handling: {} } }
    expect(buildGroupRuntimeTableValues(allFalseGroup, scenario, runDefaults).errorHandling).toBe('Stop / No snapshot')
    expect(buildGroupEditDraft(allFalseGroup, scenario, runDefaults).continue_on_error).toBe('false')
    expect(buildGroupEditDraft(allFalseGroup, scenario, runDefaults).snapshot_on_error).toBe('false')
    // ...and on the scenario root, which the group inherits when it pins nothing itself.
    const allFalseScenario = { ...scenario, runtime_settings: { ...scenario.runtime_settings, error_handling: {} } }
    expect(buildGroupRuntimeTableValues(group, allFalseScenario, runDefaults).errorHandling).toBe('Stop / No snapshot')

    // A run-default that clears one boolean leaves the scenario's retries in place.
    expect(buildGroupRuntimeTableValues(group, retryOnlyScenario, {
      runtime_settings: { continue_on_error: null },
    }).errorHandling).toBe('Stop / No snapshot')
    // But clearing the only boolean the scenario set really does release the run-level section:
    // the synthesised run-default section is not a wire value, so an empty result means "unset".
    const continueOnlyScenario = { ...scenario, runtime_settings: { error_handling: { continue_on_error: true } } }
    expect(buildGroupRuntimeTableValues(group, continueOnlyScenario, {
      runtime_settings: { continue_on_error: null },
    }).errorHandling).toBe('Script default')
    expect(buildRuntimeSettingsForRun(continueOnlyScenario, {
      runtime_settings: { continue_on_error: null },
    })?.error_handling).toBeUndefined()

    // Explicitly pinned values still read as pinned.
    expect(buildGroupRuntimeTableValues(group, scenario, runDefaults, {
      runtime_settings: { error_handling: { continue_on_error: false, snapshot_on_error: false } },
    }).errorHandling).toBe('Stop / No snapshot')
    expect(buildGroupRuntimeTableValues(group, scenario, runDefaults, {
      runtime_settings: { error_handling: { continue_on_error: true, snapshot_on_error: true } },
    }).errorHandling).toBe('Continue / Snapshot')
  })

  it('resolves group error handling section-wise, never merging halves across levels', () => {
    // The backend swaps the root error_handling section for the group's outright
    // (mergeRuntimeSettings) and then assigns it to script.Config.ErrorHandling whole
    // (applyRuntimeOverrides), so a lower level never supplies the half the winning
    // section omits. The scenario here keeps BOTH root booleans set to true, which is
    // exactly what a per-field resolution would leak into the label.
    const scenario = scenarioFixture()
    scenario.runtime_settings = {
      error_handling: { continue_on_error: true, snapshot_on_error: true },
    }
    const group = scenario.scripts[0]
    const runDefaults = { runtime_settings: {} }

    // Sanity: with no group-level section the root section is what runs.
    expect(buildGroupRuntimeTableValues(group, scenario, runDefaults).errorHandling).toBe('Continue / Snapshot')

    // A retry-only run override replaces the whole root section, so both booleans
    // execute false even though the root sets both true.
    const retryOnlyOverride: GroupRunOverride = { runtime_settings: { error_handling: { max_retries: 3 } } }
    expect(buildGroupRuntimeTableValues(group, scenario, runDefaults, retryOnlyOverride).errorHandling).toBe('Stop / No snapshot')
    const retryOnlyDraft = buildGroupEditDraft(group, scenario, runDefaults, retryOnlyOverride)
    expect(retryOnlyDraft.continue_on_error).toBe('false')
    expect(retryOnlyDraft.snapshot_on_error).toBe('false')
    expect(buildGroupsForRunPayload(scenario, { [groupOverrideKey(group)]: retryOnlyOverride })?.[0].runtime_settings)
      .toEqual({ error_handling: { max_retries: 3 } })

    // Same rule for a stored group section that names only the other half.
    const snapshotOnlyGroup = { ...group, runtime_settings: { error_handling: { snapshot_on_error: true } } }
    expect(buildGroupRuntimeTableValues(snapshotOnlyGroup, scenario, runDefaults).errorHandling).toBe('Stop / Snapshot')
    expect(buildGroupEditDraft(snapshotOnlyGroup, scenario, runDefaults).continue_on_error).toBe('false')

    // A run-override section wins over the stored group section as a whole, too.
    expect(buildGroupRuntimeTableValues(snapshotOnlyGroup, scenario, runDefaults, {
      runtime_settings: { error_handling: { continue_on_error: true } },
    }).errorHandling).toBe('Continue / No snapshot')
  })

  it('clears a stored group error_handling section through a null tombstone', () => {
    // The group run override is overlaid back onto the group's STORED runtime_settings when the
    // create payload is built, and an overlay can only assign, so simply omitting the key can
    // never remove the stored section. The clear is written as an explicit null tombstone.
    const scenario = scenarioFixture()
    delete scenario.runtime_settings?.error_handling
    const group = scenario.scripts[0]
    group.runtime_settings = { error_handling: { continue_on_error: true, snapshot_on_error: true } }
    const runDefaults = { runtime_settings: {} }
    const key = groupOverrideKey(group)

    // Sanity: without the tombstone the stored section is what the run executes.
    expect(buildGroupsForRunPayload(scenario, { [key]: { vusers: 4 } })?.[0].runtime_settings)
      .toEqual({ error_handling: { continue_on_error: true, snapshot_on_error: true } })

    const cleared: GroupRunOverride = { runtime_settings: { error_handling: null } }
    // The tombstone is an instruction even though it carries no value, so the override survives
    // the hasGroupOverrideValues filter that decides whether groups are sent at all.
    expect(groupOverrideCount({ [key]: cleared })).toBe(1)
    // ...and it removes the section instead of re-applying it. No null reaches the API.
    expect(buildGroupsForRunPayload(scenario, { [key]: cleared })?.[0].runtime_settings).toBeUndefined()
    // The stored scenario group is untouched by the run-only clear.
    expect(group.runtime_settings).toEqual({ error_handling: { continue_on_error: true, snapshot_on_error: true } })

    // Re-opening the dialog has to show the clear, not the stale stored section.
    expect(buildGroupRuntimeTableValues(group, scenario, runDefaults, cleared).errorHandling).toBe('Script default')
    expect(buildGroupEditDraft(group, scenario, runDefaults, cleared).continue_on_error).toBe('')
    expect(buildGroupEditDraft(group, scenario, runDefaults, cleared).snapshot_on_error).toBe('')

    // A cleared group falls through to the run-level section when one exists, exactly as
    // mergeRuntimeSettings does — it does NOT reach the script's own config.
    const pinnedRoot = { ...scenario, runtime_settings: { error_handling: { continue_on_error: true } } }
    expect(buildGroupRuntimeTableValues(group, pinnedRoot, runDefaults, cleared).errorHandling).toBe('Continue / No snapshot')

    // Other sections in the same override are still overlaid normally alongside the tombstone.
    const mixed: GroupRunOverride = { runtime_settings: { error_handling: null, log_level: 'debug' } }
    expect(buildGroupsForRunPayload(scenario, { [key]: mixed })?.[0].runtime_settings).toEqual({ log_level: 'debug' })

    // The dialog's other two inherit options ('Script timing', 'Script think time') clear their
    // sections through the same tombstone, and clearing one must not disturb the others.
    group.runtime_settings = {
      pacing: { type: 'fixed_delay', delay_ms: 500 },
      think_time: { type: 'fixed', mean_ms: 900 },
      log_level: 'debug',
    }
    expect(buildGroupsForRunPayload(scenario, { [key]: { runtime_settings: { pacing: null } } })?.[0].runtime_settings)
      .toEqual({ think_time: { type: 'fixed', mean_ms: 900 }, log_level: 'debug' })
    expect(buildGroupsForRunPayload(scenario, { [key]: { runtime_settings: { think_time: null } } })?.[0].runtime_settings)
      .toEqual({ pacing: { type: 'fixed_delay', delay_ms: 500 }, log_level: 'debug' })

    // Read path has to agree with the write path: a cleared section reports the run-level value
    // the run will actually use, not the stored group value the tombstone just removed.
    const pacingRunDefaults = { runtime_settings: { pacing: { type: 'fixed_interval', interval_ms: 2000 } } }
    const clearedPacing: GroupRunOverride = { runtime_settings: { pacing: null } }
    expect(buildGroupRuntimeTableValues(group, scenario, pacingRunDefaults, clearedPacing).pacing)
      .toBe('Fixed interval · 2 seconds')
    expect(buildGroupEditDraft(group, scenario, pacingRunDefaults, clearedPacing).pacingType).toBe('fixed_interval')
    const clearedThinkTime: GroupRunOverride = { runtime_settings: { think_time: null } }
    expect(buildGroupRuntimeTableValues(group, scenario, { runtime_settings: {} }, clearedThinkTime).thinkTime)
      .toBe('Script think time')
  })

  it('clears and reads back every section in CLEARABLE_RUNTIME_SECTIONS through the shared helpers', () => {
    // The tri-state has exactly one list, one writer and one reader:
    //   * CLEARABLE_RUNTIME_SECTIONS is the list the group dialog's inherit options map to;
    //   * stampClearedRuntimeSections turns a removed section into the null tombstone;
    //   * effectiveGroupRuntimeSettings reads sections off the SAME overlay the payload builder
    //     submits, so a cleared section can never be read back off the stored group.
    // This test is driven BY the constant, so a section added to it that either helper does not
    // honour fails here instead of silently re-applying the stored section at run time.
    const sectionFixtures: Record<ClearableRuntimeSection, Record<string, unknown>> = {
      pacing: { type: 'fixed_delay', delay_ms: 500 },
      think_time: { type: 'gaussian', mean_ms: 900, std_ms: 100 },
      error_handling: { continue_on_error: true, snapshot_on_error: true },
      browser: { device_profile: 'iPhone 14 Pro', headless: true },
      browser_config: { device_profile: 'pixel_8', headless: false },
    }
    const clearedTableValue: Record<ClearableRuntimeSection, [string, string]> = {
      pacing: ['pacing', 'Script timing'],
      think_time: ['thinkTime', 'Script think time'],
      error_handling: ['errorHandling', 'Script default'],
      browser: ['browser', 'Standard'],
      browser_config: ['browser', 'Standard'],
    }
    // How the group dialog's save path empties each section. The browser cases go through the
    // REAL writer rather than a hand-built patch: setRuntimeBrowser is the only helper that knows
    // both browser wire shapes, and driving the guard past it is what stops the browser_config
    // case passing vacuously while the writer leaves an un-tombstoneable `{}` behind.
    const clearSection: Record<ClearableRuntimeSection, (patch: RuntimeSettingsOverride) => void> = {
      pacing: patch => { delete patch.pacing },
      think_time: patch => { delete patch.think_time },
      error_handling: patch => { delete patch.error_handling },
      browser: patch => setRuntimeBrowser(patch, {}),
      browser_config: patch => setRuntimeBrowser(patch, {}),
    }

    for (const section of CLEARABLE_RUNTIME_SECTIONS) {
      // Fails when a new clearable section is declared without being exercised below.
      expect(sectionFixtures[section]).toBeTruthy()
      expect(clearedTableValue[section]).toBeTruthy()
      expect(clearSection[section]).toBeTruthy()

      const scenario = scenarioFixture()
      delete scenario.runtime_settings?.error_handling
      const group = scenario.scripts[0]
      group.runtime_settings = { [section]: sectionFixtures[section], log_level: 'debug' } as RuntimeSettingsOverride
      const key = groupOverrideKey(group)
      const runDefaults = { runtime_settings: {} }
      const [tableField, clearedLabel] = clearedTableValue[section]

      // Sanity: with no clear the stored section is what the run executes and what the page
      // reports, so the assertions below are not vacuous.
      const storedTable = buildGroupRuntimeTableValues(group, scenario, runDefaults) as Record<string, string>
      expect(storedTable[tableField]).not.toBe(clearedLabel)
      expect(buildGroupsForRunPayload(scenario, { [key]: { vusers: 4 } })?.[0].runtime_settings)
        .toEqual({ [section]: sectionFixtures[section], log_level: 'debug' })

      // WRITE: the save path starts from the group's stored settings, applies the dialog's edit
      // and then stamps. A section the edit emptied has to leave the patch entirely, so the
      // stamp can turn that absence into an explicit tombstone.
      const edited = cloneRuntimeSettingsForRun(group.runtime_settings) as RuntimeSettingsOverride
      clearSection[section](edited)
      expect(hasOwnKey(edited, section)).toBe(false)
      const patch = stampClearedRuntimeSections(edited, group.runtime_settings)
      expect((patch as Record<string, unknown>)[section]).toBeNull()
      // ...which the payload overlay honours by removing the stored section, not re-applying it.
      const cleared: GroupRunOverride = { runtime_settings: patch }
      expect(groupOverrideCount({ [key]: cleared })).toBe(1)
      expect(buildGroupsForRunPayload(scenario, { [key]: cleared })?.[0].runtime_settings)
        .toEqual({ log_level: 'debug' })

      // READ: the table and the re-opened dialog must show the clear, never the stored section.
      const clearedTable = buildGroupRuntimeTableValues(group, scenario, runDefaults, cleared) as Record<string, string>
      expect(clearedTable[tableField]).toBe(clearedLabel)
      // The stored scenario group is untouched by the run-only clear.
      expect(group.runtime_settings).toEqual({ [section]: sectionFixtures[section], log_level: 'debug' })
    }
  })

  it('treats an all-false error handling section as pinned and an unclearable one as blocked', () => {
    // `{}` is what the API returns for a section whose only content is continue_on_error:false
    // and/or snapshot_on_error:false — presence, not content, decides whether a level transmits.
    expect(errorHandlingSectionIsPinned(undefined)).toBe(false)
    expect(errorHandlingSectionIsPinned(null)).toBe(false)
    expect(errorHandlingSectionIsPinned({})).toBe(true)
    expect(errorHandlingSectionIsPinned({ continue_on_error: false })).toBe(true)

    // The group dialog can only edit the two booleans, so a section with anything else in it
    // survives the clear and keeps executing both booleans as false.
    expect(errorHandlingSurvivesBooleanClear(undefined)).toBe(false)
    expect(errorHandlingSurvivesBooleanClear({})).toBe(false)
    expect(errorHandlingSurvivesBooleanClear({ continue_on_error: true, snapshot_on_error: true })).toBe(false)
    expect(errorHandlingSurvivesBooleanClear({ max_retries: 3 })).toBe(true)
    expect(errorHandlingSurvivesBooleanClear({ continue_on_error: true, retry_delay_ms: 1000 })).toBe(true)
    expect(errorHandlingSurvivesBooleanClear({ retry_backoff: 'fixed' })).toBe(true)
  })

  it('builds the group error handling section for every cell of the two-boolean truth table', () => {
    // error_handling is applied as a whole SECTION, so the four combinations of the dialog's two
    // halves have to be crossed with "the group carries retry fields the dialog cannot edit" and
    // "an ancestor pins a section". Every cell states the section the group transmits, and the
    // loop underneath re-derives what each half the operator set to inherit actually EXECUTES,
    // so a cell can never quietly stop matching the option's promise.
    const ancestor: RuntimeSettingsOverride['error_handling'] = { continue_on_error: true, snapshot_on_error: true }
    const groupSections = {
      none: undefined,
      booleans: { continue_on_error: true, snapshot_on_error: true },
      // The retry policy is the residual the dialog cannot reach; it keeps the section alive.
      withRetries: { continue_on_error: true, snapshot_on_error: true, max_retries: 3 },
    } as const
    const cases: Array<{
      draft: [string, string]
      group: keyof typeof groupSections
      ancestorPinned: boolean
      expected: RuntimeSettingsOverride['error_handling'] | undefined
    }> = [
      // NEITHER half on inherit: both booleans are pinned exactly as drafted and only the group's
      // own retry policy rides along.
      { draft: ['false', 'true'], group: 'none', ancestorPinned: false, expected: { continue_on_error: false, snapshot_on_error: true } },
      { draft: ['false', 'true'], group: 'none', ancestorPinned: true, expected: { continue_on_error: false, snapshot_on_error: true } },
      { draft: ['false', 'true'], group: 'booleans', ancestorPinned: false, expected: { continue_on_error: false, snapshot_on_error: true } },
      { draft: ['false', 'true'], group: 'booleans', ancestorPinned: true, expected: { continue_on_error: false, snapshot_on_error: true } },
      { draft: ['false', 'true'], group: 'withRetries', ancestorPinned: false, expected: { continue_on_error: false, snapshot_on_error: true, max_retries: 3 } },
      { draft: ['false', 'true'], group: 'withRetries', ancestorPinned: true, expected: { continue_on_error: false, snapshot_on_error: true, max_retries: 3 } },

      // ONLY continue on inherit. The section is still transmitted because the other half pins
      // it, so continue_on_error has to carry the ancestor's value — dropping it would execute
      // false, which is the discard the round-6 fix closed only for the both-halves case.
      { draft: ['', 'true'], group: 'none', ancestorPinned: true, expected: { continue_on_error: true, snapshot_on_error: true } },
      { draft: ['', 'true'], group: 'booleans', ancestorPinned: true, expected: { continue_on_error: true, snapshot_on_error: true } },
      { draft: ['', 'true'], group: 'withRetries', ancestorPinned: true, expected: { continue_on_error: true, snapshot_on_error: true, max_retries: 3 } },
      // ...and with nothing above the group there is no value to carry: this page never loads the
      // script's own config.error_handling, so the half is left out and executes false. See the
      // half-by-half check below.
      { draft: ['', 'true'], group: 'none', ancestorPinned: false, expected: { snapshot_on_error: true } },
      { draft: ['', 'true'], group: 'booleans', ancestorPinned: false, expected: { snapshot_on_error: true } },
      { draft: ['', 'true'], group: 'withRetries', ancestorPinned: false, expected: { snapshot_on_error: true, max_retries: 3 } },

      // ONLY snapshot on inherit — the mirror image.
      { draft: ['false', ''], group: 'none', ancestorPinned: true, expected: { continue_on_error: false, snapshot_on_error: true } },
      { draft: ['false', ''], group: 'booleans', ancestorPinned: true, expected: { continue_on_error: false, snapshot_on_error: true } },
      { draft: ['false', ''], group: 'withRetries', ancestorPinned: true, expected: { continue_on_error: false, snapshot_on_error: true, max_retries: 3 } },
      { draft: ['false', ''], group: 'none', ancestorPinned: false, expected: { continue_on_error: false } },
      { draft: ['false', ''], group: 'booleans', ancestorPinned: false, expected: { continue_on_error: false } },
      { draft: ['false', ''], group: 'withRetries', ancestorPinned: false, expected: { continue_on_error: false, max_retries: 3 } },

      // BOTH halves on inherit: the group transmits nothing, so whatever level is above it wins
      // intact. Only a retry policy the dialog cannot edit keeps the section alive — and the
      // dialog disables the inherit option there (errorHandlingSurvivesBooleanClear), so those
      // two cells are unreachable from the UI.
      { draft: ['', ''], group: 'none', ancestorPinned: false, expected: undefined },
      { draft: ['', ''], group: 'none', ancestorPinned: true, expected: undefined },
      { draft: ['', ''], group: 'booleans', ancestorPinned: false, expected: undefined },
      { draft: ['', ''], group: 'booleans', ancestorPinned: true, expected: undefined },
      { draft: ['', ''], group: 'withRetries', ancestorPinned: false, expected: { max_retries: 3 } },
      { draft: ['', ''], group: 'withRetries', ancestorPinned: true, expected: { continue_on_error: true, snapshot_on_error: true, max_retries: 3 } },
    ]
    expect(cases).toHaveLength(4 * 3 * 2)

    for (const testCase of cases) {
      const cell = `draft=${JSON.stringify(testCase.draft)} group=${testCase.group} ancestorPinned=${testCase.ancestorPinned}`
      const section = buildGroupErrorHandlingSectionForRun({
        groupSection: groupSections[testCase.group],
        inheritedSection: testCase.ancestorPinned ? ancestor : undefined,
        draft: { continue_on_error: testCase.draft[0], snapshot_on_error: testCase.draft[1] },
        changed: { continue_on_error: true, snapshot_on_error: true },
      })
      expect({ cell, section }).toEqual({ cell, section: testCase.expected })

      const halves = ['continue_on_error', 'snapshot_on_error'] as const
      halves.forEach((half, index) => {
        const drafted = testCase.draft[index]
        if (drafted !== '') {
          expect({ cell, half, runs: Boolean(section?.[half]) }).toEqual({ cell, half, runs: drafted === 'true' })
          return
        }
        if (!section) return // Released: the level above the group is applied whole, untouched.
        if (testCase.ancestorPinned) {
          // "Scenario default" with a section still on the wire: the ancestor's value has to be
          // materialised, because the winning section is applied whole.
          expect({ cell, half, runs: Boolean(section[half]) }).toEqual({ cell, half, runs: Boolean(ancestor?.[half]) })
          return
        }
        // "Script default" is not reachable while a section is transmitted: the script's own
        // config.error_handling never reaches this page, so the half is omitted and executes
        // false. NewTestPage.test.tsx "keeps inherited error handling selectable as Script
        // default and overrides only the edited half" pins that minimal-payload behaviour.
        expect({ cell, half, omitted: section[half] === undefined }).toEqual({ cell, half, omitted: true })
      })
    }
  })

  it('treats the error handling inherit option as a section-level clear in the dialog draft', () => {
    // Selecting the inherit option on ONE select cannot release the section on its own — the
    // other half keeps it on the wire and the released half then executes false. The draft edit
    // therefore moves BOTH halves to inherit, which is what makes buildGroupErrorHandlingSection
    // ForRun's release cell the one the UI actually reaches.
    const drafts = ['', 'true', 'false']
    for (const continueDraft of drafts) {
      for (const snapshotDraft of drafts) {
        const draft = { continue_on_error: continueDraft, snapshot_on_error: snapshotDraft }
        expect(applyGroupErrorHandlingDraftEdit(draft, 'continue_on_error', ''))
          .toEqual({ continue_on_error: '', snapshot_on_error: '' })
        expect(applyGroupErrorHandlingDraftEdit(draft, 'snapshot_on_error', ''))
          .toEqual({ continue_on_error: '', snapshot_on_error: '' })
        // An explicit value pins only the half it was set on; a section that omits the other
        // executes it as false, exactly as the dialog already displays it.
        for (const value of ['true', 'false']) {
          expect(applyGroupErrorHandlingDraftEdit(draft, 'continue_on_error', value))
            .toEqual({ continue_on_error: value, snapshot_on_error: snapshotDraft })
          expect(applyGroupErrorHandlingDraftEdit(draft, 'snapshot_on_error', value))
            .toEqual({ continue_on_error: continueDraft, snapshot_on_error: value })
        }
      }
    }
  })

  it('releases both browser wire shapes when the four browser fields are cleared', () => {
    // `browser` and `browser_config` are two wire shapes of ONE logical section and the dialog's
    // four controls write whichever one the level carries. Only the flat shape used to be
    // released on a clear; the whole-section shape was left behind as `{}`, which the API applies
    // as `script.Config.Browser = *cloneViaJSON(BrowserConfig)` and so wipes the script's own
    // engine/viewport/timeouts instead of leaving them alone.
    const flat: RuntimeSettingsOverride = { browser: { device_profile: 'iPhone 14 Pro', headless: true }, log_level: 'debug' }
    setRuntimeBrowser(flat, {})
    expect(flat).toEqual({ log_level: 'debug' })

    const wholeSection: RuntimeSettingsOverride = { browser_config: { device_profile: 'pixel_8', headless: false }, log_level: 'debug' }
    setRuntimeBrowser(wholeSection, {})
    expect(wholeSection).toEqual({ log_level: 'debug' })

    // A section that also carries fields the dialog cannot edit is NOT released — those fields
    // are the group's own pin and clearing a device profile must not discard them. (The cleared
    // keys are left holding `undefined`, which the JSON clone in mergeRuntimeSettingsForRun
    // drops before the payload is built.)
    const withEngine: RuntimeSettingsOverride = { browser_config: { engine: 'chromium', user_agent: 'APEX', device_profile: 'pixel_8' } }
    setRuntimeBrowser(withEngine, {})
    expect(withEngine).toEqual({ browser_config: { engine: 'chromium', user_agent: 'APEX' } })

    // ...and neither is a section the run level pins: releasing the group's would simply hand the
    // ancestor's section down, so the empty whole-section pin is the only way to say "Standard".
    const overridingAncestor: RuntimeSettingsOverride = { browser_config: { device_profile: 'pixel_8' } }
    setRuntimeBrowser(overridingAncestor, {}, { ancestorPinsBrowserConfig: true })
    expect(hasOwnKey(overridingAncestor, 'browser_config')).toBe(true)
    expect(overridingAncestor.browser_config).toEqual({})

    // Values still write through both shapes, and the legacy flat key is dropped once the level
    // carries the whole-section shape.
    const flatWrite: RuntimeSettingsOverride = {}
    setRuntimeBrowser(flatWrite, { device_profile: 'pixel_8' })
    expect(flatWrite).toEqual({ browser: { device_profile: 'pixel_8' } })
    const sectionWrite: RuntimeSettingsOverride = { browser: { headless: true }, browser_config: { engine: 'chromium' } }
    setRuntimeBrowser(sectionWrite, { headless: false })
    expect(sectionWrite).toEqual({ browser_config: { engine: 'chromium', headless: false } })
    expect(hasOwnKey(sectionWrite, 'browser')).toBe(false)
  })

  it('reports a retry-only run-default section as the booleans the run will execute', () => {
    // buildRunDefaultCards renders on the same page as the group table; a section that
    // names neither boolean still pins both to false, so 'Script default' would promise
    // an inheritance the run never performs.
    const scenario = scenarioFixture()
    scenario.runtime_settings = { error_handling: { max_retries: 2, retry_delay_ms: 250 } }
    const overrides = { runtime_settings: {} }

    const cards = buildRunDefaultCards(scenario, overrides)
    expect(cards.find(card => card.key === 'continue_on_error')).toMatchObject({
      value: 'Disabled',
      scenarioValue: 'Disabled',
      isDefault: false,
    })
    expect(cards.find(card => card.key === 'snapshot_on_error')).toMatchObject({
      value: 'Disabled',
      scenarioValue: 'Disabled',
      isDefault: false,
    })
    expect(buildRunDefaultEditDraft('continue_on_error', scenario, overrides).value).toBe('false')
    expect(buildGroupRuntimeTableValues(scenario.scripts[0], scenario, overrides).errorHandling).toBe('Stop / No snapshot')

    // With no section anywhere the run really does inherit the script's own config.
    const noSection = { ...scenario, runtime_settings: { iterations: 3 } }
    const inheritedCards = buildRunDefaultCards(noSection, overrides)
    expect(inheritedCards.find(card => card.key === 'continue_on_error')).toMatchObject({
      value: 'Script default',
      scenarioValue: 'Script default',
      isDefault: true,
    })
    expect(buildRunDefaultEditDraft('snapshot_on_error', noSection, overrides).value).toBe('')
  })

  it('builds effective groups and create payloads from group overrides', () => {
    const scenario = scenarioFixture()
    const firstGroup = scenario.scripts[0]
    const overrides: Record<string, GroupRunOverride> = {
      [groupOverrideKey(firstGroup)]: {
        vusers: 25,
        ramp_up: '2m',
        runtime_settings: {
          iterations: 4,
          pacing: { type: 'fixed_delay', delay_ms: 250 },
        },
        assignment: {
          mode: 'specific_hosts',
          selected_targets: ['gen-a:9090', 'gen-b:9090'],
          shard_strategy: 'capacity_weighted',
        },
      },
    }

    const effectiveGroups = buildEffectiveScenarioGroups(scenario, overrides)
    const payload = buildGroupsForRunPayload(scenario, overrides)

    expect(effectiveGroups[0].vusers).toBe(25)
    expect(scenario.scripts[0].vusers).toBe(10)
    expect(payload?.[0]).toMatchObject({
      name: 'Browse',
      script_id: 'script-1',
      vusers: 25,
      ramp_up: '2m',
      iterations: undefined,
      initialization: 'at_runtime',
      runtime_settings: {
        iterations: 4,
        pacing: { type: 'fixed_delay', delay_ms: 250 },
      },
      assignment: {
        mode: 'specific_hosts',
        selected_targets: ['gen-a:9090', 'gen-b:9090'],
        shard_strategy: 'capacity_weighted',
      },
    })
    expect(payload?.[1]).toMatchObject({
      name: 'Checkout',
      script_id: 'script-2',
      role: 'observer',
    })
  })

  it('derives staged group VUsers from the immutable peak stage target', () => {
    const scenario = scenarioFixture()
    const group = scenario.scripts[0]
    group.vusers = 30
    group.stages = [
      { type: 'ramp_up', duration: '1m', target_vusers: 10 },
      { type: 'hold', duration: '5m', target_vusers: 30 },
      { type: 'ramp_down', duration: '1m', target_vusers: 0 },
    ]
    const override = { vusers: 99, runtime_settings: { iterations: 4 } }
    const overrides = { [groupOverrideKey(group)]: override }

    expect(stagedGroupPeakVUsers(group)).toBe(30)
    expect(buildEffectiveScenarioGroups(scenario, overrides)[0].vusers).toBe(30)
    expect(buildGroupEditDraft(group, scenario, { runtime_settings: {} }, override).vusers).toBe('30')
    expect(buildGroupsForRunPayload(scenario, overrides)?.[0]).toMatchObject({
      vusers: 30,
      stages: group.stages,
      runtime_settings: { iterations: 4 },
    })
  })

  it('merges a partial per-run group edit over the complete stored group runtime', () => {
    const scenario = scenarioFixture()
    scenario.scripts[0].runtime_settings = {
      log: { level: 'extended', extended_options: ['data_returned'] },
      error_handling: { continue_on_error: true, max_retries: 3, retry_backoff: 'fixed' },
      http: { version: '2', tls: { min_version: '1.2' } },
      browser_config: { engine: 'chromium', client_metrics: true },
      env: { TOKEN: '[REDACTED]' },
    }
    const payload = buildGroupsForRunPayload(scenario, {
      [groupOverrideKey(scenario.scripts[0])]: { runtime_settings: { iterations: 12 } },
    })
    expect(payload?.[0].runtime_settings).toMatchObject({
      iterations: 12,
      log: { level: 'extended', extended_options: ['data_returned'] },
      error_handling: { continue_on_error: true, max_retries: 3, retry_backoff: 'fixed' },
      http: { version: '2', tls: { min_version: '1.2' } },
      browser_config: { engine: 'chromium', client_metrics: true },
      env: { TOKEN: '[REDACTED]' },
    })
  })

  it('round-trips weighted placement and generator-loss tolerance in the group edit draft', () => {
    const scenario = scenarioFixture()
    scenario.scripts[0].assignment = {
      mode: 'region_weighted',
      region_weights: { east: 70, west: 30 },
      required_labels: { runtime: 'go' },
      tolerate_generator_loss: true,
    }
    const draft = buildGroupEditDraft(scenario.scripts[0], scenario, { runtime_settings: {} })
    expect(draft).toMatchObject({
      assignmentMode: 'region_weighted',
      regionWeights: { east: 70, west: 30 },
      requiredLabels: { runtime: 'go' },
      tolerateGeneratorLoss: true,
    })
    expect(formatGroupPlacement(undefined, scenario.scripts[0])).toContain('Region weighted')
  })

  it('models load profile timing including resource baseline windows', () => {
    const preview = buildRunLoadProfilePreview(scenarioFixture().scripts, '10m', '30s', true)

    expect(preview.steadySeconds).toBe(600)
    expect(preview.preBaselineSeconds).toBe(330)
    expect(preview.postBaselineSeconds).toBe(300)
    expect(preview.totalSeconds).toBe(1260)
    expect(preview.peakVUsers).toBe(15)
    expect(preview.showVUsers).toBe(true)
    expect(preview.points[0]).toMatchObject({ second: 0, total: 0 })
    expect(preview.points[preview.points.length - 1]).toMatchObject({ second: 1260, total: 0 })
  })

  it('stops a late group\'s ramp-up at the deadline and ramps it down from the level it reached', () => {
    const base = { ...scenarioFixture().scripts[0], stages: undefined }
    // Activates at 9m with a 2m ramp: at the 10m deadline it reached half of
    // its 20 VUsers, and ramps down from there.
    const late = buildRunLoadProfilePreview([{ ...base, vusers: 20, start_offset: '9m', ramp_up: '2m', ramp_down: '1m' }], '10m', undefined, false)
    const at = (second: number) => late.points.find(point => point.second === second)?.total
    expect(at(600)).toBe(10)
    expect(Math.max(...late.points.filter(point => point.second > 600).map(point => point.total))).toBeLessThanOrEqual(10)
    expect(at(660)).toBe(0)
    // Activating at the deadline, it never ran: nothing to ramp down.
    const never = buildRunLoadProfilePreview([{ ...base, vusers: 20, start_offset: '10m', ramp_up: '0s', ramp_down: '1m' }], '10m', undefined, false)
    expect(never.points.every(point => point.total === 0)).toBe(true)
  })

  it('renders authored load stages instead of the legacy simple ramp fields', () => {
    const group = {
      ...scenarioFixture().scripts[0],
      vusers: 20,
      ramp_up: '10m',
      ramp_down: '10m',
      stages: [
        { type: 'ramp_up', duration: '1m', target_vusers: 20 },
        { type: 'hold', duration: '1m', target_vusers: 20 },
        { type: 'ramp_down', duration: '1m', target_vusers: 0 },
      ],
    }

    const preview = buildRunLoadProfilePreview([group], '3m', undefined, false)

    expect(preview.totalSeconds).toBe(180)
    expect(preview.points.find(point => point.second === 0)?.group0).toBe(0)
    expect(preview.points.find(point => point.second === 60)?.group0).toBe(20)
    expect(preview.points.find(point => point.second === 120)?.group0).toBe(20)
    expect(preview.points.find(point => point.second === 180)?.group0).toBe(0)
    expect(preview.peakVUsers).toBe(20)
  })

  it('includes the production TPS curve in the New Test load preview', () => {
    const group = { ...scenarioFixture().scripts[0], vusers: 9, ramp_up: '', ramp_down: '' }
    const trafficProfile: TrafficProfile = {
      version: 1,
      source: { kind: 'file', file: { filename: 'traffic.csv', timestamp_column: 'time', value_columns: ['transactions'] } },
      value_semantics: 'transactions_per_bucket', runtime_target: 'tps', interpolation: 'step', source_duration: '3m', target_duration: '3m', refreshed_at: '2026-08-31T00:00:00Z',
      mappings: [{ series_key: 'requests', series_name: 'Requests', group_name: 'Browse', peak_target: 12, points: [{ offset_ms: 0, target: 3 }, { offset_ms: 60_000, target: 12 }, { offset_ms: 120_000, target: 5 }] }],
      sizing: { complete: true, profile_peak_vusers: 18, static_peak_vusers: 0, estimated_peak_vusers: 18, suggested_min_vusers: 1, suggested_max_vusers: 36, recommended_generators: 2 },
    }

    const preview = buildRunLoadProfilePreview([group], '3m', undefined, false, trafficProfile, 'goal_oriented')

    expect(preview.points.find(point => point.second === 0)).toMatchObject({ total: 9, totalTps: 3 })
    expect(preview.points.find(point => point.second === 60)?.totalTps).toBe(12)
    expect(preview.points.find(point => point.second === 120)?.totalTps).toBe(5)
    expect(preview.points.find(point => point.second === 180)?.totalTps).toBe(0)
    expect(preview.peakTps).toBe(12)
    expect(preview.minTps).toBe(3)
    expect(preview.averageTps).toBeCloseTo(20 / 3)
    expect(preview.showVUsers).toBe(false)
    expect(preview.estimatedPeakVUsers).toBe(18)
    expect(preview.recommendedGenerators).toBe(2)
    expect(preview.tpsGroupIndexes).toEqual([0])

    const delayedPreview = buildRunLoadProfilePreview([group], '3m', '30s', true, trafficProfile)
    expect(delayedPreview.points.find(point => point.second === 0)?.totalTps).toBe(0)
    expect(delayedPreview.points.find(point => point.second === 330)?.totalTps).toBe(3)

    const staleRampPreview = buildRunLoadProfilePreview([{ ...group, ramp_down: '2m' }], '3m', undefined, false, trafficProfile)
    expect(staleRampPreview.totalSeconds).toBe(180)

    const multiGroupProfile: TrafficProfile = {
      ...trafficProfile,
      mappings: [
        ...trafficProfile.mappings,
        { series_key: 'search', series_name: 'Search', group_name: 'Search', peak_target: 7, points: [{ offset_ms: 0, target: 2 }, { offset_ms: 60_000, target: 7 }, { offset_ms: 120_000, target: 4 }] },
      ],
    }
    const multiGroupPreview = buildRunLoadProfilePreview([group, { ...group, name: 'Search', vusers: 4 }], '3m', undefined, false, multiGroupProfile)
    expect(multiGroupPreview.points.find(point => point.second === 60)).toMatchObject({ group0Tps: 12, group1Tps: 7, totalTps: 19 })
    expect(multiGroupPreview.minTps).toBe(5)
    expect(multiGroupPreview.averageTps).toBeCloseTo(11)
    expect(multiGroupPreview.tpsGroupIndexes).toEqual([0, 1])
  })

  it('renders the configured TPS target for an ordinary goal-oriented scenario', () => {
    const group = { ...scenarioFixture().scripts[0], vusers: 9, ramp_up: '', ramp_down: '' }
    const preview = buildRunLoadProfilePreview([group], '3m', undefined, false, undefined, 'goal_oriented', 75)

    expect(preview.showVUsers).toBe(false)
    expect(preview.runtimeTarget).toBe('tps')
    expect(preview.peakTps).toBe(75)
    expect(preview.minTps).toBe(75)
    expect(preview.averageTps).toBe(75)
    expect(preview.points.find(point => point.second === 0)?.totalTps).toBe(75)
    expect(preview.points.find(point => point.second === 180)?.totalTps).toBe(0)
    expect(preview.tpsGroupIndexes).toEqual([])
  })

  it('formats placement summaries and generator eligibility', () => {
    const scenario = scenarioFixture()
    const pinned: GroupRunOverride = { assignment: { mode: 'specific_hosts', selected_targets: ['gen-a:9090'] } }
    const sharded: GroupRunOverride = { assignment: { mode: 'specific_hosts', selected_targets: ['gen-a:9090', 'gen-b:9090'] } }
    const requiredJvm: GroupRunOverride = { assignment: { mode: 'auto', required_labels: { jvm: '17' } } }

    expect(formatGroupPlacement()).toBe('Auto')
    expect(formatGroupPlacement(pinned)).toBe('Pinned: gen-a:9090')
    expect(formatGroupPlacement(sharded)).toBe('Sharded: 2 hosts, capacity weighted')
    expect(formatGroupPlacement(requiredJvm)).toBe('Capability: jvm=17')
    expect(groupPlacementSummary({ one: pinned, two: sharded })).toBe('1 pinned group, 1 sharded group')
    const savedPinned = { ...scenario.scripts[0], assignment: { mode: 'specific_hosts' as const, selected_targets: ['gen-a:9090'] } }
    expect(groupPlacementSummary({ [groupOverrideKey(savedPinned)]: { assignment: { mode: 'auto' } } }, [
      savedPinned,
      { ...scenario.scripts[1], assignment: { mode: 'auto', required_labels: { jvm: '17' } } },
    ])).toBe('1 capability-constrained group')

    expect(generatorCanRunGroup(generatorFixture(), scenario.scripts[0])).toBe(true)
    expect(generatorCanRunGroup(generatorFixture({ supported_protocols: ['http'] }), scenario.scripts[1])).toBe(false)
    expect(generatorDisabledReason(generatorFixture({ supported_protocols: ['http'] }), scenario.scripts[1])).toBe('Does not support browser')
    expect(selectedGeneratorCapacity([
      generatorFixture({ target: 'gen-a:9090', available_capacity: 20 }),
      generatorFixture({ target: 'gen-b:9090', available_capacity: 30 }),
    ], ['gen-b:9090'])).toBe(30)
  })

  it('treats explicit auto as a run-only group placement override', () => {
    const scenario = scenarioFixture()
    scenario.scripts[0].assignment = { mode: 'specific_hosts', selected_targets: ['gen-a:9090'] }
    const override: GroupRunOverride = { assignment: { mode: 'auto' } }

    const effective = buildEffectiveScenarioGroups(scenario, { [groupOverrideKey(scenario.scripts[0])]: override })
    const payload = buildGroupsForRunPayload(scenario, { [groupOverrideKey(scenario.scripts[0])]: override })

    expect(effective[0].assignment).toEqual({ mode: 'auto' })
    expect(payload?.[0].assignment).toEqual({ mode: 'auto' })
    expect(formatGroupPlacement(override, scenario.scripts[0])).toBe('Auto')
  })

  it('builds run default cards with override status', () => {
    const cards = buildRunDefaultCards(scenarioFixture(), {
      duration: '20m',
      resource_baseline: true,
      runtime_settings: {
        log_level: 'debug',
      },
    })

    expect(cards.find(card => card.key === 'duration')).toMatchObject({ value: '20m', isOverridden: true })
    expect(cards.find(card => card.key === 'resource_baseline')).toMatchObject({ value: '5m before and after', isOverridden: true })
    expect(cards.find(card => card.key === 'log_level')).toMatchObject({ value: 'debug', scenarioValue: 'info', isOverridden: true })
  })
})

describe('run-launch overlays on partial (override_fields) rows', () => {
  type PartialRow = RuntimeSettingsOverride & { override_fields?: string[] }
  function partialScenario(): StoredScenario {
    const scenario = scenarioFixture()
    scenario.runtime_settings = {
      http: { request_timeout_ms: 5000 },
      error_handling: { continue_on_error: false },
      override_fields: ['http.request_timeout_ms', 'error_handling.continue_on_error'],
    } as PartialRow as RuntimeSettingsOverride
    scenario.scripts[0].runtime_settings = {
      http: { connect_timeout_ms: 750 },
      pacing: { type: 'fixed', delay_ms: 100 },
      override_fields: ['http.connect_timeout_ms', 'pacing'],
    } as PartialRow as RuntimeSettingsOverride
    return scenario
  }

  it('lists every run default it applies to a partial scenario row', () => {
    const payload = buildRuntimeSettingsForRun(partialScenario(), {
      runtime_settings: {
        pacing: { type: 'fixed', delay_ms: 2000 },
        think_time: { type: 'ignore' },
        log_level: 'debug',
        iterations: 4,
        auto_transactions: 'actions',
        continue_on_error: true,
        snapshot_on_error: true,
      },
    }) as PartialRow
    expect(payload.override_fields).toEqual(expect.arrayContaining([
      'http.request_timeout_ms',
      'pacing',
      'think_time',
      'log.level',
      'run_logic.iterations',
      'run_logic.auto_transactions',
      'error_handling.continue_on_error',
      'error_handling.snapshot_on_error',
    ]))
    expect(payload.override_fields).toHaveLength(8)
    expect(payload.pacing).toEqual({ type: 'fixed', delay_ms: 2000 })
    expect(payload.log?.level).toBe('debug')
    expect(payload.run_logic).toEqual({ iterations: 4, auto_transactions: 'actions' })
    expect(payload.error_handling).toEqual({ continue_on_error: true, snapshot_on_error: true })
    expect(payload.http).toEqual({ request_timeout_ms: 5000 })
  })

  it('turns a run-level clear on a partial scenario row into a reset, not a zero section', () => {
    const payload = buildRuntimeSettingsForRun(partialScenario(), {
      runtime_settings: { continue_on_error: null },
    }) as PartialRow
    expect(payload.override_fields).toEqual(['http.request_timeout_ms'])
    expect(payload.error_handling).toBeUndefined()
  })

  it('keeps the v1 section assignment for a v1 scenario row', () => {
    const payload = buildRuntimeSettingsForRun(scenarioFixture(), {
      runtime_settings: { pacing: { type: 'fixed', delay_ms: 2000 } },
    }) as PartialRow
    expect(payload.override_fields).toBeUndefined()
    expect(payload.pacing).toEqual({ type: 'fixed', delay_ms: 2000 })
    expect(payload.iterations).toBe(3)
  })

  it('lists group run overrides laid over a partial group row', () => {
    const scenario = partialScenario()
    const group = scenario.scripts[0]
    // The group edit dialog stores the merged row plus its edits as the overlay.
    const overlay = mergeRuntimeSettingsForRun(group.runtime_settings) as PartialRow
    overlay.iterations = 9
    overlay.log_level = 'trace'
    overlay.think_time = { type: 'ignore' }
    overlay.error_handling = { continue_on_error: true }
    overlay.pacing = null
    const runtime = buildGroupsForRunPayload(scenario, { [groupOverrideKey(group)]: { runtime_settings: overlay } })?.[0].runtime_settings as PartialRow
    expect([...(runtime.override_fields || [])].sort()).toEqual([
      'error_handling.continue_on_error',
      'http.connect_timeout_ms',
      'log.level',
      'run_logic.iterations',
      'think_time',
    ])
    expect(runtime.pacing).toBeUndefined()
    expect(runtime.http).toEqual({ connect_timeout_ms: 750 })
    expect(runtime.run_logic).toEqual({ iterations: 9 })
    expect(runtime.log).toEqual({ level: 'trace' })
  })

  it('keeps a run-level clear of the only listed path as a defined, empty row', () => {
    const scenario = scenarioFixture()
    scenario.runtime_settings = {
      pacing: { type: 'fixed', delay_ms: 100 },
      override_fields: ['pacing'],
    } as PartialRow as RuntimeSettingsOverride
    const payload = buildRuntimeSettingsForRun(scenario, { runtime_settings: { pacing: null } }) as PartialRow | undefined
    // undefined would leave runtime_settings out of the create request, and the API would then
    // clone the scenario's stored row back in, restoring the pacing the operator cleared.
    expect(payload).toBeDefined()
    expect(payload?.pacing).toBeUndefined()
    expect(resolveRuntimeOverrideFields(payload)).not.toContain('pacing')
    expect(payload !== undefined ? { runtime_settings: payload } : {}).toHaveProperty('runtime_settings')
  })

  it('builds a partial row listing only the edits for a group with no stored row', () => {
    const row = buildPartialGroupRunRuntimeOverride(undefined, undefined, [
      { path: 'run_logic.iterations', value: 7 },
      { path: 'browser.device_profile', value: 'iphone' },
    ]) as PartialRow
    expect(resolveRuntimeOverrideFields(row).sort()).toEqual(['browser.device_profile', 'run_logic.iterations'])
    const scenario = partialScenario()
    const group = { ...scenario.scripts[0], runtime_settings: undefined }
    scenario.scripts = [group]
    const runtime = buildGroupsForRunPayload(scenario, { [groupOverrideKey(group)]: { runtime_settings: row } })?.[0].runtime_settings as PartialRow
    expect(resolveRuntimeOverrideFields(runtime).sort()).toEqual(['browser.device_profile', 'run_logic.iterations'])
    expect(runtime.run_logic).toEqual({ iterations: 7 })
  })

  it('keeps a v1 group row\'s whole sections when a partial edit is laid over it', () => {
    const scenario = partialScenario()
    const group = { ...scenario.scripts[0], runtime_settings: { run_logic: { iterations: 3, action_sequence: ['a'] }, pacing: { type: 'fixed', delay_ms: 100 } } as RuntimeSettingsOverride }
    scenario.scripts = [group]
    const row = buildPartialGroupRunRuntimeOverride(group.runtime_settings, undefined, [
      { path: 'run_logic.iterations', value: 7 },
      { path: 'pacing', value: undefined },
      { path: 'log.level', value: 'debug' },
    ])
    const runtime = buildGroupsForRunPayload(scenario, { [groupOverrideKey(group)]: { runtime_settings: row } })?.[0].runtime_settings as PartialRow
    expect(resolveRuntimeOverrideFields(runtime).sort()).toEqual(['log.level', 'run_logic'])
    expect(runtime.run_logic).toEqual({ iterations: 7, action_sequence: ['a'] })
    expect(runtime.pacing).toBeUndefined()
  })

  it('drops an Inherit of both error-handling halves over a v1 group section (no stale v1 section)', () => {
    const scenario = scenarioFixture()
    scenario.runtime_settings = {
      error_handling: { snapshot_on_error: true },
      override_fields: ['error_handling.snapshot_on_error'],
    } as PartialRow as RuntimeSettingsOverride
    const group = { ...scenario.scripts[0], runtime_settings: { error_handling: { continue_on_error: false } } as RuntimeSettingsOverride }
    scenario.scripts = [group]
    const row = buildPartialGroupRunRuntimeOverride(group.runtime_settings, undefined, [
      { path: 'error_handling.continue_on_error', value: undefined },
      { path: 'error_handling.snapshot_on_error', value: undefined },
    ])
    const runtime = buildGroupsForRunPayload(scenario, { [groupOverrideKey(group)]: { runtime_settings: row } })?.[0].runtime_settings as PartialRow
    const fields = resolveRuntimeOverrideFields(runtime)
    expect(fields).not.toContain('error_handling')
    expect(fields).not.toContain('error_handling.continue_on_error')
    expect(fields).not.toContain('error_handling.snapshot_on_error')
    expect(runtime.error_handling?.continue_on_error).toBeUndefined()
    // The table now reports the scenario's listed snapshot and the script's continue.
    const table = buildGroupRuntimeTableValues(group, scenario, buildEmptyRunDefaultOverrides(), { runtime_settings: row })
    expect(table.errorHandling).toBe('Script default / Snapshot')
  })

  it('resolves a partial group error-handling half per path over the scenario', () => {
    const scenario = scenarioFixture()
    scenario.runtime_settings = {
      error_handling: { snapshot_on_error: true },
      override_fields: ['error_handling.snapshot_on_error'],
    } as PartialRow as RuntimeSettingsOverride
    const group = { ...scenario.scripts[0], runtime_settings: undefined }
    scenario.scripts = [group]
    const override: GroupRunOverride = {
      runtime_settings: { error_handling: { continue_on_error: true }, override_fields: ['error_handling.continue_on_error'] } as PartialRow as RuntimeSettingsOverride,
    }
    const runDefaults = buildEmptyRunDefaultOverrides()
    expect(buildGroupRuntimeTableValues(group, scenario, runDefaults, override).errorHandling).toBe('Continue / Snapshot')
    const draft = buildGroupEditDraft(group, scenario, runDefaults, override)
    expect(draft.continue_on_error).toBe('true')
    expect(draft.snapshot_on_error).toBe('true')
  })

  it('shows the script default for a half a partial scenario row does not list on the run-default cards', () => {
    const scenario = scenarioFixture()
    scenario.runtime_settings = {
      error_handling: { continue_on_error: true },
      override_fields: ['error_handling.continue_on_error'],
    } as PartialRow as RuntimeSettingsOverride
    const cards = buildRunDefaultCards(scenario, buildEmptyRunDefaultOverrides())
    const snapshot = cards.find(card => card.key === 'snapshot_on_error')
    const cont = cards.find(card => card.key === 'continue_on_error')
    expect(snapshot?.isDefault).toBe(true)
    expect(snapshot?.scenarioValue).toBe(cards.find(card => card.key === 'snapshot_on_error')?.value)
    expect(cont?.isDefault).toBe(false)
    expect(getEffectiveRuntimeDefaults(scenario, buildEmptyRunDefaultOverrides()).snapshot_on_error).toBeUndefined()
  })

  it('keeps the scenario device profile when a partial group lists only browser.headless', () => {
    const scenario = scenarioFixture()
    scenario.runtime_settings = {
      browser_config: { device_profile: 'iphone-15' },
      override_fields: ['browser.device_profile'],
    } as PartialRow as RuntimeSettingsOverride
    const group = { ...scenario.scripts[0], protocol: 'browser', runtime_settings: undefined } as typeof scenario.scripts[0]
    scenario.scripts = [group]
    const override: GroupRunOverride = {
      runtime_settings: { browser_config: { headless: true }, override_fields: ['browser.headless'] } as PartialRow as RuntimeSettingsOverride,
    }
    const runDefaults = buildEmptyRunDefaultOverrides()
    expect(buildGroupRuntimeTableValues(group, scenario, runDefaults, override).browser).toBe('iphone-15 · Headless')
    expect(buildGroupEditDraft(group, scenario, runDefaults, override).browser_device_profile).toBe('iphone-15')
  })

  it('reads a reset log level off the merged row rather than the stored group', () => {
    const scenario = partialScenario()
    const group = { ...scenario.scripts[0], runtime_settings: { log: { level: 'debug' }, override_fields: ['log.level'] } as PartialRow as RuntimeSettingsOverride }
    scenario.scripts = [group]
    const row = buildPartialGroupRunRuntimeOverride(group.runtime_settings, undefined, [{ path: 'log.level', value: undefined }])
    const table = buildGroupRuntimeTableValues(group, scenario, buildEmptyRunDefaultOverrides(), { runtime_settings: row })
    expect(table.logLevel).toBe('Standard')
  })

  it('makes the error-handling inherit per half only for partial rows', () => {
    const draft = { continue_on_error: 'false', snapshot_on_error: 'true' }
    expect(applyGroupErrorHandlingSelect(draft, 'snapshot_on_error', '', true)).toEqual({ continue_on_error: 'false', snapshot_on_error: '' })
    expect(applyGroupErrorHandlingSelect(draft, 'snapshot_on_error', '', false)).toEqual({ continue_on_error: '', snapshot_on_error: '' })
  })

  describe('partial pacing and think_time rows', () => {
    function typedTimingScenario(): StoredScenario {
      const scenario = scenarioFixture()
      scenario.runtime_settings = {
        pacing: { type: 'fixed_delay', delay_ms: 100 },
        think_time: { type: 'random', mean_ms: 200 },
      }
      scenario.scripts = [{ ...scenario.scripts[0], runtime_settings: undefined }]
      return scenario
    }
    const leafGroupRow = {
      pacing: { delay_ms: 500 },
      think_time: { mean_ms: 800 },
      override_fields: ['pacing.delay_ms', 'think_time.mean_ms'],
    } as PartialRow as RuntimeSettingsOverride

    it('lays a group row listing only pacing.delay_ms and think_time.mean_ms over the typed scenario sections', () => {
      const scenario = typedTimingScenario()
      const group = { ...scenario.scripts[0], runtime_settings: leafGroupRow }
      scenario.scripts = [group]
      const runDefaults = buildEmptyRunDefaultOverrides()
      const wholeGroup = { ...group, runtime_settings: { pacing: { type: 'fixed_delay', delay_ms: 500 }, think_time: { type: 'random', mean_ms: 800 } } as RuntimeSettingsOverride }
      const table = buildGroupRuntimeTableValues(group, scenario, runDefaults)
      expect(table.pacing).toBe(buildGroupRuntimeTableValues(wholeGroup, scenario, runDefaults).pacing)
      expect(table.pacing).not.toBe('Script timing')
      expect(table.thinkTime).toBe('random · 800ms')

      const draft = buildGroupEditDraft(group, scenario, runDefaults)
      expect(draft.pacingType).toBe('fixed_delay')
      expect(draft.pacingConfig).toEqual({ type: 'fixed_delay', delay_ms: 500 })
      expect(draft.thinkTimeMs).toBe('800')
      expect(draft.thinkTimeConfig?.mean_ms).toBe(800)
      expect(draft.thinkTimeConfig?.type).not.toBe('fixed')
    })

    it('keeps resolving the stored leaves after a run-dialog edit of another field', () => {
      const scenario = typedTimingScenario()
      const group = { ...scenario.scripts[0], runtime_settings: leafGroupRow }
      scenario.scripts = [group]
      const runDefaults = buildEmptyRunDefaultOverrides()
      const override: GroupRunOverride = {
        runtime_settings: buildPartialGroupRunRuntimeOverride(group.runtime_settings, undefined, [{ path: 'log.level', value: 'debug' }]),
      }
      const table = buildGroupRuntimeTableValues(group, scenario, runDefaults, override)
      expect(table.thinkTime).toBe('random · 800ms')
      expect(table.logLevel).not.toBe('Standard')
      expect(buildGroupEditDraft(group, scenario, runDefaults, override).pacingType).toBe('fixed_delay')
    })

    it('keeps v1 whole-section semantics: a v1 group pacing section replaces the scenario section', () => {
      const scenario = typedTimingScenario()
      const group = { ...scenario.scripts[0], runtime_settings: { think_time: { mean_ms: 800 } } as RuntimeSettingsOverride }
      scenario.scripts = [group]
      const runDefaults = buildEmptyRunDefaultOverrides()
      expect(buildGroupRuntimeTableValues(group, scenario, runDefaults).thinkTime).toBe('fixed · 800ms')
    })

    it('reads only the listed leaves of a partial scenario row on the run-default cards', () => {
      const scenario = scenarioFixture()
      scenario.runtime_settings = {
        // The type is not listed, so the backend never applies it: the script's type runs.
        pacing: { type: 'tps', delay_ms: 500 },
        think_time: { type: 'random', mean_ms: 800 },
        override_fields: ['pacing.delay_ms', 'think_time.mean_ms'],
      } as PartialRow as RuntimeSettingsOverride
      const runDefaults = buildEmptyRunDefaultOverrides()
      const effective = getEffectiveRuntimeDefaults(scenario, runDefaults)
      expect(effective.pacing).toEqual({ delay_ms: 500 })
      expect(effective.think_time).toEqual({ mean_ms: 800 })
      const cards = buildRunDefaultCards(scenario, runDefaults)
      const pacing = cards.find(card => card.key === 'pacing')
      const thinkTime = cards.find(card => card.key === 'think_time')
      expect(pacing?.value).toBe('Script pacing · delay ms 500')
      expect(pacing?.scenarioValue).toBe('Script pacing · delay ms 500')
      expect(thinkTime?.value).toBe('Script think time · mean ms 800')
      expect(thinkTime?.value).not.toContain('random')
      expect(buildRunDefaultEditDraft('pacing', scenario, runDefaults).pacingType).toBe('')
    })

    it('shows a run-level pacing edit over a partial scenario row as the whole section', () => {
      const scenario = scenarioFixture()
      scenario.runtime_settings = { pacing: { delay_ms: 500 }, override_fields: ['pacing.delay_ms'] } as PartialRow as RuntimeSettingsOverride
      const runDefaults = buildEmptyRunDefaultOverrides()
      runDefaults.runtime_settings.pacing = { type: 'fixed_interval', interval_ms: 2000 }
      const cards = buildRunDefaultCards(scenario, runDefaults)
      expect(getEffectiveRuntimeDefaults(scenario, runDefaults).pacing).toEqual({ type: 'fixed_interval', interval_ms: 2000 })
      expect(cards.find(card => card.key === 'pacing')?.scenarioValue).toBe('Script pacing · delay ms 500')
    })
  })

  it('leaves an untouched partial group row byte-identical', () => {
    const scenario = partialScenario()
    const group = scenario.scripts[0]
    expect(mergeRuntimeSettingsForRun(group.runtime_settings, { ...group.runtime_settings })).toEqual(group.runtime_settings)
  })
})

describe('empty v1 sections keep whole-section semantics', () => {
  it('keeps a stored v1 error_handling: {} pinned through an unrelated partial edit', () => {
    const stored = { error_handling: {}, log: { level: 'debug' } } as RuntimeSettingsOverride
    const row = buildPartialGroupRunRuntimeOverride(stored, undefined, [{ path: 'log.level', value: 'trace' }]) as RuntimeSettingsOverride & { override_fields?: string[] }
    expect(row.override_fields).toEqual(expect.arrayContaining(['error_handling', 'log']))
    expect(row.error_handling).toEqual({})
    // The run payload merge of that (complete) partial overlay over the v1 row keeps the pin too.
    const merged = mergeRuntimeSettingsForRun(stored, row) as RuntimeSettingsOverride & { override_fields?: string[] }
    expect(merged.override_fields).toEqual(expect.arrayContaining(['error_handling']))
    expect(merged.error_handling).toEqual({})
  })

  it('shows an empty v1 think_time or pacing as the zero value, not the layer below', () => {
    const scenario = scenarioFixture()
    scenario.runtime_settings = { think_time: { type: 'random', mean_ms: 100 }, pacing: { type: 'fixed_delay', delay_ms: 100 } }
    const runDefaults = { runtime_settings: {} }
    const inherited = buildGroupRuntimeTableValues(scenario.scripts[0], scenario, runDefaults)
    const group = { ...scenario.scripts[0], runtime_settings: { think_time: {}, pacing: {} } as RuntimeSettingsOverride }
    const values = buildGroupRuntimeTableValues(group, scenario, runDefaults)
    expect(values.thinkTime).not.toBe(inherited.thinkTime)
    expect(values.thinkTime).not.toContain('100')
    expect(values.pacing).not.toBe(inherited.pacing)
    expect(values.pacing).not.toContain('0.1')
  })

  it('reads a v1 root error_handling: {} as a pinned scenario section', () => {
    expect(errorHandlingSectionIsPinned(effectiveRuntimeSection('error_handling', [{ error_handling: {} } as RuntimeSettingsOverride]))).toBe(true)
  })
})

describe('groupHiddenPartialTiming', () => {
  it('names an untyped partial group think time or pacing the dialog inherit option hides', () => {
    const scenario = scenarioFixture()
    scenario.runtime_settings = {}
    const group = {
      ...scenario.scripts[0],
      runtime_settings: { think_time: { mean_ms: 800 }, pacing: { delay_ms: 500 }, override_fields: ['think_time.mean_ms', 'pacing.delay_ms'] } as unknown as RuntimeSettingsOverride,
    }
    const runDefaults = { runtime_settings: {} }
    expect(groupHiddenPartialTiming('think_time', group, scenario, runDefaults)).toEqual({ summary: 'Script think time · mean ms 800', groupOwned: true })
    expect(groupHiddenPartialTiming('pacing', group, scenario, runDefaults)).toEqual({ summary: 'Script timing · delay ms 500', groupOwned: true })
    // Resetting the section path at the group drops the listed leaves.
    const reset = buildPartialGroupRunRuntimeOverride(group.runtime_settings, undefined, [{ path: 'think_time', value: undefined }]) as RuntimeSettingsOverride & { override_fields?: string[] }
    expect(reset.override_fields).toEqual(['pacing.delay_ms'])
    expect(reset.think_time).toBeUndefined()
  })

  it('reports a scenario-owned leaf as not resettable at the group, and nothing for a typed section', () => {
    const scenario = scenarioFixture()
    scenario.runtime_settings = { think_time: { mean_ms: 800 }, override_fields: ['think_time.mean_ms'] } as unknown as RuntimeSettingsOverride
    const runDefaults = { runtime_settings: {} }
    expect(groupHiddenPartialTiming('think_time', scenario.scripts[0], scenario, runDefaults)?.groupOwned).toBe(false)
    const typed = { ...scenario.scripts[0], runtime_settings: { think_time: { type: 'fixed', mean_ms: 5 }, override_fields: ['think_time'] } as RuntimeSettingsOverride }
    expect(groupHiddenPartialTiming('think_time', typed, scenario, runDefaults)).toBeUndefined()
  })
})
