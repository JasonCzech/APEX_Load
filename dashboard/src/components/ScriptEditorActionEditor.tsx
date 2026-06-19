import { useState, useEffect, useCallback, type MouseEvent as ReactMouseEvent } from 'react'
import type { ScriptActionDefinition, ScriptAssertionDefinition, ScriptParameterDefinition, ScriptValidationResponse } from '../types/scripts'
import type { ActionEditorParameterDraft, ActionEditorSelectionState } from './ScriptEditor'
import { sanitizeVarName } from './scriptEditorUtils'
import { asString, httpSequenceRequests, isControlAction, isReviewGatedJavaScriptAction, actionRequiresReview, actionSelectorFallbackCount } from './scriptActionHelpers'
import { appendActionStep, moveActionStep, removeActionStep, updateActionStepField, actionTransactionName, setActionTransactionName } from './scriptEditorWorkbench'
import { dslActionUsesSelector, dslActionUsesValue } from './dslActionCatalog'
import { BrowserActionFields, DatabaseActionFields, GrpcActionFields, HttpActionFields, JmsActionFields, NetworkActionFields, WebSocketActionFields } from './scriptActionFields'
import { type AuthoringIssue } from './scriptAuthoringAssist'

type Action = ScriptActionDefinition
type Assertion = ScriptAssertionDefinition
type ParameterConfig = ScriptParameterDefinition
type ValidationEvidence = Pick<ScriptValidationResponse, 'data_source_fit' | 'ai_guardrails' | 'authoring_health'>

export function ActionEditor({ action, index, parameters, onUpdate, onAddExtractor, onUpdateExtractor, onRemoveExtractor, onAddAssertion, onUpdateAssertion, onRemoveAssertion, onAddHeader, onUpdateHeader, onRemoveHeader, onAddMetadata, onUpdateMetadata, onRemoveMetadata, onBeginParameterEdit, methodColor, protocol, isBrowserScript, isJmsScript, isSoapScript, issues }: {
  action: Action; index: number
  parameters: ParameterConfig[]
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
  onAddExtractor: (i: number) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onAddAssertion: (i: number) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onAddHeader: (i: number) => void
  onUpdateHeader: (ai: number, ok: string, nk: string, v: string) => void
  onRemoveHeader: (ai: number, k: string) => void
  onAddMetadata: (i: number) => void
  onUpdateMetadata: (ai: number, ok: string, nk: string, v: string) => void
  onRemoveMetadata: (ai: number, k: string) => void
  onBeginParameterEdit: (draft: ActionEditorParameterDraft) => void
  methodColor: (m: string) => string
  protocol: string
  isBrowserScript: boolean
  isJmsScript: boolean
  isSoapScript: boolean
  issues: AuthoringIssue[]
}) {
  const isWebSocketScript = protocol === 'websocket'
  const isGrpcScript = protocol === 'grpc'
  const isDatabaseScript = protocol === 'database'
  const isNetworkScript = protocol === 'network'
  const isHTTPSequence = action.type === 'http_sequence'
  const { selectionState, openSelectionMenu, openParameterEditor } = useActionParameterMenu({
    index,
    onBeginParameterEdit,
    parameters,
  })

  return (
    <div className="action-editor-panel">
      <ActionEditorHeader action={action} issues={issues} methodColor={methodColor} />

      {isHTTPSequence ? (
        <HTTPSequenceFields action={action} index={index} onUpdate={onUpdate} />
      ) : (
        <>
          <ActionCoreFields
            action={action}
            index={index}
            protocol={protocol}
            isBrowserScript={isBrowserScript}
            isJmsScript={isJmsScript}
            onUpdate={onUpdate}
          />

          {isBrowserScript && action.type.startsWith('browser_') && (
            <BrowserActionFields action={action} index={index} onUpdate={onUpdate} />
          )}

          {isControlAction(action) && (
            <ControlFlowFields action={action} index={index} onUpdate={onUpdate} />
          )}

          {isWebSocketScript ? (
            <WebSocketActionFields
              action={action}
              index={index}
              onAddAssertion={onAddAssertion}
              onAddExtractor={onAddExtractor}
              onAddHeader={onAddHeader}
              onRemoveAssertion={onRemoveAssertion}
              onRemoveExtractor={onRemoveExtractor}
              onRemoveHeader={onRemoveHeader}
              onSelectionMenu={openSelectionMenu}
              onUpdate={onUpdate}
              onUpdateAssertion={onUpdateAssertion}
              onUpdateExtractor={onUpdateExtractor}
              onUpdateHeader={onUpdateHeader}
            />
          ) : isGrpcScript ? (
            <GrpcActionFields
              action={action}
              index={index}
              onAddAssertion={onAddAssertion}
              onAddExtractor={onAddExtractor}
              onAddHeader={onAddHeader}
              onRemoveAssertion={onRemoveAssertion}
              onRemoveExtractor={onRemoveExtractor}
              onRemoveHeader={onRemoveHeader}
              onSelectionMenu={openSelectionMenu}
              onUpdate={onUpdate}
              onUpdateAssertion={onUpdateAssertion}
              onUpdateExtractor={onUpdateExtractor}
              onUpdateHeader={onUpdateHeader}
            />
          ) : isDatabaseScript ? (
            <DatabaseActionFields
              action={action}
              index={index}
              onAddAssertion={onAddAssertion}
              onAddExtractor={onAddExtractor}
              onRemoveAssertion={onRemoveAssertion}
              onRemoveExtractor={onRemoveExtractor}
              onSelectionMenu={openSelectionMenu}
              onUpdate={onUpdate}
              onUpdateAssertion={onUpdateAssertion}
              onUpdateExtractor={onUpdateExtractor}
            />
          ) : isNetworkScript ? (
            <NetworkActionFields
              action={action}
              index={index}
              onAddAssertion={onAddAssertion}
              onAddExtractor={onAddExtractor}
              onRemoveAssertion={onRemoveAssertion}
              onRemoveExtractor={onRemoveExtractor}
              onSelectionMenu={openSelectionMenu}
              onUpdate={onUpdate}
              onUpdateAssertion={onUpdateAssertion}
              onUpdateExtractor={onUpdateExtractor}
            />
          ) : isJmsScript ? (
            <JmsActionFields
              action={action}
              index={index}
              onAddHeader={onAddHeader}
              onAddMetadata={onAddMetadata}
              onAddAssertion={onAddAssertion}
              onAddExtractor={onAddExtractor}
              onRemoveAssertion={onRemoveAssertion}
              onRemoveExtractor={onRemoveExtractor}
              onRemoveHeader={onRemoveHeader}
              onRemoveMetadata={onRemoveMetadata}
              onUpdate={onUpdate}
              onUpdateAssertion={onUpdateAssertion}
              onUpdateExtractor={onUpdateExtractor}
              onUpdateHeader={onUpdateHeader}
              onUpdateMetadata={onUpdateMetadata}
              onSelectionMenu={openSelectionMenu}
            />
          ) : (
            <HttpActionFields
              action={action}
              index={index}
              isBrowserScript={isBrowserScript}
              isSoapScript={isSoapScript}
              onAddAssertion={onAddAssertion}
              onAddExtractor={onAddExtractor}
              onAddHeader={onAddHeader}
              onRemoveAssertion={onRemoveAssertion}
              onRemoveExtractor={onRemoveExtractor}
              onRemoveHeader={onRemoveHeader}
              onUpdate={onUpdate}
              onUpdateAssertion={onUpdateAssertion}
              onUpdateExtractor={onUpdateExtractor}
              onUpdateHeader={onUpdateHeader}
              onSelectionMenu={openSelectionMenu}
            />
          )}
        </>
      )}

      <ActionThinkTimeToggle action={action} index={index} onUpdate={onUpdate} />

      <ActionParameterMenu selectionState={selectionState} onOpenParameterEditor={openParameterEditor} />
    </div>
  )
}

