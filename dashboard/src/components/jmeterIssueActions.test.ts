import { describe, expect, it, vi } from 'vitest'

import { jmeterDiagnosticsWithGuidance, jmeterIssueAction, jmeterScriptIndex, jmeterSourceGroupIndex, jmeterSuggestionInFormWords, jmeterSuggestionTarget } from './jmeterIssueActions'

describe('jmeterIssueAction', () => {
  const context = (overrides = {}) => ({ batch: true, saved: false, openTimestampPolicy: vi.fn(), openBatchRepair: vi.fn(), ...overrides })

  it('opens the timestamp policy for the timestamp blocker', () => {
    const ctx = context()
    const action = jmeterIssueAction({ id: 'a', severity: 'blocker', title: 't', code: 'jmeter_timestamp_timezone_required' }, ctx)
    expect(action?.label).toBe('Choose a timestamp policy')
    action?.run?.()
    expect(ctx.openTimestampPolicy).toHaveBeenCalled()
  })

  it('opens the batch repair that clears a credential gate on its script', () => {
    const ctx = context()
    jmeterIssueAction({ id: 'a', severity: 'blocker', title: 't', code: 'jmeter_eventhubs_credentials_required', location: 'scripts[2].actions[0]' }, ctx)?.run?.()
    expect(ctx.openBatchRepair).toHaveBeenCalledWith('bind_eventhubs_credentials', 2)
    jmeterIssueAction({ id: 'b', severity: 'blocker', title: 't', code: 'jmeter_kafka_oauth_credentials_required', source: 'plan.jmx#scripts[0].actions[0]#P' }, ctx)?.run?.()
    expect(ctx.openBatchRepair).toHaveBeenLastCalledWith('bind_kafka_credentials', 0)
    jmeterIssueAction({ id: 'c', severity: 'blocker', title: 't', code: 'jmeter_reference_context_confirmation_required', location: 'scripts[1].actions[0]' }, ctx)?.run?.()
    expect(ctx.openBatchRepair).toHaveBeenLastCalledWith('confirm_reference_context', 1)
  })

  it('offers no action for gates nothing clears, and explains locked repairs', () => {
    expect(jmeterIssueAction({ id: 'a', severity: 'blocker', title: 't', code: 'jmeter_messaging_compatibility_unverified' }, context())).toBeUndefined()
    expect(jmeterIssueAction({ id: 'a', severity: 'blocker', title: 't', code: 'jmeter_kafka_batch_allocation_parity_unverified' }, context())).toBeUndefined()
    const saved = jmeterIssueAction({ id: 'a', severity: 'blocker', title: 't', code: 'jmeter_eventhubs_credentials_required' }, context({ saved: true }))
    expect(saved?.run).toBeUndefined()
    expect(saved?.resolution).toContain('already saved')
    const notBatch = jmeterIssueAction({ id: 'a', severity: 'blocker', title: 't', code: 'jmeter_eventhubs_credentials_required' }, context({ batch: false }))
    expect(notBatch?.run).toBeUndefined()
    expect(notBatch?.resolution).toContain('cannot be applied here')
  })

  it('words a batch repair\'s resolution by the form\'s labels, not the API operation and field names', () => {
    const resolution = (code: string) => jmeterIssueAction({ id: code, severity: 'blocker', title: 't', code }, context())?.resolution || ''
    expect(resolution('jmeter_kafka_oauth_credentials_required')).toContain('apply the repair "Bind Kafka credential references"')
    expect(resolution('jmeter_eventhubs_credentials_required')).toContain('apply the repair "Bind Event Hubs credential reference"')
    expect(resolution('jmeter_kafka_nonstandard_oauth_options')).toContain('Apply the repair "Normalize Kafka OAuth option names"')
    expect(resolution('jmeter_reference_context_confirmation_required')).toContain('Apply the repair "Confirm the reference timezone and no property overrides"')
    for (const code of ['jmeter_kafka_oauth_credentials_required', 'jmeter_eventhubs_credentials_required', 'jmeter_kafka_nonstandard_oauth_options', 'jmeter_reference_context_confirmation_required']) {
      expect(resolution(code)).not.toMatch(/bind_kafka_credentials|bind_eventhubs_credentials|normalize_oauth_options|confirm_reference_context|client_id_secret|secret_name/)
    }
  })

  it('sends a non-batch review\'s credential gate to the saved script\'s binding, not to the Credentials tab', () => {
    for (const code of ['jmeter_kafka_oauth_credentials_required', 'jmeter_eventhubs_credentials_required']) {
      const action = jmeterIssueAction({ id: code, severity: 'blocker', title: 't', code }, context({ batch: false }))
      expect(action?.run).toBeUndefined()
      expect(action?.resolution).toContain('Save the scripts, then open the saved script in the Visual Editor and bind the credentials from the blocked step\'s repair')
      expect(action?.resolution).toContain('The Credentials tab shows the references but does not clear this gate')
      expect(action?.resolution).not.toContain('import the plan again once artifact storage is available')
    }
    // A gate only the batch repairs clear still needs a new import.
    const timezone = jmeterIssueAction({ id: 'tz', severity: 'blocker', title: 't', code: 'jmeter_reference_context_confirmation_required' }, context({ batch: false }))
    expect(timezone?.resolution).toContain('import the plan again once artifact storage is available')
  })

  it('reads the script index from a location', () => {
    expect(jmeterScriptIndex('scripts[12].actions[0]')).toBe(12)
    expect(jmeterScriptIndex('actions[0]')).toBe(0)
    expect(jmeterScriptIndex()).toBe(0)
  })
})

