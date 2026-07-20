import { describe, expect, it } from 'vitest'
import type { ScriptDefinition, ScriptDataSource } from '../types/scripts'
import { EMPTY_SCRIPT } from './scriptEditorUtils'
import {
  buildMergedParameterSourceScript,
  dataSourceRowMatchesFilter,
  formatDataSourceColumnLabel,
  formatDataSourceDelimiterLabel,
  getDataSourceDelimiterChoice,
  getDataSourceDelimiterValue,
  getParameterDataColumnIndexes,
  getSameLineParameterOptions,
  mergeFocusedParameterData,
  stringifyFocusedParameterData,
  summarizeParameterSourceMerge,
} from './scriptEditorParameterDataModel'

function source(overrides: Partial<ScriptDataSource> = {}): ScriptDataSource {
  return {
    name: 'users',
    type: 'csv',
    strategy: 'sequential',
    delimiter: 'comma',
    first_row_header: true,
    inline_data: 'username,password\nalice,old\nbob,secret',
    ...overrides,
  }
}

describe('script editor parameter data model', () => {
  it('normalizes delimiter controls while retaining auto-detection labels', () => {
    const detectedTab = source({ delimiter: 'auto', inline_data: 'username\tpassword\nalice\tsecret' })

    expect(getDataSourceDelimiterChoice(detectedTab)).toBe('auto')
    expect(formatDataSourceDelimiterLabel(detectedTab)).toBe('Auto: Tab')
    expect(getDataSourceDelimiterValue('tab')).toBe('tab')
    expect(getDataSourceDelimiterValue('comma')).toBe('comma')
    expect(getDataSourceDelimiterValue('auto')).toBe('auto')
  })

  it('matches filtered rows and supplies stable labels for unnamed columns', () => {
    expect(dataSourceRowMatchesFilter(['Alice', 'Admin'], 'ali', 'all')).toBe(true)
    expect(dataSourceRowMatchesFilter(['Alice', 'Admin'], 'admin', 1)).toBe(true)
    expect(dataSourceRowMatchesFilter(['Alice', 'Admin'], 'admin', 0)).toBe(false)
    expect(dataSourceRowMatchesFilter(['Alice', 'Admin'], '   ', 0)).toBe(true)
    expect(formatDataSourceColumnLabel('', 2)).toBe('Column 3')
  })

  it('isolates and merges focused raw-column edits without changing sibling columns', () => {
    const dataSource = source({ inline_data: 'username,password,role\nalice,old,admin\nbob,secret,viewer' })
    const parsed = {
      columns: ['username', 'password', 'role'],
      rows: [['alice', 'old', 'admin'], ['bob', 'secret', 'viewer']],
    }
    const focusedColumns = getParameterDataColumnIndexes({ name: 'credential', type: 'file', column: 'PASSWORD' }, parsed.columns)

    expect(focusedColumns).toEqual([1])
    expect(stringifyFocusedParameterData(dataSource, parsed, focusedColumns)).toBe('password\nold\nsecret')
    expect(mergeFocusedParameterData(dataSource, parsed, focusedColumns, 'credential\nnew\nchanged\nextra')).toBe(
      'username,credential,role\nalice,new,admin\nbob,changed,viewer\n,extra,',
    )
  })

  it('offers same-row synchronization only for named data-backed parameters on the same source', () => {
    const parameters = [
      { name: 'username', type: 'file', data_source: 'Users', column: 'username' },
      { name: 'password', type: 'list', data_source: ' users ', column: 'password' },
      { name: 'generated', type: 'uuid', data_source: 'Users' },
      { name: '', type: 'file', data_source: 'Users' },
      { name: 'account', type: 'file', data_source: 'accounts' },
    ]

    expect(getSameLineParameterOptions(parameters, 0)).toEqual([
      { label: 'password (password)', value: 'password' },
    ])
    expect(getSameLineParameterOptions(parameters, 2)).toEqual([])
  })

  it('merges eligible parameter sources and rewires only their mapped parameters', () => {
    const script: ScriptDefinition = {
      ...EMPTY_SCRIPT,
      data_sources: [
        source({ name: 'users', inline_data: 'username\nalice\nbob' }),
        source({ name: 'passwords', path: 'passwords.csv', inline_data: 'password\nsecret' }),
        source({ name: 'accounts', type: 'json', inline_data: '' }),
      ],
      parameters: [
        { name: 'username', type: 'file', data_source: 'users', column: 'username' },
        { name: 'password', type: 'file', data_source: 'passwords', column: 'password' },
        { name: 'account', type: 'file', data_source: 'accounts', column: 'id' },
      ],
    }

    expect(summarizeParameterSourceMerge(script)).toEqual({ canMerge: true, parameterCount: 2, sourceCount: 2 })

    const result = buildMergedParameterSourceScript(script)
    expect(result).toMatchObject({
      mergedSourceIndex: 1,
      mergedSourceName: 'merged_parameters',
      parameterCount: 2,
      rowCount: 2,
      sourceCount: 2,
    })
    expect(result?.script.data_sources?.map(item => item.name)).toEqual(['accounts', 'merged_parameters'])
    expect(result?.script.data_sources?.[1].inline_data).toBe('username,password\nalice,secret\nbob,')
    expect(result?.script.parameters).toEqual([
      expect.objectContaining({ name: 'username', data_source: 'merged_parameters', column: 'username' }),
      expect.objectContaining({ name: 'password', data_source: 'merged_parameters', column: 'password' }),
      expect.objectContaining({ name: 'account', data_source: 'accounts', column: 'id' }),
    ])
  })

  it('declines source merging until at least two eligible files are represented', () => {
    const script: ScriptDefinition = {
      ...EMPTY_SCRIPT,
      data_sources: [source()],
      parameters: [{ name: 'username', type: 'file', data_source: 'users', column: 'username' }],
    }

    expect(summarizeParameterSourceMerge(script)).toEqual({ canMerge: false, parameterCount: 1, sourceCount: 1 })
    expect(buildMergedParameterSourceScript(script)).toBeNull()
  })
})
