import { describe, expect, it } from 'vitest'
import type {
  ScriptActionDefinition,
  ScriptDefinition,
  ScriptImportMetadata,
  SourceArchiveReview,
} from '../types/scripts'
import { EMPTY_SCRIPT } from './scriptEditorUtils'
import {
  applyValidationRuntimeSettingsMetadata,
  buildImportedCorrelationFocusTargets,
  buildImportedDraftDiffSummary,
  buildImportedParameterFileMappings,
  buildWeb2ObjectDescriptorReview,
  findActionIndexByImportedLocation,
  findActionIndexByName,
  findDataSourceIndexByName,
  findFirstParameterIndexByDataSource,
  findImportedParameterMappingDataSourceIndex,
  findSourceMappingForAction,
  formatImportedLocationLabel,
  formatImportParameterMapping,
  formatPromotionCount,
  formatRuntimeSettingStatusLabel,
  formatTrueClientPromotionStatus,
  getImportSummary,
  getImportedRuntimeSectionId,
  normalizeParameterDataLookup,
  readImportedJavaVMParameters,
  readSourceReview,
  readTrueClientPromotionReview,
} from './scriptEditorImportModel'

function action(name: string, type = 'GET'): ScriptActionDefinition {
  return { name, type, target: `/${name.toLowerCase()}`, headers: {}, body: '' }
}