describe('jmeterIssueAction with the API suggestions', () => {
  const scripts = [
    { name: 'Kafka', protocol: 'kafka', config: { jmeter: { source_group_id: 'g0' } }, actions: [] },
    { name: 'Hub', protocol: 'eventhubs', config: { jmeter: { source_group_id: 'g1' } }, actions: [] },
  ] as never
  const suggestions = [
    { id: 'jmeter:jmeter_eventhubs_credentials_required:g1', code: 'jmeter_eventhubs_credentials_required', category: 'review_gate', severity: 'blocker', title: 'Bind the Event Hubs connection string', description: 'D', action: 'Create a project secret, then apply the bind_eventhubs_credentials repair.', target_action: 'Publish', clearable: true, batch_operation: 'bind_eventhubs_credentials', source_group_id: 'g1' },
    { id: 'jmeter:jmeter_messaging_compatibility_unverified', code: 'jmeter_messaging_compatibility_unverified', category: 'review_gate', severity: 'blocker', title: 'JMeter messaging replay is not available yet', description: 'D', action: 'Nothing in the plan or the import can clear this gate in this release.', clearable: false },
    { id: 'jmeter:jmeter_timestamp_timezone_required', code: 'jmeter_timestamp_timezone_required', category: 'review_gate', severity: 'blocker', title: 'Choose how JMeter timestamps are evaluated', description: 'D', action: 'Convert the plan again with a timestamp policy.', clearable: true, reinspect_options: [{ field: 'time_mode', values: ['utc', 'source'] }, { field: 'timezone' }] },
  ]
  const context = (overrides = {}) => ({ batch: true, saved: false, suggestions, scripts, openTimestampPolicy: vi.fn(), openBatchRepair: vi.fn(), ...overrides })

  it('opens the suggestion\'s batch operation, says an unclearable gate cannot be cleared, and reinspects the timestamp policy', () => {
    const ctx = context()
    jmeterIssueAction({ id: 'a', severity: 'blocker', title: 't', code: 'jmeter_eventhubs_credentials_required', location: 'scripts[1].actions[0]' }, ctx)?.run?.()
    expect(ctx.openBatchRepair).toHaveBeenCalledWith('bind_eventhubs_credentials', 1)
    expect(jmeterIssueAction({ id: 'b', severity: 'blocker', title: 't', code: 'jmeter_messaging_compatibility_unverified' }, ctx)).toEqual({ unclearable: true })
    const timestamp = jmeterIssueAction({ id: 'c', severity: 'blocker', title: 't', code: 'jmeter_timestamp_timezone_required' }, ctx)
    timestamp?.run?.()
    expect(ctx.openTimestampPolicy).toHaveBeenCalled()
  })
})

