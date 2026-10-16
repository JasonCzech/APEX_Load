import { describe, expect, it } from 'vitest'

import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import { buildScriptPhaseActionRows } from './scriptEditorWorkbench'
import {
  buildOutlineModel,
  commonNamePrefix,
  dropNamePrefix,
  fitOutlineName,
  outlineStartsCollapsed,
  readableStepName,
  renameTransactionMarker,
  splitMeasureName,
  visibleOutlineRows,
  type OutlineGroup,
  type OutlineModel,
  type OutlineNode,
} from './scriptOutlineModel'

type Raw = Record<string, unknown>
const script = (actions: Raw[], init: Raw[] = []): ScriptDefinition => ({
  name: 'outline', protocol: 'http', actions: actions as unknown as ScriptActionDefinition[], init: init as unknown as ScriptActionDefinition[],
} as unknown as ScriptDefinition)
const modelOf = (actions: Raw[], options: { showMarkers?: boolean } = {}, init: Raw[] = []) => buildOutlineModel(buildScriptPhaseActionRows(script(actions, init)), options)

// Markers as the LoadRunner importer writes them: the name is the call's first argument.
const marker = (role: 'start' | 'end', name: string, sub = false) => ({
  name: `${role === 'start' ? 'Start' : 'End'}_${name}_transaction`,
  type: 'source_transaction',
  params: { function: `lr_${role}_${sub ? 'sub_' : ''}transaction`, args: [name, role === 'end' ? 'LR_AUTO' : name.split('||')[0]] },
})
const auraBody = (classname: string, method: string) => `message=${encodeURIComponent(JSON.stringify({ actions: [{ id: '1;a', descriptor: 'aura://ApexActionController/ACTION$execute', params: { namespace: '', classname, method, params: {} } }] }))}&aura.token=x`
const request = (name: string, body?: string) => ({ name, type: 'POST', method: 'POST', target: '/aura', ...(body ? { body } : {}) })

/** One transaction, each request measured by its own sub-transaction (the iPad script's shape). */
function measuredTransaction(transaction: string, calls: Array<[string, string?]>, first = 0) {
  return [
    marker('start', transaction),
    ...calls.flatMap(([sub, body], index) => [
      marker('start', `${transaction}||${sub}`, true),
      request(`${transaction}_aura_${first + index}`, body),
      marker('end', `${transaction}||${sub}`, true),
    ]),
    marker('end', transaction),
  ]
}

const shape = (nodes: OutlineNode[]): unknown[] => nodes.map(node => node.kind === 'step'
  ? `${node.label}${node.occurrence ? ` #${node.occurrence}` : ''}${node.measuredAs ? ' (measured)' : ''}`
  : node.kind === 'marker'
    ? `marker ${node.role} ${node.name}`
    : { [node.kind]: node.label, steps: node.stepCount, children: shape(node.children) })
const actions = (model: OutlineModel) => model.phases.find(phase => phase.phase === 'action')!.children

describe('readableStepName', () => {
  it('reads Aura descriptors as Class.method, as Salesforce names Apex methods', () => {
    expect(readableStepName('ui.flexipage.components.page.FlexipageControllerV2/ACTION$getPage')).toBe('FlexipageControllerV2.getPage')
    expect(readableStepName('aura_ui.flexipage.components.page.FlexipageControllerV2_ui.flexipage.components.page.FlexipageControllerV2/ACTION$getPage')).toBe('FlexipageControllerV2.getPage')
    expect(readableStepName('aura_RecordUi_RecordUi/ACTION$getRecordWithFields')).toBe('RecordUi.getRecordWithFields')
    expect(readableStepName('ACTION$getStoreLocation')).toBe('getStoreLocation')
  })

  it('names an ApexActionController call by the class its request names, or by the method alone', () => {
    const withBody = request('T03_aura_1', auraBody('DCIController', 'getBaggingOrderCount')) as unknown as ScriptActionDefinition
    expect(readableStepName('ACTION$execute_getBaggingOrderCount', withBody)).toBe('DCIController.getBaggingOrderCount')
    expect(readableStepName('ACTION$execute_getBaggingOrderCount')).toBe('getBaggingOrderCount')
    // A request for another method does not lend its class.
    expect(readableStepName('ACTION$execute_getStoreId', withBody)).toBe('getStoreId')
  })

  it('leaves other names alone', () => {
    expect(readableStepName('web_custom_request_checkout')).toBe('web_custom_request_checkout')
    expect(readableStepName('ACTION$')).toBe('ACTION$')
  })
})