const BROWSER_ACTION_TYPES = [
  'browser_navigate',
  'browser_click',
  'browser_fill',
  'browser_select',
  'browser_wait',
  'browser_screenshot',
  'browser_dblclick',
  'browser_hover',
  'browser_drag_and_drop',
  'browser_set_files',
  'browser_press_key',
  'browser_evaluate_js',
  'browser_switch_frame',
  'browser_switch_window',
  'browser_tap',
  'browser_long_press',
  'browser_swipe',
  'control_if',
  'control_loop',
]
const JMS_ACTION_TYPES = ['jms_publish', 'jms_consume', 'jms_request_reply', 'jms_ack', 'jms_commit', 'jms_rollback']
const WEBSOCKET_ACTION_TYPES = ['ws_connect', 'ws_send', 'ws_receive', 'ws_ping', 'ws_close']
const GRPC_ACTION_TYPES = ['grpc_unary', 'grpc_server_stream', 'grpc_client_stream', 'grpc_bidi_stream']
const DATABASE_ACTION_TYPES = ['db_health', 'db_begin', 'db_query', 'db_exec', 'db_procedure', 'db_commit', 'db_rollback']
const NETWORK_ACTION_TYPES = ['network_health', 'dns_query', 'tcp_request', 'udp_request', 'ftp_command', 'smtp_send', 'pop3_command', 'imap_command', 'ldap_bind', 'ldap_search']
const HTTP_ACTION_TYPES = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS', 'http_oauth2_token']

interface HTTPSequenceFieldsProps {
  action: Action
  index: number
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
}