describe('jmeterSuggestionInFormWords', () => {
  const suggestion = { id: 's', code: 'jmeter_kafka_oauth_credentials_required', category: 'review_gate', severity: 'blocker', title: 'Bind the Kafka OAuth client credentials', description: 'D', action: 'Create project secrets, then apply the bind_kafka_credentials repair with their names (client_id_secret and secret_name).', clearable: true, batch_operation: 'bind_kafka_credentials' }

  it('replaces a batch repair suggestion\'s action with the form\'s wording for the review it is shown in', () => {
    expect(jmeterSuggestionInFormWords(suggestion, { batch: true, saved: false }).action).toContain('apply the repair "Bind Kafka credential references"')
    expect(jmeterSuggestionInFormWords(suggestion, { batch: true, saved: true }).action).toContain('already saved')
    expect(jmeterSuggestionInFormWords(suggestion, { batch: false, saved: false }).action).toContain('open the saved script in the Visual Editor')
    // A suggestion no batch repair clears keeps the server's words.
    const timestamp = { ...suggestion, code: 'jmeter_timestamp_timezone_required', batch_operation: undefined, action: 'Convert the plan again.' }
    expect(jmeterSuggestionInFormWords(timestamp, { batch: true, saved: false })).toBe(timestamp)
  })
})

describe('jmeterSuggestionTarget', () => {
  const scripts = [
    { name: 'Kafka', protocol: 'kafka', config: { jmeter: { source_group_id: 'g0' } }, actions: [] },
    { name: 'Hub', protocol: 'eventhubs', config: { jmeter: { source_group_id: 'g1' } }, actions: [] },
  ] as never
  const suggestion = (fields: Record<string, unknown>) => ({ id: 's', category: 'review_gate', severity: 'blocker', title: 'T', description: 'D', action: 'Apply the repair named in the guidance.', ...fields })

  it('routes by batch_operation, source_group_id, reinspect_options and clearable, never by the action text', () => {
    expect(jmeterSuggestionTarget(suggestion({ batch_operation: 'normalize_oauth_options', source_group_id: 'g1', clearable: true }), scripts)).toEqual({ kind: 'batch_repair', type: 'normalize_oauth_options', group: 1 })
    // A plan-wide confirmation has no group and opens on the first script.
    expect(jmeterSuggestionTarget(suggestion({ batch_operation: 'confirm_reference_context', clearable: true }), scripts)).toEqual({ kind: 'batch_repair', type: 'confirm_reference_context', group: 0 })
    expect(jmeterSuggestionTarget(suggestion({ reinspect_options: [{ field: 'time_mode', values: ['utc', 'source'] }], clearable: true }), scripts)).toEqual({ kind: 'timestamp_policy' })
    expect(jmeterSuggestionTarget(suggestion({ clearable: false, batch_operation: 'normalize_oauth_options' }), scripts)).toEqual({ kind: 'not_clearable' })
    // action and target_action are resolution text and a step name now.
    expect(jmeterSuggestionTarget(suggestion({ action: 'normalize_oauth_options', target_action: 'time_mode' }), scripts)).toBeNull()
    expect(jmeterSourceGroupIndex(scripts, 'missing')).toBe(0)
  })
})