describe('name prefixes', () => {
  it('drops the transaction name a step repeats, at a separator only', () => {
    expect(dropNamePrefix('iPad_T02_Login_aura_2', 'iPad_T02_Login')).toBe('aura_2')
    expect(dropNamePrefix('iPad_T02_LoginPage', 'iPad_T02_Login')).toBe('iPad_T02_LoginPage')
    expect(dropNamePrefix('iPad_T02_Login', 'iPad_T02_Login')).toBe('iPad_T02_Login')
  })

  it('finds the prefix sibling names share, cut back to a word', () => {
    expect(commonNamePrefix(['iPadTest_LrgeBANs_T01_Launch', 'iPadTest_LrgeBANs_T02_Login'])).toBe('iPadTest_LrgeBANs')
    expect(commonNamePrefix(['Checkout', 'Check'])).toBe('')
    expect(commonNamePrefix(['Only'])).toBe('')
  })
})

describe('fitOutlineName', () => {
  const measure = (text: string) => text.length * 10
  it('keeps a name that fits and cuts a long one at its end on a word boundary', () => {
    expect(fitOutlineName('DCIController.getBaggingOrderCount', 400, measure)).toBe('DCIController.getBaggingOrderCount')
    expect(fitOutlineName('DCIController.getBaggingOrderCount', 250, measure)).toBe('DCIController.getBagging…')
    expect(fitOutlineName('DCIController.getBaggingOrderCount', 150, measure)).toBe('DCIController…')
    expect(fitOutlineName('T02_Login_aura_15', 100, measure)).toBe('T02_Login…')
  })

  it('shows an Aura step by its method alone before cutting it', () => {
    expect(fitOutlineName('DCIController.getBaggingOrderCount', 250, measure, 'getBaggingOrderCount')).toBe('getBaggingOrderCount')
    expect(fitOutlineName('DCIController.getBaggingOrderCount', 150, measure, 'getBaggingOrderCount')).toBe('getBagging…')
  })

  it('never cuts inside a word, even when only an ellipsis fits', () => {
    expect(fitOutlineName('averyveryverylongnamewithoutbreaks', 100, measure)).toBe('…')
    for (const width of [60, 90, 120, 180, 260]) {
      const fitted = fitOutlineName('HostConfigController.getConfigData', width, measure)
      const head = fitted.replace(/…$/, '')
      expect(['', 'Host', 'HostConfig', 'HostConfigController', 'HostConfigController.get', 'HostConfigController.getConfig', 'HostConfigController.getConfigData']).toContain(head)
    }
  })
})