function HTTPSequenceFields({ action, index, onUpdate }: HTTPSequenceFieldsProps) {
  const requests = httpSequenceRequests(action)
  const sourceTransaction = asString(action.metadata?.source_transaction) || asString(action.metadata?.source_transaction)
  const subtransactions = requests
    .map(request => asString(request.metadata?.source_subtransaction) || asString(request.metadata?.source_subtransaction))
    .filter(Boolean)
  const uniqueSubtransactions = [...new Set(subtransactions)]
  const sequenceExtractors = requests.flatMap((request, requestIndex) =>
    (request.extractors || []).map((extractor, extractorIndex) => ({ extractor, requestName: request.name, key: `${requestIndex}-${extractorIndex}` })),
  )
  return (
    <>
      <div className="form-grid">
        <label><span>Name</span><input value={action.name} onChange={e => onUpdate(index, 'name', e.target.value)} /></label>
        <label><span>Action Type</span><input value="HTTP sequence" readOnly /></label>
        <label className="full-width"><span>Source Transaction</span><input value={sourceTransaction || action.name || ''} readOnly /></label>
      </div>
      <div className="sub-section http-sequence-section">
        <div className="sub-header">
          <h4>Grouped Requests</h4>
          <span className="sequence-summary">{requests.length} request{requests.length === 1 ? '' : 's'}{uniqueSubtransactions.length > 0 ? ` • ${uniqueSubtransactions.length} subtransaction${uniqueSubtransactions.length === 1 ? '' : 's'}` : ''}</span>
        </div>
        <div className="http-sequence-table-wrap">
          <table className="http-sequence-table">
            <thead>
              <tr>
                <th>#</th>
                <th>Method</th>
                <th>Name</th>
                <th>Target</th>
                <th>Signals</th>
              </tr>
            </thead>
            <tbody>
              {requests.map((request, requestIndex) => {
                const subtransaction = asString(request.metadata?.source_subtransaction) || asString(request.metadata?.source_subtransaction)
                const signalParts = [
                  request.body ? 'Body' : '',
                  request.headers && Object.keys(request.headers).length > 0 ? `${Object.keys(request.headers).length} headers` : '',
                  request.extractors?.length ? `${request.extractors.length} extractors` : '',
                  request.assertions?.length ? `${request.assertions.length} checks` : '',
                ].filter(Boolean)
                return (
                  <tr key={`${request.name}-${requestIndex}`}>
                    <td>{requestIndex + 1}</td>
                    <td><span className="sequence-method">{request.method || request.type}</span></td>
                    <td>
                      <strong>{request.name}</strong>
                      {subtransaction && <small>{subtransaction}</small>}
                    </td>
                    <td className="sequence-target">{request.target}</td>
                    <td>{signalParts.join(', ') || 'Request'}</td>
                  </tr>
                )
              })}
              {requests.length === 0 && (
                <tr>
                  <td colSpan={5} className="sequence-empty">No grouped child requests were found on this action.</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
      {sequenceExtractors.length > 0 && (
        <div className="sub-section http-sequence-section">
          <div className="sub-header">
            <h4>Extractors</h4>
            <span className="sequence-summary">{sequenceExtractors.length} extractor{sequenceExtractors.length === 1 ? '' : 's'}</span>
          </div>
          <div className="http-sequence-table-wrap">
            <table className="http-sequence-table">
              <thead>
                <tr>
                  <th>Variable</th>
                  <th>Type</th>
                  <th>Source</th>
                  <th>Expression</th>
                  <th>From</th>
                </tr>
              </thead>
              <tbody>
                {sequenceExtractors.map(({ extractor, requestName, key }) => (
                  <tr key={key}>
                    <td><strong>{extractor.name || '—'}</strong></td>
                    <td><span className="sequence-method">{extractor.type || '—'}</span></td>
                    <td>{extractor.source || '—'}</td>
                    <td className="sequence-target cell-mono">{extractor.expression || '—'}</td>
                    <td>{requestName}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </>
  )
}

interface ActionEditorHeaderProps {
  action: Action
  issues: AuthoringIssue[]
  methodColor: (method: string) => string
}

function ActionEditorHeader({ action, issues, methodColor }: ActionEditorHeaderProps) {
  const badgeLabel = action.type === 'http_sequence' ? 'SEQ' : action.type
  const fallbackCount = actionSelectorFallbackCount(action)
  const reviewGated = actionRequiresReview(action)
  return (
    <>
      <div className="action-editor-top">
        <span className="action-badge" style={{ background: methodColor(action.type), color: '#fff' }}>{badgeLabel}</span>
        <h3>{action.name || 'Unnamed Action'}</h3>
        {(isControlAction(action) || reviewGated || fallbackCount > 0) && (
          <div className="action-editor-issue-pills">
            {isControlAction(action) && <span className="action-signal-pill control">control</span>}
            {reviewGated && <span className="action-signal-pill warning">review</span>}
            {isReviewGatedJavaScriptAction(action) && <span className="action-signal-pill warning">js</span>}
            {fallbackCount > 0 && <span className="action-signal-pill selector">fallbacks {fallbackCount}</span>}
          </div>
        )}
        {issues.length > 0 && (
          <div className="action-editor-issue-pills">
            {issues.map((issue) => (
              <span key={issue.id} className={`action-signal-pill ${issue.severity}`}>{issue.severity}</span>
            ))}
          </div>
        )}
      </div>
      {issues.length > 0 && (
        <div className="action-editor-issues">
          {issues.map((issue) => (
            <div key={`${issue.id}:message`} className={`action-editor-issue ${issue.severity}`}>
              {issue.message}
            </div>
          ))}
        </div>
      )}
    </>
  )
}

interface ActionCoreFieldsProps {
  action: Action
  index: number
  protocol: string
  isBrowserScript: boolean
  isJmsScript: boolean
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
}

function formatControlActions(actions: Action[] | undefined) {
  return JSON.stringify(actions || [], null, 2)
}

function parseControlActions(value: string): Action[] | null {
  try {
    const parsed = JSON.parse(value)
    return Array.isArray(parsed) ? parsed as Action[] : null
  } catch {
    return null
  }
}

const DEFAULT_CONTROL_STEP: Action = { name: '', type: 'GET', target: '' } as Action

interface ControlBranchEditorProps {
  label: string
  steps: Action[] | undefined
  onChange: (steps: Action[]) => void
}

// Structured editor for a control block's child steps (then/else). Each step is
// an editable row (type / name / target) with add / remove / reorder, replacing
// the raw-JSON textarea. A per-branch "JSON" toggle is the escape hatch for
// anything the row form can't express (nested control, headers, extractors) —
// the documented depth boundary. Step edits preserve unknown keys (R1).
// Exported for focused render tests.
export function ControlBranchEditor({ label, steps, onChange }: ControlBranchEditorProps) {
  const list = steps || []
  const [mode, setMode] = useState<'structured' | 'json'>('structured')
  const [jsonDraft, setJsonDraft] = useState(() => formatControlActions(list))
  const [jsonInvalid, setJsonInvalid] = useState(false)

  useEffect(() => {
    if (mode === 'json') {
      setJsonDraft(formatControlActions(steps))
      setJsonInvalid(false)
    }
  }, [steps, mode])

  const commitJson = () => {
    const parsed = parseControlActions(jsonDraft)
    setJsonInvalid(parsed === null)
    if (parsed !== null) onChange(parsed)
  }

  return (
    <div className="control-branch full-width">
      <div className="control-branch-header">
        <span>{label}</span>
        <small>{list.length} step{list.length === 1 ? '' : 's'}</small>
        <div className="control-branch-modes">
          <button type="button" className={`btn btn-sm ${mode === 'structured' ? 'btn-secondary' : 'btn-ghost'}`} onClick={() => setMode('structured')}>Steps</button>
          <button type="button" className={`btn btn-sm ${mode === 'json' ? 'btn-secondary' : 'btn-ghost'}`} onClick={() => { setJsonDraft(formatControlActions(list)); setJsonInvalid(false); setMode('json') }}>JSON</button>
        </div>
      </div>
      {mode === 'structured' ? (
        <div className="control-step-list">
          {list.length === 0 && <div className="control-step-empty">No steps yet. Add one, or switch to JSON.</div>}
          {list.map((step, i) => (
            <div className="control-step-row" key={i}>
              <input className="control-step-type" value={step.type || ''} placeholder="type" aria-label={`${label} step ${i + 1} type`} onChange={e => onChange(updateActionStepField(list, i, 'type', e.target.value))} />
              <input className="control-step-name" value={step.name || ''} placeholder="name" aria-label={`${label} step ${i + 1} name`} onChange={e => onChange(updateActionStepField(list, i, 'name', e.target.value))} />
              <input className="control-step-target" value={step.target || ''} placeholder="target" aria-label={`${label} step ${i + 1} target`} onChange={e => onChange(updateActionStepField(list, i, 'target', e.target.value))} />
              <div className="control-step-actions">
                <button type="button" className="btn-icon" disabled={i === 0} aria-label="Move step up" onClick={() => onChange(moveActionStep(list, i, -1))}>↑</button>
                <button type="button" className="btn-icon" disabled={i === list.length - 1} aria-label="Move step down" onClick={() => onChange(moveActionStep(list, i, 1))}>↓</button>
                <button type="button" className="btn-icon danger" aria-label="Remove step" onClick={() => onChange(removeActionStep(list, i))}>✕</button>
              </div>
            </div>
          ))}
          <button type="button" className="btn btn-sm btn-ghost control-step-add" onClick={() => onChange(appendActionStep(list, { ...DEFAULT_CONTROL_STEP }))}>+ Add step</button>
        </div>
      ) : (
        <>
          <textarea className={`body-textarea ${jsonInvalid ? 'invalid' : ''}`} rows={6} value={jsonDraft} aria-label={`${label} JSON`} onChange={e => setJsonDraft(e.target.value)} onBlur={commitJson} />
          {jsonInvalid && <div className="browser-config-warning">{label} must be a JSON array.</div>}
        </>
      )}
    </div>
  )
}

interface ControlFlowFieldsProps {
  action: Action
  index: number
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
}

function ControlFlowFields({ action, index, onUpdate }: ControlFlowFieldsProps) {
  const control = action.control || {}
  const condition = control.condition || {}

  const updateControl = (patch: Record<string, unknown>) => {
    onUpdate(index, 'control', { ...control, ...patch })
  }
  const updateCondition = (patch: Record<string, unknown>) => {
    updateControl({ condition: { ...condition, ...patch } })
  }

  return (
    <div className="sub-section control-flow-section">
      <div className="sub-header"><h4>Control Flow</h4></div>
      <div className="form-grid">
        <label className="full-width">
          <span>Condition Left</span>
          <input value={condition.left || ''} onChange={e => updateCondition({ left: e.target.value })} placeholder="{{var.status}}" />
        </label>
        <label>
          <span>Operator</span>
          <select value={condition.op || 'exists'} onChange={e => updateCondition({ op: e.target.value })}>
            <option value="exists">exists</option>
            <option value="not_exists">not exists</option>
            <option value="eq">equals</option>
            <option value="ne">not equals</option>
            <option value="contains">contains</option>
            <option value="not_contains">not contains</option>
            <option value="gt">greater than</option>
            <option value="gte">greater/equal</option>
            <option value="lt">less than</option>
            <option value="lte">less/equal</option>
          </select>
        </label>
        <label>
          <span>Condition Right</span>
          <input value={condition.right || ''} onChange={e => updateCondition({ right: e.target.value })} placeholder="ready" />
        </label>
        {action.type === 'control_loop' && (
          <>
            <label>
              <span>Loop Count</span>
              <input type="number" min="0" value={control.count ?? 0} onChange={e => updateControl({ count: Math.max(0, Number(e.target.value) || 0) })} />
            </label>
            <label>
              <span>Max Iterations</span>
              <input type="number" min="0" value={control.max_iterations ?? 0} onChange={e => updateControl({ max_iterations: Math.max(0, Number(e.target.value) || 0) })} />
            </label>
          </>
        )}
        <ControlBranchEditor label={action.type === 'control_loop' ? 'Loop steps' : 'Then steps'} steps={control.steps} onChange={steps => updateControl({ steps })} />
        {action.type === 'control_if' && (
          <ControlBranchEditor label="Else steps" steps={control.else} onChange={steps => updateControl({ else: steps })} />
        )}
      </div>
    </div>
  )
}

function ActionCoreFields({ action, index, protocol, isBrowserScript, isJmsScript, onUpdate }: ActionCoreFieldsProps) {
  const showSelector = isBrowserScript && dslActionUsesSelector(action.type)
  const showValue = isBrowserScript && dslActionUsesValue(action.type)
  const isWebSocketScript = protocol === 'websocket'
  const isGrpcScript = protocol === 'grpc'
  const isDatabaseScript = protocol === 'database'
  const isNetworkScript = protocol === 'network'
  const targetLabel = isJmsScript
    ? 'Destination'
    : isBrowserScript
    ? action.type === 'browser_navigate'
      ? 'URL or Path'
      : action.type === 'browser_evaluate_js'
        ? 'JavaScript'
        : action.type === 'browser_switch_window'
          ? 'Window Title or URL'
      : action.type === 'browser_wait'
        ? 'Wait Target or Duration'
        : 'Target Selector'
    : isWebSocketScript
      ? action.type === 'ws_connect' ? 'Endpoint' : 'Connection or Topic'
      : isGrpcScript
        ? 'Service / Method'
        : isDatabaseScript
          ? action.type === 'db_procedure' ? 'Procedure' : 'SQL'
          : isNetworkScript
            ? 'Endpoint / Host'
    : 'Target (path)'
  const targetPlaceholder = isJmsScript
    ? 'orders.in or {{data.destinations.queue}}'
    : isBrowserScript
    ? action.type === 'browser_navigate'
      ? 'https://example.com or /login'
      : action.type === 'browser_evaluate_js'
        ? 'window.app.ready === true'
        : action.type === 'browser_switch_window'
          ? 'checkout or /payment'
      : action.type === 'browser_wait'
        ? '#ready, text, or 0.75s'
        : '#submit'
    : isWebSocketScript
      ? action.type === 'ws_connect' ? '/ws or wss://example.com/ws' : 'Leave blank to use active connection'
      : isGrpcScript
        ? 'package.Service/Method'
        : isDatabaseScript
          ? 'select id from users where id = ?'
          : isNetworkScript
            ? 'localhost:8080, example.com, or dc=example,dc=com'
    : '/api/resource'
  const actionTypes = isBrowserScript
    ? BROWSER_ACTION_TYPES
    : isJmsScript
      ? JMS_ACTION_TYPES
      : isWebSocketScript
        ? WEBSOCKET_ACTION_TYPES
        : isGrpcScript
          ? GRPC_ACTION_TYPES
          : isDatabaseScript
            ? DATABASE_ACTION_TYPES
            : isNetworkScript
              ? NETWORK_ACTION_TYPES
              : HTTP_ACTION_TYPES
  const actionTypeLabel = isBrowserScript || isJmsScript || isWebSocketScript || isGrpcScript || isDatabaseScript || isNetworkScript ? 'Action Type' : 'Method'

  // http_oauth2_token is an engine action driven by params (no target/headers/body),
  // so it gets a dedicated field editor instead of the generic HTTP request inputs.
  const isOAuthTokenAction = action.type === 'http_oauth2_token'
  const oauthParams = (action.params || {}) as Record<string, unknown>
  const oauthStr = (key: string, fallback = '') => (typeof oauthParams[key] === 'string' ? (oauthParams[key] as string) : fallback)
  const setOAuthParam = (key: string, value: string) => onUpdate(index, 'params', { ...oauthParams, [key]: value })
  const oauthGrant = oauthStr('grant_type', 'client_credentials')

  return (
    <div className="form-grid">
      <label><span>Name</span><input value={action.name} onChange={e => onUpdate(index, 'name', e.target.value)} /></label>
      <label><span>{actionTypeLabel}</span>
        <select value={action.type} onChange={e => onUpdate(index, 'type', e.target.value)}>
          {actionTypes.map(type => <option key={type}>{type}</option>)}
        </select>
      </label>
      <label><span>Transaction</span>
        <input
          value={actionTransactionName(action)}
          placeholder="(none)"
          title="Wrap this action in a named transaction — timed and aggregated (TPS, percentiles, pass/fail); SLAs attach to it."
          onChange={e => onUpdate(index, 'metadata', setActionTransactionName(action, e.target.value).metadata)}
        />
      </label>
      {isOAuthTokenAction ? (
        <>
          <label><span>Grant Type</span>
            <select value={oauthGrant} onChange={e => setOAuthParam('grant_type', e.target.value)}>
              <option value="client_credentials">client_credentials</option>
              <option value="jwt_bearer">jwt_bearer</option>
              <option value="refresh_token">refresh_token</option>
              <option value="password">password</option>
            </select>
          </label>
          <label><span>Save As (variable)</span><input value={oauthStr('save_as', 'access_token')} onChange={e => setOAuthParam('save_as', e.target.value)} placeholder="access_token" /></label>
          <label className="full-width"><span>Token URL</span><input value={oauthStr('token_url')} onChange={e => setOAuthParam('token_url', e.target.value)} placeholder="https://login.salesforce.com/services/oauth2/token" /></label>
          <label><span>Client ID</span><input value={oauthStr('client_id')} onChange={e => setOAuthParam('client_id', e.target.value)} placeholder="{{env.CLIENT_ID}}" /></label>
          <label><span>Client Secret</span><input value={oauthStr('client_secret')} onChange={e => setOAuthParam('client_secret', e.target.value)} placeholder="{{secret.CLIENT_SECRET}}" /></label>
          <label><span>Scope</span><input value={oauthStr('scope')} onChange={e => setOAuthParam('scope', e.target.value)} placeholder="api full" /></label>
          <label><span>Audience</span><input value={oauthStr('audience')} onChange={e => setOAuthParam('audience', e.target.value)} placeholder="https://login.salesforce.com" /></label>
          {oauthGrant === 'jwt_bearer' && (
            <>
              <label><span>Issuer (iss)</span><input value={oauthStr('issuer')} onChange={e => setOAuthParam('issuer', e.target.value)} placeholder="{{env.CLIENT_ID}}" /></label>
              <label><span>Subject (sub)</span><input value={oauthStr('subject')} onChange={e => setOAuthParam('subject', e.target.value)} placeholder="user@example.com" /></label>
              <label className="full-width"><span>Private Key (PEM)</span><input value={oauthStr('private_key')} onChange={e => setOAuthParam('private_key', e.target.value)} placeholder="{{secret.JWT_PRIVATE_KEY}}" /></label>
            </>
          )}
          {oauthGrant === 'refresh_token' && (
            <label className="full-width"><span>Refresh Token</span><input value={oauthStr('refresh_token')} onChange={e => setOAuthParam('refresh_token', e.target.value)} placeholder="{{secret.REFRESH_TOKEN}}" /></label>
          )}
          {oauthGrant === 'password' && (
            <>
              <label><span>Username</span><input value={oauthStr('username')} onChange={e => setOAuthParam('username', e.target.value)} placeholder="{{env.USERNAME}}" /></label>
              <label><span>Password</span><input value={oauthStr('password')} onChange={e => setOAuthParam('password', e.target.value)} placeholder="{{secret.PASSWORD}}" /></label>
            </>
          )}
        </>
      ) : (
        <label className="full-width"><span>{targetLabel}</span><input value={action.target} onChange={e => onUpdate(index, 'target', e.target.value)} placeholder={targetPlaceholder} /></label>
      )}
      {showSelector && (
        <label className="full-width"><span>Selector Override</span><input value={action.selector || ''} onChange={e => onUpdate(index, 'selector', e.target.value)} placeholder="#submit, input[name=email], [data-testid=save]" /></label>
      )}
      {showSelector && (
        <label className="full-width">
          <span>Object Repository Ref</span>
          <input list="object-ref-suggestions" value={action.object_ref || ''} onChange={e => onUpdate(index, 'object_ref', e.target.value)} placeholder="login or object-repo-…:login — resolves to a repository object" />
          <small>Binds to a project object repository by logical name; resolved to a selector + fallbacks at run time (takes precedence over the inline selector when it resolves).</small>
        </label>
      )}
      {showValue && (
        <label className="full-width"><span>{action.type === 'browser_set_files' ? 'File Path(s)' : action.type === 'browser_press_key' ? 'Key' : 'Value'}</span><input value={action.value || ''} onChange={e => onUpdate(index, 'value', e.target.value)} placeholder={action.type === 'browser_set_files' ? '/tmp/upload.pdf or {{data.files.path}}' : action.type === 'browser_press_key' ? 'Enter' : '{{data.users.email}}'} /></label>
      )}
    </div>
  )
}

interface ActionThinkTimeToggleProps {
  action: Action
  index: number
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
}

function ActionThinkTimeToggle({ action, index, onUpdate }: ActionThinkTimeToggleProps) {
  return (
    <div className="sub-section">
      <label className="checkbox-label">
        <input type="checkbox" checked={action.think_time_after ?? false} onChange={e => onUpdate(index, 'think_time_after', e.target.checked)} />
        <span>Apply think time after this action</span>
      </label>
    </div>
  )
}

interface ActionParameterMenuProps {
  selectionState: ActionEditorSelectionState | null
  onOpenParameterEditor: (nextExistingName?: string | null) => void
  onOpenExistingParameter?: () => void
  onOpenOccurrenceReview?: () => void
  variant?: 'action' | 'json'
}

export function ActionParameterMenu({
  selectionState,
  onOpenParameterEditor,
  onOpenExistingParameter,
  onOpenOccurrenceReview,
  variant = 'action',
}: ActionParameterMenuProps) {
  if (!selectionState) return null
  const isJson = variant === 'json'

  return (
    <div
      className="action-parameter-menu"
      style={{ left: selectionState.x, top: selectionState.y }}
      onClick={event => event.stopPropagation()}
    >
      <button type="button" className="btn btn-sm" onClick={() => onOpenParameterEditor(selectionState.existingName)}>
        {isJson ? 'Create new parameter' : 'Parameterize selection'}
      </button>
      {isJson && onOpenExistingParameter && (
        <button type="button" className="btn btn-sm btn-ghost" onClick={onOpenExistingParameter}>
          Use existing parameter
        </button>
      )}
      {isJson && onOpenOccurrenceReview && (
        <button type="button" className="btn btn-sm btn-ghost" onClick={onOpenOccurrenceReview}>
          Review matching occurrences
        </button>
      )}
      {selectionState.existingName && (
        <button type="button" className="btn btn-sm btn-ghost" onClick={() => onOpenParameterEditor(selectionState.existingName)}>
          Edit parameter
        </button>
      )}
    </div>
  )
}

export function ValidationEvidencePanel({ evidence }: { evidence: ValidationEvidence }) {
  const dataFit = evidence.data_source_fit
  const aiGuardrails = evidence.ai_guardrails
  const health = evidence.authoring_health
  if (!dataFit && !aiGuardrails && !health) {
    return null
  }

  return (
    <div className="validation-evidence-panel">
      {health && (
        <div className="validation-evidence-summary">
          <strong>Authoring Health</strong>
          <span className={`validation-evidence-status ${health.status}`}>{formatValidationStatus(health.status)} · {health.score}</span>
        </div>
      )}
      {dataFit && (
        <section className="validation-evidence-section">
          <div className="validation-evidence-section-header">
            <strong>Data-Source Fit</strong>
            <span className={`validation-evidence-status ${dataFit.status}`}>{formatValidationStatus(dataFit.status)}</span>
          </div>
          {dataFit.items.length === 0 ? (
            <p>Data sources match referenced parameters and modeled row usage.</p>
          ) : (
            <ul>
              {dataFit.items.slice(0, 5).map((item, index) => (
                <li key={`${item.data_source || 'source'}:${item.parameter || 'parameter'}:${item.column || 'column'}:${index}`}>
                  <span className={`validation-evidence-status ${item.status}`}>{formatValidationStatus(item.status)}</span>
                  <span>{item.summary}</span>
                  <small>{formatDataSourceFitContext(item)}</small>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}
      {aiGuardrails && (
        <section className="validation-evidence-section">
          <div className="validation-evidence-section-header">
            <strong>AI Draft Guardrails</strong>
            <span className={`validation-evidence-status ${aiGuardrails.status}`}>{formatValidationStatus(aiGuardrails.status)}</span>
          </div>
          <ul>
            {aiGuardrails.items.map(item => (
              <li key={item.key}>
                <span className={`validation-evidence-status ${item.status}`}>{formatValidationStatus(item.status)}</span>
                <span>{item.summary}</span>
                <small>{formatValidationStatus(item.key)}</small>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  )
}

function formatValidationStatus(value: string): string {
  const text = String(value || '').trim()
  if (!text) return 'Unknown'
  return text.replace(/_/g, ' ').replace(/\b\w/g, char => char.toUpperCase())
}

function formatDataSourceFitContext(item: NonNullable<ValidationEvidence['data_source_fit']>['items'][number]): string {
  const parts = [
    item.data_source ? `Source ${item.data_source}` : '',
    item.parameter ? `Parameter ${item.parameter}` : '',
    item.column ? `Column ${item.column}` : '',
    item.required_rows || item.available_rows ? `${item.available_rows || 0}/${item.required_rows || 0} rows` : '',
  ].filter(Boolean)
  return parts.join(' · ')
}

function useActionParameterMenu({
  index,
  onBeginParameterEdit,
  parameters,
}: {
  index: number
  onBeginParameterEdit: (draft: ActionEditorParameterDraft) => void
  parameters: ParameterConfig[]
}) {
  const [selectionState, setSelectionState] = useState<ActionEditorSelectionState | null>(null)

  useEffect(() => {
    if (!selectionState) return
    const closeMenu = () => setSelectionState(null)
    window.addEventListener('click', closeMenu)
    return () => window.removeEventListener('click', closeMenu)
  }, [selectionState])

  const detectSelectedParameter = useCallback((selectedText: string): ParameterConfig | null => {
    const match = selectedText.trim().match(/^{{\s*param\.([a-zA-Z0-9_]+)\s*}}$/)
    if (!match) return null
    const normalizedName = sanitizeVarName(match[1])
    return parameters.find(parameter => sanitizeVarName(parameter.name || '') === normalizedName) || null
  }, [parameters])

  const openSelectionMenu = useCallback((event: ReactMouseEvent<HTMLTextAreaElement>) => {
    const textarea = event.currentTarget
    const start = textarea.selectionStart ?? 0
    const end = textarea.selectionEnd ?? 0
    const selectedText = (textarea.value || '').slice(start, end)
    if (!selectedText.trim()) {
      setSelectionState(null)
      return
    }

    event.preventDefault()
    const existingParameter = detectSelectedParameter(selectedText)
    setSelectionState({
      start,
      end,
      selectedText,
      x: event.clientX,
      y: event.clientY,
      existingName: existingParameter?.name || null,
    })
  }, [detectSelectedParameter])

  const openParameterEditor = useCallback((nextExistingName?: string | null) => {
    if (!selectionState) return
    const existingParameter = nextExistingName
      ? parameters.find(parameter => sanitizeVarName(parameter.name || '') === sanitizeVarName(nextExistingName))
      : detectSelectedParameter(selectionState.selectedText)

    const requestedName = existingParameter?.name
      || sanitizeVarName(selectionState.selectedText.replace(/^{{\s*param\.|\s*}}$/g, ''))
    const seedValue = existingParameter?.values?.[0] || selectionState.selectedText

    setSelectionState(null)
    onBeginParameterEdit({
      source: 'action',
      actionIndex: index,
      start: selectionState.start,
      end: selectionState.end,
      selectedText: selectionState.selectedText,
      existingName: existingParameter?.name || null,
      requestedName,
      seedValue,
    })
  }, [detectSelectedParameter, index, onBeginParameterEdit, parameters, selectionState])

  return { openParameterEditor, openSelectionMenu, selectionState }
}