describe('jmeterDiagnosticsWithGuidance', () => {
  it('splits the server\'s "<Detail> <Resolution>" detail and attaches the resolution', () => {
    const suggestions = [{ id: 'x', code: 'jmeter_kafka_nonstandard_oauth_options', category: 'review_gate', severity: 'blocker', title: 'Rename the Kafka OAuth options', description: 'The options are named cid and cs.', action: 'Apply the normalize_oauth_options repair.' }]
    const [rewritten, own, other] = jmeterDiagnosticsWithGuidance([
      { code: 'jmeter_kafka_nonstandard_oauth_options', element: 'Producer', detail: 'The options are named cid and cs. Apply the normalize_oauth_options repair.', severity: 'blocker' },
      { code: 'jmeter_kafka_nonstandard_oauth_options', element: 'Producer', detail: 'A sentence the converter wrote.', severity: 'blocker', resolution: 'Its own resolution.' },
      { code: 'import_missing_variables', element: 'Login', detail: 'Declare host', severity: 'warning' },
    ], suggestions)!
    expect(rewritten).toMatchObject({ detail: 'The options are named cid and cs.', resolution: 'Apply the normalize_oauth_options repair.' })
    expect(own).toMatchObject({ detail: 'A sentence the converter wrote.', resolution: 'Its own resolution.' })
    expect(other.resolution).toBeUndefined()
    expect(jmeterDiagnosticsWithGuidance(undefined, suggestions)).toBeUndefined()
  })
})

describe('jmeterDiagnosticsWithGuidance with diagnostics[].resolution', () => {
  it('keeps the server\'s resolution field and strips it from a detail an older server appended it to', () => {
    const [field, transitional, noSuggestions] = jmeterDiagnosticsWithGuidance([
      { code: 'jmeter_eventhubs_credentials_required', element: 'Hub', detail: 'The connection string moved to a project secret.', resolution: 'Bind it to a project secret.', severity: 'blocker' },
      { code: 'jmeter_eventhubs_credentials_required', element: 'Hub', detail: 'The connection string moved to a project secret. Bind it to a project secret.', resolution: 'Bind it to a project secret.', severity: 'blocker' },
      { element: 'Hub', detail: 'No code. Do this.', resolution: 'Do this.', severity: 'warning' },
    ], null)!
    expect(field).toMatchObject({ detail: 'The connection string moved to a project secret.', resolution: 'Bind it to a project secret.' })
    expect(transitional).toMatchObject({ detail: 'The connection string moved to a project secret.', resolution: 'Bind it to a project secret.' })
    // Without suggestions too, and for a diagnostic without a code.
    expect(noSuggestions).toMatchObject({ detail: 'No code.', resolution: 'Do this.' })
  })

  it('prefers the resolution field over a suggestion\'s action and leaves an unchanged diagnostic as it is', () => {
    const suggestions = [{ id: 'x', code: 'jmeter_eventhubs_credentials_required', category: 'review_gate', severity: 'blocker', title: 'T', description: 'D', action: 'The suggestion\'s action.' }]
    const diagnostic = { code: 'jmeter_eventhubs_credentials_required', element: 'Hub', detail: 'Meaning only.', resolution: 'The field.', severity: 'blocker' as const }
    const [item] = jmeterDiagnosticsWithGuidance([diagnostic], suggestions)!
    expect(item).toBe(diagnostic)
  })
})