describe('buildOutlineModel', () => {
  it('folds a sub-transaction around one request into that request, named by the call', () => {
    const model = modelOf([
      { name: 'home', type: 'GET' },
      ...measuredTransaction('iPad_T03_Search', [
        ['ACTION$execute_getBaggingOrderCount', auraBody('DCIController', 'getBaggingOrderCount')],
        ['aura_RecordUi_RecordUi/ACTION$getRecordWithFields'],
      ]),
    ])
    expect(shape(actions(model))).toEqual([
      'home',
      { transaction: 'iPad_T03_Search', steps: 2, children: ['DCIController.getBaggingOrderCount (measured)', 'RecordUi.getRecordWithFields (measured)'] },
    ])
    const folded = (actions(model)[1] as OutlineGroup).children[0]
    expect(folded.kind === 'step' && folded.title).toBe('ACTION$execute_getBaggingOrderCount — iPad_T03_Search_aura_0')
    // Every marker of a folded step leads to the step's row.
    expect(model.rowKeyByAction.get('action:2')).toBe('action:3')
    expect(model.rowKeyByAction.get('action:4')).toBe('action:3')
    expect(model.measureByAction.get('action:3')?.name).toBe('iPad_T03_Search||ACTION$execute_getBaggingOrderCount')
    expect(model.ancestorsByKey.get('action:3')).toEqual(['phase:action', 'transaction:action:iPad_T03_Search#1'])
    expect(model.stepCount).toBe(3)
  })

  it('nests a sub-transaction around several steps, and keeps an empty one visible', () => {
    const model = modelOf([
      marker('start', 'T01'),
      marker('start', 'T01||checkout', true),
      { name: 'T01_cart', type: 'GET' },
      { name: 'T01_pay', type: 'POST' },
      marker('end', 'T01||checkout', true),
      marker('start', 'T01||nothing', true),
      marker('end', 'T01||nothing', true),
      marker('end', 'T01'),
    ])
    expect(shape(actions(model))).toEqual([{ transaction: 'T01', steps: 2, children: [
      { subtransaction: 'checkout', steps: 2, children: ['cart', 'pay'] },
      { subtransaction: 'nothing', steps: 0, children: [] },
    ] }])
  })

  it('shows a marker without a partner as its own row', () => {
    expect(shape(actions(modelOf([{ name: 'a', type: 'GET' }, marker('end', 'Orphan')])))).toEqual(['a', 'marker end Orphan'])
  })

  it('draws every marker as a row when markers are shown', () => {
    const model = modelOf(measuredTransaction('T01', [['ACTION$getStoreId']]), { showMarkers: true })
    expect(shape(actions(model))).toEqual([{ transaction: 'T01', steps: 1, children: [
      'marker start T01||ACTION$getStoreId', 'aura_0', 'marker end T01||ACTION$getStoreId', 'marker end T01',
    ] }])
    expect(model.measureByAction.size).toBe(0)
  })

  it('groups a run of steps tagged with one transaction, and separates different ones', () => {
    const tagged = (name: string, transaction: string) => ({ name, type: 'GET', metadata: { source_transaction: transaction } })
    expect(shape(actions(modelOf([tagged('a', 'Browse'), tagged('b', 'Browse'), tagged('c', 'Buy'), { name: 'd', type: 'GET' }])))).toEqual([
      { transaction: 'Browse', steps: 2, children: ['a', 'b'] },
      { transaction: 'Buy', steps: 1, children: ['c'] },
      'd',
    ])
  })

  it('tells sibling transactions apart by what follows their shared prefix', () => {
    const model = modelOf([
      ...measuredTransaction('iPadTest_LrgeBANs_T01_Launch', [['ACTION$a']]),
      ...measuredTransaction('iPadTest_LrgeBANs_T02_Login', [['ACTION$b']], 1),
    ])
    expect(actions(model).map(node => node.kind === 'transaction' && node.label)).toEqual(['T01_Launch', 'T02_Login'])
  })

  it('tells transactions apart across phases by what follows their shared prefix', () => {
    const model = modelOf(measuredTransaction('iPadTest_LrgeBANs_T03_Search', [['ACTION$a']]), {}, measuredTransaction('iPadTest_LrgeBANs_T01_Launch', [['ACTION$b']], 1))
    expect(model.phases.map(phase => phase.children.map(node => node.kind === 'transaction' && node.label))).toEqual([['T01_Launch'], ['T03_Search'], []])
  })

  it('keeps a shorter name for an Aura step that names its class', () => {
    const model = modelOf(measuredTransaction('T03', [['ACTION$execute_getBaggingOrderCount', auraBody('DCIController', 'getBaggingOrderCount')], ['ACTION$getStoreId']]))
    const steps = (actions(model)[0] as OutlineGroup).children
    expect(steps.map(step => step.kind === 'step' && [step.label, step.short])).toEqual([['DCIController.getBaggingOrderCount', 'getBaggingOrderCount'], ['getStoreId', undefined]])
  })

  it('numbers a name used more than once among its siblings', () => {
    const model = modelOf(measuredTransaction('T03', [
      ['ACTION$execute_getBaggingOrderCount'], ['ACTION$execute_getStoreId'], ['ACTION$execute_getBaggingOrderCount'], ['ACTION$execute_getBaggingOrderCount'],
    ]))
    expect(shape((actions(model)[0] as OutlineGroup).children)).toEqual([
      'getBaggingOrderCount #1 (measured)', 'getStoreId (measured)', 'getBaggingOrderCount #2 (measured)', 'getBaggingOrderCount #3 (measured)',
    ])
  })
})