describe('script editor import model', () => {
  it('accepts source-review arrays and rejects unrelated preserved objects', () => {
    const review = { mappings: [] }
    expect(readSourceReview({ preserved: { source_review: review } })).toBe(review)
    expect(readSourceReview({ preserved: { source_review: { summary: 'not a review' } } })).toBeNull()
    expect(readSourceReview({ preserved: { source_review: [] } })).toBeNull()
    expect(readSourceReview(undefined)).toBeNull()
  })

  it('reads imported VM parameters from the normalized preserved java_vm_settings object', () => {
    expect(
      readImportedJavaVMParameters({
        preserved: { java_vm_settings: { additional_vm_parameters: ['-Xmx512m', '-Denv=perf'] } },
      }),
    ).toEqual(['-Xmx512m', '-Denv=perf'])
  })

  it('falls back to tokenizing the raw JavaVM section from runtime_files for older imports', () => {
    expect(
      readImportedJavaVMParameters({
        preserved: {
          runtime_files: {
            'default.cfg': { 'JavaVM:Options': { Java_VM_Params: '-Xmx256m -Dlabel=perf' } },
          },
        },
      }),
    ).toEqual(['-Xmx256m', '-Dlabel=perf'])
  })

  it('keeps quoted VM arguments intact in the legacy fallback tokenizer', () => {
    expect(
      readImportedJavaVMParameters({
        preserved: {
          runtime_files: {
            'default.cfg': { JavaVM: { VmParams: '-Xmx256m -Dlabel="hello world" -Dpath="/opt/app dir"' } },
          },
        },
      }),
    ).toEqual(['-Xmx256m', '-Dlabel=hello world', '-Dpath=/opt/app dir'])
  })

  it('returns an empty list when no Java VM parameters are present', () => {
    expect(readImportedJavaVMParameters(undefined)).toEqual([])
    expect(readImportedJavaVMParameters({ preserved: { runtime_files: { 'default.cfg': { HTTP: { KeepAlive: '1' } } } } })).toEqual([])
  })

  it('selects the most specific mapping for an action and supports name fallback', () => {
    const checkout = {
      ...action('Checkout'),
      metadata: { source_transaction: 'purchase' },
    }
    const review: SourceArchiveReview = {
      mappings: [
        { id: 'nested', source_file: 'Action.c', start_line: 1, target_path: 'actions[0].params.requests[0]', status: 'review' },
        { id: 'exact', source_file: 'Action.c', start_line: 1000, target_path: 'actions[0]', status: 'review' },
        { id: 'transaction', source_file: 'Action.c', start_line: 3, target_name: 'PURCHASE', status: 'review' },
      ],
    }

    expect(findSourceMappingForAction(review, checkout, 0)?.id).toBe('exact')
    expect(findSourceMappingForAction({ mappings: [review.mappings![2]] }, checkout, 0)?.id).toBe('transaction')
    expect(findSourceMappingForAction(review, checkout, -1)).toBeNull()
    expect(findSourceMappingForAction(review, undefined, 0)).toBeNull()
  })

  it('normalizes TrueClient promotion evidence with safe defaults and bounded counts', () => {
    const metadata: ScriptImportMetadata = {
      source: 'truclient',
      source_script_name: 'Checkout',
      preserved: {
        trueclient2_promotion_review: {
          status: ' replay_evidence_attached ',
          compatibility: {
            promotion_allowed: 'yes',
            selector_confidence_pct: 97.8,
            frame_window_blockers: -2,
          },
          gates: {
            replay_required: '1',
            review_required_actions: 2.9,
            replay_warnings: [' warning ', '', 42],
          },
          replay_evidence: {
            events: 4.7,
            failed: Number.NaN,
          },
          next_actions: [' Replay script ', ''],
        },
      },
    }

    const review = readTrueClientPromotionReview(metadata)
    expect(review).not.toBeNull()
    expect(review?.status).toBe('replay_evidence_attached')
    expect(review?.source).toBe('truclient')
    expect(review?.sourceScriptName).toBe('Checkout')
    expect(review?.compatibility).toMatchObject({
      promotionAllowed: true,
      selectorConfidencePct: 97,
      frameWindowBlockers: 0,
    })
    expect(review?.gates.reviewRequiredActions).toBe(2)
    expect(review?.gates.replayWarnings).toEqual(['warning'])
    expect(review?.replayEvidence).toMatchObject({ status: 'not_run', events: 4, failed: 0 })
    expect(review?.nextActions).toEqual(['Replay script'])
  })

  it('formats promotion statuses and singular/plural counts', () => {
    expect(formatTrueClientPromotionStatus('promotion_ready')).toBe('Promotion ready')
    expect(formatTrueClientPromotionStatus('custom_review_state')).toBe('Custom Review State')
    expect(formatPromotionCount(1, 'event')).toBe('1 event')
    expect(formatPromotionCount(2, 'person', 'people')).toBe('2 people')
  })

  it('builds object-descriptor rows only from reviewable imported steps', () => {
    const script: ScriptDefinition = {
      ...EMPTY_SCRIPT,
      protocol: 'browser',
      actions: [
        { ...action('Open', 'browser_navigate'), selector: '#current' },
        action('API'),
        { ...action('Click', 'browser_click'), selector: '.submit' },
      ],
    }
    const metadata: ScriptImportMetadata = {
      preserved: {
        trueclient_archive_inspection: {
          object_repositories: [{ id: 'repo-1' }],
          unsupported_fragments: [{ id: 'unsupported-1' }],
          steps: [
            {
              step_id: 'open',
              action_name: 'Open page',
              object_descriptor_id: 'object-1',
              status: 'ready',
              selector_candidates: [
                { kind: 'css', value: ' #next ', confidence: 0.9, reason: 'stable id' },
                { kind: 'css', value: '' },
              ],
              replay_diagnostics: [{ summary: 'Found during replay' }, { code: 'fallback' }, null],
            },
            { step_id: 'api', object_descriptor_id: 'object-2', selector_candidates: [] },
            { step_id: 'empty', selector_candidates: [{ value: '' }] },
          ],
        },
      },
    }

    const review = buildWeb2ObjectDescriptorReview(script, metadata)
    expect(review).toMatchObject({ repositoryCount: 1, candidateCount: 1, unsupportedCount: 1 })
    expect(review.rows).toHaveLength(2)
    expect(review.rows[0]).toMatchObject({
      label: 'Open page',
      descriptorId: 'object-1',
      actionIndex: 0,
      currentSelector: '#current',
      replayDiagnostics: ['Found during replay', 'fallback'],
      candidates: [{ kind: 'css', value: '#next', confidence: 0.9, reason: 'stable id' }],
    })
    expect(review.rows[1].actionIndex).toBeNull()
  })

  it('finds actions and imported locations with exact-first, case-insensitive fallback', () => {
    const actions = [action('Login'), action('Checkout')]
    expect(findActionIndexByName(actions, 'Checkout')).toBe(1)
    expect(findActionIndexByName(actions, ' login ')).toBe(0)
    expect(findActionIndexByName(actions, 'missing')).toBeNull()
    expect(findActionIndexByImportedLocation(actions, 'Request 2')).toBe(1)
    expect(findActionIndexByImportedLocation(actions, 'Step 0')).toBeNull()
    expect(findActionIndexByImportedLocation(actions, 'Request 3')).toBeNull()
    expect(formatImportedLocationLabel(actions, 'Request 2', 1)).toBe('Request 2: Checkout')
    expect(formatImportedLocationLabel(actions, '', null)).toBe('Unknown action')
  })

  it('finds parameter data sources across import naming conventions', () => {
    const dataSources = [
      { name: 'users', type: 'csv', strategy: 'sequential' },
      { name: 'Orders', type: 'csv', strategy: 'sequential' },
    ]
    const parameters = [
      { name: 'user', type: 'file', data_source: 'USERS' },
      { name: 'order', type: 'file', data_source: 'Orders' },
    ]

    expect(findDataSourceIndexByName(dataSources, ' orders ')).toBe(1)
    expect(findFirstParameterIndexByDataSource(parameters, 'users')).toBe(0)
    expect(findImportedParameterMappingDataSourceIndex(dataSources, { Table: 'C:\\Data Files\\Users.csv' })).toBe(0)
    expect(findImportedParameterMappingDataSourceIndex(dataSources, { ignored: 'users.csv' })).toBeNull()
    expect(normalizeParameterDataLookup(' USERS ')).toBe('users')
  })

  it.each([
    ['Think Time', 'think'],
    ['run logic', 'runlogic'],
    ['Web HTTP/HTML', 'web_runtime'],
    ['SOAP Settings', 'soap'],
    ['Java over HTTP', 'java_http'],
    ['Java VM', 'java_vuser'],
    ['Java Environment Settings', 'java_vuser'],
    ['database runtime', 'database'],
    ['Network Runtime', 'network'],
    ['unknown', null],
  ] as const)('maps imported runtime area %s to %s', (area, expected) => {
    expect(getImportedRuntimeSectionId(area)).toBe(expected)
  })

  it('normalizes the complete import summary and parameter-file mappings', () => {
    const metadata: ScriptImportMetadata = {
      preserved: {
        runtime_files: { 'default.cfg': {} },
        parameter_files: [' Users.prm ', '', 42],
        parameter_file_sections: {
          'Users.prm': {
            Login: { Table: 'users.csv', ColumnName: ' username ', Empty: '' },
          },
        },
        project_settings: { scheduler: {} },
        unsupported_functions: [' web_reg_save_param ', 3],
        correlation_suggestions: [{
          name: ' token ',
          source: 'body',
          expression: '$.token',
          found_at: 'Request 1',
          used_at: [' Request 2 ', ''],
        }],
        runtime_settings_parity: [{
          id: ' timeout ',
          area: ' HTTP ',
          concept: ' timeout ',
          status: ' partial ',
          runtime_files: [' default.cfg '],
        }],
        runtime_settings_status_counts: { partial: 1, ignored: '2' },
        runtime_settings_migration_readiness: {
          score_pct: 80,
          summary: ' Ready after review ',
          blocking_settings: [],
        },
      },
    }
    const summary = getImportSummary(metadata)

    expect(summary).toMatchObject({
      runtimeFiles: ['default.cfg'],
      parameterFiles: ['Users.prm'],
      projectSections: ['scheduler'],
      unsupportedFunctions: ['web_reg_save_param'],
      runtimeSettingStatusCounts: { partial: 1 },
      runtimeSettingReadiness: { score_pct: 80, summary: 'Ready after review' },
    })
    expect(summary.correlationSuggestions[0]).toMatchObject({ name: 'token', used_at: ['Request 2'] })
    expect(summary.runtimeSettingsParity[0]).toMatchObject({ id: 'timeout', area: 'HTTP', runtime_files: ['default.cfg'] })
    expect(summary.parameterFileSections[0]).toEqual({
      file: 'Users.prm',
      name: 'Login',
      values: { Table: 'users.csv', ColumnName: 'username' },
    })

    const script = { ...EMPTY_SCRIPT, data_sources: [{ name: 'users', type: 'csv', strategy: 'sequential' }] }
    expect(buildImportedParameterFileMappings(script, summary)).toEqual([{
      section: summary.parameterFileSections[0],
      sourceName: 'users',
    }])
  })

  it('builds stable correlation navigation targets for source and use locations', () => {
    const actions = [action('Login'), action('Checkout')]
    const targets = buildImportedCorrelationFocusTargets(actions, [{
      name: 'token',
      source: 'body',
      expression: '$.token',
      found_at: 'Request 1',
      used_at: ['Request 2', 'Request 9'],
    }])

    expect(targets[0]).toMatchObject({
      foundAtActionIndex: 0,
      foundAtLabel: 'Request 1: Login',
      usedAtLocations: [
        { location: 'Request 2', actionIndex: 1, label: 'Request 2: Checkout' },
        { location: 'Request 9', actionIndex: null, label: 'Request 9' },
      ],
      usedAtTargets: [{ location: 'Request 2', actionIndex: 1, label: 'Request 2: Checkout' }],
    })
  })

  it('summarizes imported draft changes without counting case-only named-list changes', () => {
    const baseline: ScriptDefinition = {
      ...EMPTY_SCRIPT,
      actions: [action('Login')],
      parameters: [{ name: 'User', type: 'list', values: ['a'] }],
      data_sources: [{ name: 'Users', type: 'csv', strategy: 'sequential' }],
    }
    const current: ScriptDefinition = {
      ...baseline,
      actions: [{ ...baseline.actions[0], target: '/v2/login' }],
      parameters: [{ name: 'user', type: 'list', values: ['b'] }],
      data_sources: [{ name: 'users', type: 'csv', strategy: 'random' }],
    }

    expect(buildImportedDraftDiffSummary(baseline, current)).toMatchObject({
      changed: true,
      actionChanges: 1,
      parameterChanges: 0,
      dataSourceChanges: 0,
    })
    expect(buildImportedDraftDiffSummary(null, current).changed).toBe(false)
  })

  it('refreshes and clears validation-owned runtime metadata without disturbing other import evidence', () => {
    const script: ScriptDefinition = {
      ...EMPTY_SCRIPT,
      metadata: { import: { source: 'archive', preserved: { source_review: { mappings: [] }, runtime_settings_parity: [{ stale: true }] } } },
    }
    const refreshed = applyValidationRuntimeSettingsMetadata(script, {
      runtime_settings_parity: [{ id: 'timeout', area: 'HTTP', concept: 'Timeout', status: 'executed', runtime_files: [] }],
      runtime_settings_status_counts: { executed: 1 },
      migration_readiness: { score_pct: 100, blocked: false, summary: 'Ready', blocking_statuses: [], blocking_settings: [] },
    } as never)

    expect(refreshed.metadata?.import?.preserved).toMatchObject({
      source_review: { mappings: [] },
      runtime_settings_status_counts: { executed: 1 },
      runtime_settings_migration_readiness: { score_pct: 100 },
    })
    expect(refreshed.metadata?.import?.preserved?.runtime_settings_parity).toHaveLength(1)

    const cleared = applyValidationRuntimeSettingsMetadata(refreshed, {} as never)
    expect(cleared.metadata?.import?.preserved).toEqual({ source_review: { mappings: [] } })
    expect(applyValidationRuntimeSettingsMetadata(EMPTY_SCRIPT, {} as never)).toBe(EMPTY_SCRIPT)
  })

  it('formats runtime statuses and preserved parameter mapping details', () => {
    expect(formatRuntimeSettingStatusLabel('compatibility_only')).toBe('Compatibility-only')
    expect(formatRuntimeSettingStatusLabel('custom_status')).toBe('custom status')
    expect(formatImportParameterMapping({ Type: 'Table', ColumnName: 'user', Table: 'users.csv' }))
      .toBe('Table • column user • table users.csv')
    expect(formatImportParameterMapping({})).toBe('No preserved mapping details')
  })
  it.each(['original_import', 'future_domain'])('does not infer current action or correlation navigation from %s paths', domain => {
    const currentActions = [action('Unrelated native action'), action('Another native action')]
    const source = { target_domain: domain, mappings: [{ id: 'old', source_file: 'capture.har', start_line: 1, target_path: 'actions[0]', target_name: 'Unrelated native action', status: 'converted' }] }
    expect(findSourceMappingForAction(source, currentActions[0], 0)).toBeNull()
    const targets = buildImportedCorrelationFocusTargets(currentActions, [{ name: 'value', source: 'body', expression: '$.result', found_at: 'Request 1', used_at: ['Request 2'] }], source)
    expect(targets[0]).toMatchObject({ foundAtActionIndex: null, foundAtLabel: 'Request 1', usedAtTargets: [], usedAtLocations: [{ location: 'Request 2', actionIndex: null, label: 'Request 2' }] })
  })

  it('normalizes legacy native-selected source navigation while leaving generic imports current', () => {
    const review = { mappings: [{ id: 'original', source_file: 'capture.har', target_path: 'actions[0]', target_name: 'Unrelated', status: 'converted' }] }
    const preserved = { source_review: review, salesforce_review: { native_preview: { correlations: [] } } }
    const native = readSourceReview({ source: 'salesforce', preserved })!
    expect(native.target_domain).toBe('original_import')
    expect(findSourceMappingForAction(native, action('Unrelated'), 0)).toBeNull()
    expect(buildImportedCorrelationFocusTargets([action('Unrelated')], [{ name: 'value', source: 'body', expression: '$.value', found_at: 'Request 1', used_at: [] }], native)[0].foundAtActionIndex).toBeNull()
    const generic = readSourceReview({ source: 'har', preserved })!
    expect(generic).toBe(review)
    expect(findSourceMappingForAction(generic, action('Unrelated'), 0)?.id).toBe('original')
    expect(preserved.source_review).toBe(review)
  })

})
