import { describe, expect, it } from 'vitest'

import {
  buildEffectiveScenarioGroups,
  buildGroupsForRunPayload,
  buildGroupEditDraft,
  buildGroupRuntimeTableValues,
  buildRunDefaultCards,
  buildRunDefaultEditDraft,
  buildRunLoadProfilePreview,
  buildRuntimeSettingsForRun,
  CLEARABLE_RUNTIME_SECTIONS,
  errorHandlingSectionIsPinned,
  errorHandlingSurvivesBooleanClear,
  formatGroupPlacement,
  generatorCanRunGroup,
  generatorDisabledReason,
  getEffectiveRuntimeDefaults,
  groupOverrideCount,
  groupOverrideKey,
  groupPlacementSummary,
  selectedGeneratorCapacity,
  stagedGroupPeakVUsers,
  stampClearedRuntimeSections,
  type ClearableRuntimeSection,
  type GroupRunOverride,
} from './newTestRunModel'
import { type RuntimeSettingsOverride, type StoredScenario, type SystemInventoryGenerator } from '../types'

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

    for (const section of CLEARABLE_RUNTIME_SECTIONS) {
      // Fails when a new clearable section is declared without being exercised below.
      expect(sectionFixtures[section]).toBeTruthy()
      expect(clearedTableValue[section]).toBeTruthy()

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

      // WRITE: the dialog's edit removed the section from the patch, so the shared writer has to
      // turn that absence into an explicit tombstone.
      const patch = stampClearedRuntimeSections({ log_level: 'debug' }, group.runtime_settings)
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
    expect(preview.points[0]).toMatchObject({ second: 0, total: 0 })
    expect(preview.points[preview.points.length - 1]).toMatchObject({ second: 1260, total: 0 })
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