describe('visibleOutlineRows', () => {
  const view = (model: OutlineModel, expanded: (key: string) => boolean, query = '') => visibleOutlineRows(model, { isExpanded: expanded, expandedContainers: new Set(), query })
  // The iPad script's shape: 21 transactions, 399 one-request sub-transactions and 24 other requests.
  const large = () => {
    const body: Raw[] = []
    for (let index = 0; index < 21; index += 1) {
      const name = `iPadTest_LrgeBANs_ViewServProm_T${String(index + 1).padStart(2, '0')}_Step`
      const calls = Array.from({ length: 19 }, (_, call): [string, string] => [`ACTION$execute_method${call % 7}`, auraBody(`Controller${call % 3}`, `method${call % 7}`)])
      const block = measuredTransaction(name, calls, index * 19)
      // Each transaction also has a request or two outside any sub-transaction.
      block.splice(block.length - 1, 0, { name: `${name}_aura_x${index}`, type: 'POST' } as never, ...(index < 3 ? [{ name: `${name}_aura_y${index}`, type: 'POST' } as never] : []))
      body.push(...block)
    }
    return modelOf(body)
  }

  it('shows the iPad-shaped script in under 450 rows expanded and its transactions collapsed', () => {
    const model = large()
    expect(model.stepCount).toBe(399 + 24)
    expect(outlineStartsCollapsed(model)).toBe(true)
    const expanded = view(model, () => true)
    expect(expanded.length).toBeLessThanOrEqual(450)
    expect(expanded.filter(row => row.measuredAs)).toHaveLength(399)
    const collapsed = view(model, key => key.startsWith('phase:'))
    expect(collapsed.map(row => row.kind)).toEqual(['phase', ...Array(21).fill('transaction')])
    expect(collapsed[1]).toMatchObject({ label: 'T01_Step', badge: '21 steps', level: 2, expandable: true, expanded: false, posInSet: 1, setSize: 21 })
  })

  it('opens the groups on the way to a search match and keeps their names in view', () => {
    const model = modelOf([
      { name: 'home', type: 'GET' },
      ...measuredTransaction('Checkout', [['ACTION$execute_getStoreId'], ['ACTION$execute_pay']]),
    ])
    const rows = view(model, key => key.startsWith('phase:'), 'storeid')
    expect(rows.map(row => `${row.level}:${row.label}`)).toEqual(['1:Actions', '2:Checkout', '3:getStoreId'])
    expect(rows[1].expanded).toBe(true)
    // A group whose own name matches shows as it is.
    expect(view(model, key => key.startsWith('phase:'), 'checkout').map(row => row.label)).toEqual(['Actions', 'Checkout'])
    expect(view(model, () => true, 'nothing like this')).toEqual([])
  })

  it('opens a sequence into its requests, one level deeper', () => {
    const model = modelOf([{ name: 'Batch', type: 'http_sequence', params: { requests: [{ name: 'one', method: 'GET', url: '/1' }, { name: 'two', method: 'GET', url: '/2' }] } }])
    const closed = visibleOutlineRows(model, { isExpanded: () => true, expandedContainers: new Set(), query: '' })
    expect(closed.map(row => [row.label, row.expandable, row.expanded])).toEqual([['Actions', true, true], ['Batch', true, false]])
    const open = visibleOutlineRows(model, { isExpanded: () => true, expandedContainers: new Set(['action:0']), query: '' })
    expect(open.slice(2).map(row => ({ label: row.label, level: row.level, parent: row.parentKey, kind: row.kind }))).toEqual([
      { label: 'one', level: 3, parent: 'action:0', kind: 'nested' },
      { label: 'two', level: 3, parent: 'action:0', kind: 'nested' },
    ])
  })

  it('opens the blocks on the way to a request or step that a search matches', () => {
    const model = modelOf([
      { name: 'Login', type: 'GET', target: '/login' },
      { name: 'Retry', type: 'control_loop', control: { count: 2, steps: [
        { name: 'Poll', type: 'GET', target: '/poll' },
        { name: 'Pay', type: 'http_sequence', params: { requests: [{ name: 'Authorize', method: 'POST', target: '/authorize' }, { name: 'Capture', method: 'POST', target: '/capture' }] } },
      ] } },
    ])
    const found = visibleOutlineRows(model, { isExpanded: () => false, expandedContainers: new Set(), query: 'capture' })
    expect(found.map(row => [row.label, row.level, row.expanded])).toEqual([
      ['Actions', 1, true], ['Retry', 2, true], ['Poll', 3, false], ['Pay', 3, true], ['Authorize', 4, false], ['Capture', 4, false],
    ])
    // Nothing inside matches: the block stays as the reader left it.
    expect(visibleOutlineRows(model, { isExpanded: () => true, expandedContainers: new Set(), query: 'login' }).map(row => row.label)).toEqual(['Actions', 'Login'])
  })

  it('keeps an empty Init out and shows Actions even when empty', () => {
    expect(visibleOutlineRows(modelOf([]), { isExpanded: () => true, expandedContainers: new Set(), query: '' }).map(row => row.label)).toEqual(['Actions'])
    expect(visibleOutlineRows(modelOf([], {}, [{ name: 'login', type: 'POST' }]), { isExpanded: () => true, expandedContainers: new Set(), query: '' }).map(row => row.label)).toEqual(['Init', 'login', 'Actions'])
  })
})