describe('jmeterIssueAction for a step awaiting runtime support', () => {
  const step = (codes: string[], resolution: string) => ({ name: 'Produce', type: 'kafka_produce', target: 'orders', metadata: { execution_blocker: 'import_review_required', import_review: { reason_codes: codes, resolution } } })
  const scripts = [
    { name: 'Kafka', protocol: 'kafka', config: { jmeter: { source_group_id: 'g0' } }, actions: [step(['jmeter_messaging_compatibility_unverified', 'jmeter_future_runtime_gate', 'jmeter_messaging_variable_unresolved', 'jmeter_kafka_oauth_credentials_required'], 'awaiting_runtime_support')] },
    { name: 'Other', protocol: 'kafka', config: { jmeter: { source_group_id: 'g1' } }, actions: [step(['jmeter_future_runtime_gate'], 'replace_behavior_and_remove_gate')] },
  ] as never
  const context = (overrides = {}) => ({ batch: true, saved: false, scripts, openTimestampPolicy: vi.fn(), openBatchRepair: vi.fn(), ...overrides })

  it('says a gate nothing clears cannot be cleared yet, from the step\'s resolution', () => {
    expect(jmeterIssueAction({ id: 'a', severity: 'blocker', title: 't', code: 'jmeter_messaging_compatibility_unverified', location: 'scripts[0].actions[0]' }, context())).toEqual({ unclearable: true })
    expect(jmeterIssueAction({ id: 'a2', severity: 'blocker', title: 't', code: 'import_review_required', location: 'scripts[0].actions[0]' }, context())).toEqual({ unclearable: true })
    // The same code on a step a change can clear offers nothing either way.
    expect(jmeterIssueAction({ id: 'b', severity: 'blocker', title: 't', code: 'jmeter_future_runtime_gate', location: 'scripts[1].actions[0]' }, context())).toBeUndefined()
    // A code the step does not hold, or a non-blocker, is not its gate.
    expect(jmeterIssueAction({ id: 'c', severity: 'blocker', title: 't', code: 'jmeter_other_gate', location: 'scripts[0].actions[0]' }, context())).toBeUndefined()
    expect(jmeterIssueAction({ id: 'd', severity: 'warning', title: 't', code: 'jmeter_future_runtime_gate', location: 'scripts[0].actions[0]' }, context())).toBeUndefined()
  })

  it('keeps the source fix for a source defect on that step and says its gate remains', () => {
    // A corrected source clears this blocker, so it is not "Cannot be cleared yet".
    const action = jmeterIssueAction({ id: 'a', severity: 'blocker', title: 't', code: 'jmeter_messaging_variable_unresolved', location: 'scripts[0].actions[0]' }, context())
    expect(action?.unclearable).toBeUndefined()
    expect(action?.resolution).toBe('Correct the source or configuration, then import it again. The step stays blocked by its messaging replay gate after this fix: nothing clears that gate in this release.')
    const withResolution = jmeterIssueAction({ id: 'b', severity: 'blocker', title: 't', code: 'jmeter_messaging_variable_unresolved', location: 'scripts[0].actions[0]', resolution: 'Define the variable in the plan.' }, context())
    expect(withResolution).toEqual({ resolution: 'Define the variable in the plan. The step stays blocked by its messaging replay gate after this fix: nothing clears that gate in this release.' })
  })

  it('keeps the repair for a clearable code on that step', () => {
    const ctx = context()
    jmeterIssueAction({ id: 'a', severity: 'blocker', title: 't', code: 'jmeter_kafka_oauth_credentials_required', location: 'scripts[0].actions[0]' }, ctx)?.run?.()
    expect(ctx.openBatchRepair).toHaveBeenCalledWith('bind_kafka_credentials', 0)
  })

  it('points a saved import\'s credential gate at the saved script\'s repair', () => {
    const saved = jmeterIssueAction({ id: 'a', severity: 'blocker', title: 't', code: 'jmeter_kafka_oauth_credentials_required', location: 'scripts[0].actions[0]' }, context({ saved: true }))
    expect(saved?.run).toBeUndefined()
    expect(saved?.resolution).toContain('already saved')
    expect(saved?.resolution).toContain('Open the saved script in the Visual Editor and bind the credentials from the blocked step\'s repair')
    const timezone = jmeterIssueAction({ id: 'b', severity: 'blocker', title: 't', code: 'jmeter_reference_context_confirmation_required' }, context({ saved: true }))
    expect(timezone?.resolution).toBe('These scripts are already saved, so the reviewed repairs for all scripts are locked. Import the plan again and apply the repair before saving to clear this gate.')
  })
})
