import { describe, expect, it } from 'vitest'

import {
  applyGroupErrorHandlingDraftEdit,
  buildEffectiveScenarioGroups,
  buildGroupErrorHandlingSectionForRun,
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
  groupOverrideCount,
  groupOverrideKey,
  groupPlacementSummary,
  hasOwnKey,
  selectedGeneratorCapacity,
  setRuntimeBrowser,
  stagedGroupPeakVUsers,
  stampClearedRuntimeSections,
  type ClearableRuntimeSection,
  type GroupRunOverride,
} from './newTestRunModel'
import { type RuntimeSettingsOverride, type StoredScenario, type SystemInventoryGenerator, type TrafficProfile } from '../types'

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