describe('renameTransactionMarker', () => {
  it('renames a marker everywhere the import wrote its name, and leaves the rest', () => {
    const start = {
      name: 'Start_T02||ACTION$getUser_transaction_2', type: 'source_transaction',
      params: { function: 'lr_start_sub_transaction', args: ['T02||ACTION$getUser', 'T02'], call_args: [{ kind: 'string', raw: '"T02||ACTION$getUser"', value: 'T02||ACTION$getUser' }, { kind: 'string', raw: '"T02"', value: 'T02' }], line: 12 },
    } as unknown as ScriptActionDefinition
    const renamed = renameTransactionMarker(start, 'T02||ACTION$getUser', 'T02||Load user') as unknown as { name: string; params: Record<string, unknown> }
    expect(renamed.name).toBe('Start_T02||Load user_transaction_2')
    expect(renamed.params.args).toEqual(['T02||Load user', 'T02'])
    expect(renamed.params.call_args).toEqual([{ kind: 'string', raw: '"T02||Load user"', value: 'T02||Load user' }, { kind: 'string', raw: '"T02"', value: 'T02' }])
    expect(renamed.params.line).toBe(12)
    expect((start as unknown as { params: { args: string[] } }).params.args[0]).toBe('T02||ACTION$getUser')
  })

  it('splits a sub-transaction name into its parent and its own name', () => {
    expect(splitMeasureName('T02||HostConfigController/ACTION$getConfigData')).toEqual({ parent: 'T02', child: 'HostConfigController/ACTION$getConfigData' })
    expect(splitMeasureName('Alone')).toEqual({ parent: '', child: 'Alone' })
  })
})
