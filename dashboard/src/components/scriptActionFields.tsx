import { bodyLanguage } from './actionBodyModel'
import { HTTPActionVersionFields } from './HTTPActionVersionFields'
import { ManagedActionChecks, OpenActionBody } from './actionInspectorContext'
// Per-protocol action field editors and the shared header/extractor/assertion
// section editors used by the ScriptEditor ActionEditor. Extracted from
// ScriptEditor.tsx: these are self-contained presentational components driven
// entirely by props (no parent state, hooks, or shared mutable helpers).

import { type MouseEvent as ReactMouseEvent, useMemo, useContext } from 'react'
import type {
  ScriptActionDefinition,
  ScriptAssertionDefinition,
} from '../types/scripts'
import { headerListToCompatibilityMap } from './scriptActionHelpers'
import CodeLineWrapToggle from './CodeLineWrapToggle'
import SourceCodeViewer from './SourceCodeViewer'
import { useCodeLineWrapPreference } from './codeLineWrapPreference'
import { formatXMLForDisplay } from './xmlDisplayFormat'
import { BODY_ENCODING_OPTIONS, bodyEncodingLabel, knownBodyEncoding } from './bodyEncodings'

type Action = ScriptActionDefinition
type Assertion = ScriptAssertionDefinition

interface ActionBodyTextareaProps {
  onChange: (value: string) => void
  onContextMenu: (event: ReactMouseEvent<HTMLTextAreaElement>) => void
  placeholder: string
  rows: number
  value: string
}

function ActionBodyTextarea({ onChange, onContextMenu, placeholder, rows, value }: ActionBodyTextareaProps) {
  const [wrapLines] = useCodeLineWrapPreference()
  return (
    <textarea
      className="body-textarea"
      data-word-wrap={wrapLines ? 'true' : 'false'}
      value={value}
      onChange={event => onChange(event.target.value)}
      onContextMenu={onContextMenu}
      placeholder={placeholder}
      rows={rows}
      wrap={wrapLines ? 'soft' : 'off'}
    />
  )
}

function ActionBodyWrapToggle() {
  const [wrapLines, setWrapLines] = useCodeLineWrapPreference()
  return <CodeLineWrapToggle wrapLines={wrapLines} onChange={setWrapLines} />
}

export interface BrowserActionFieldsProps {
  action: Action
  index: number
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
}

export function BrowserActionFields({ action, index, onUpdate }: BrowserActionFieldsProps) {
  const waitFor = action.wait_for || {}
  const capture = action.capture || {}
  const params = action.params || {}
  const sync = action.sync || {}

  return (
    <>
      {action.type === 'browser_wait' && (
        <div className="sub-section browser-action-section">
          <div className="sub-header"><h4>Wait Condition</h4></div>
          <div className="browser-action-grid">
            <label>
              <span>Type</span>
              <select
                value={waitFor.type || 'selector'}
                onChange={e => onUpdate(index, 'wait_for', { ...waitFor, type: e.target.value })}
              >
                <option value="selector">Selector visible</option>
                <option value="hidden">Selector hidden</option>
                <option value="text">Text appears</option>
                <option value="url_contains">URL contains</option>
                <option value="duration">Duration</option>
              </select>
            </label>
            <label>
              <span>Value</span>
              <input
                value={waitFor.value || ''}
                onChange={e => onUpdate(index, 'wait_for', { ...waitFor, value: e.target.value })}
                placeholder={waitFor.type === 'duration' ? '1000ms' : '#ready or expected text'}
              />
            </label>
            <label>
              <span>Timeout (ms)</span>
              <input
                type="number"
                min="0"
                value={waitFor.timeout_ms ?? 10000}
                onChange={e => onUpdate(index, 'wait_for', { ...waitFor, timeout_ms: Math.max(0, Number(e.target.value) || 0) })}
              />
            </label>
          </div>
        </div>
      )}

      {action.type === 'browser_long_press' && (
        <div className="sub-section browser-action-section">
          <div className="sub-header"><h4>Long Press</h4></div>
          <div className="browser-action-grid">
            <label>
              <span>Duration (ms)</span>
              <input
                type="number"
                min="1"
                value={typeof params.duration_ms === 'number' ? params.duration_ms : 350}
                onChange={e => onUpdate(index, 'params', { ...params, duration_ms: Math.max(1, Number(e.target.value) || 1) })}
              />
            </label>
          </div>
        </div>
      )}

      {action.type === 'browser_swipe' && (
        <div className="sub-section browser-action-section">
          <div className="sub-header"><h4>Swipe Gesture</h4></div>
          <div className="browser-action-grid">
            <label>
              <span>Direction</span>
              <select
                value={typeof params.direction === 'string' ? params.direction : 'left'}
                onChange={e => onUpdate(index, 'params', { ...params, direction: e.target.value })}
              >
                <option value="left">Left</option>
                <option value="right">Right</option>
                <option value="up">Up</option>
                <option value="down">Down</option>
              </select>
            </label>
            <label>
              <span>Distance (px)</span>
              <input
                type="number"
                min="1"
                value={typeof params.distance === 'number' ? params.distance : 180}
                onChange={e => onUpdate(index, 'params', { ...params, distance: Math.max(1, Number(e.target.value) || 1) })}
              />
            </label>
          </div>
        </div>
      )}

      {action.type === 'browser_drag_and_drop' && (
        <div className="sub-section browser-action-section">
          <div className="sub-header"><h4>Drag Target</h4></div>
          <div className="browser-action-grid">
            <label className="full-width">
              <span>Drop Target Selector</span>
              <input
                value={typeof params.target === 'string' ? params.target : ''}
                onChange={e => onUpdate(index, 'params', { ...params, target: e.target.value })}
                placeholder="#drop-zone"
              />
            </label>
          </div>
        </div>
      )}

      <details className="browser-action-section"><summary>Advanced: synchronization and capture</summary><div className="sub-section browser-action-section">
        <div className="sub-header"><h4>Post-Action Sync</h4></div>
        <div className="browser-action-grid">
          <label>
            <span>Type</span>
            <select value={sync.type || ''} onChange={e => onUpdate(index, 'sync', { ...sync, type: e.target.value || undefined })}>
              <option value="">Default</option>
              <option value="network_idle">Network idle</option>
              <option value="dom_stable">DOM stable</option>
              <option value="element_visible">Element visible</option>
              <option value="load">Load event</option>
              <option value="domcontentloaded">DOM content loaded</option>
              <option value="duration">Duration</option>
            </select>
          </label>
          <label>
            <span>Value</span>
            <input
              value={sync.value || ''}
              onChange={e => onUpdate(index, 'sync', { ...sync, value: e.target.value || undefined })}
              placeholder={sync.type === 'duration' ? '750ms' : '#ready'}
            />
          </label>
          <label>
            <span>Timeout (ms)</span>
            <input
              type="number"
              min="0"
              value={sync.timeout_ms ?? ''}
              onChange={e => onUpdate(index, 'sync', { ...sync, timeout_ms: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value) || 0) })}
            />
          </label>
          <label>
            <span>Stable (ms)</span>
            <input
              type="number"
              min="0"
              value={sync.stable_ms ?? ''}
              onChange={e => onUpdate(index, 'sync', { ...sync, stable_ms: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value) || 0) })}
            />
          </label>
        </div>
      </div>

      <div className="sub-section browser-action-section">
        <div className="sub-header"><h4>Capture</h4></div>
        <div className="browser-capture-row">
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={!!capture.screenshot || action.type === 'browser_screenshot'}
              disabled={action.type === 'browser_screenshot'}
              onChange={e => onUpdate(index, 'capture', { ...capture, screenshot: e.target.checked })}
            />
            <span>Screenshot</span>
          </label>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={!!capture.full_page}
              onChange={e => onUpdate(index, 'capture', { ...capture, full_page: e.target.checked })}
            />
            <span>Full page</span>
          </label>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={!!capture.html}
              onChange={e => onUpdate(index, 'capture', { ...capture, html: e.target.checked })}
            />
            <span>HTML</span>
          </label>
        </div>
      </div>
    </details>
    </>
  )
}

function formatActionStringList(value: unknown): string {
  if (Array.isArray(value)) return value.map(item => String(item ?? '')).join('\n')
  if (typeof value === 'string') return value
  return ''
}

function parseActionStringList(value: string): string[] {
  return value.split(/\n|,/).map(item => item.trim()).filter(Boolean)
}

export interface WebSocketActionFieldsProps {
  action: Action
  index: number
  onAddAssertion: (i: number) => void
  onAddExtractor: (i: number) => void
  onAddHeader: (i: number) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onRemoveHeader: (ai: number, k: string) => void
  onSelectionMenu: (event: ReactMouseEvent<HTMLTextAreaElement>) => void
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
  onUpdateHeader: (ai: number, ok: string, nk: string, v: string) => void
}

export function WebSocketActionFields({
  action,
  index,
  onAddAssertion,
  onAddExtractor,
  onAddHeader,
  onRemoveAssertion,
  onRemoveExtractor,
  onRemoveHeader,
  onSelectionMenu,
  onUpdate,
  onUpdateAssertion,
  onUpdateExtractor,
  onUpdateHeader,
}: WebSocketActionFieldsProps) {
  const params = action.params || {}
  return (
    <>
      <div className="sub-section">
        <div className="sub-header">
          <h4>WebSocket Operation</h4>
          {action.type === 'ws_send' && <ActionBodyWrapToggle />}
        </div>
        <div className="form-grid">
          {(action.type === 'ws_send' || action.type === 'ws_receive') && (
            <label>
              <span>Message Type</span>
              <select value={typeof params.type === 'string' ? params.type : 'text'} onChange={e => onUpdate(index, 'params', { ...params, type: e.target.value })}>
                <option value="text">Text</option>
                <option value="binary">Binary</option>
                <option value="json">JSON</option>
              </select>
            </label>
          )}
          {(action.type === 'ws_receive' || action.type === 'ws_connect') && (
            <label>
              <span>Timeout (ms)</span>
              <input type="number" value={typeof params.timeout_ms === 'number' ? params.timeout_ms : 30000}
                onChange={e => onUpdate(index, 'params', { ...params, timeout_ms: Number(e.target.value) || undefined })} />
            </label>
          )}
          {action.type === 'ws_receive' && (
            <label className="full-width">
              <span>Expected Message Text</span>
              <input value={typeof params.expected === 'string' ? params.expected : ''} onChange={e => onUpdate(index, 'params', { ...params, expected: e.target.value })} placeholder="created, pong, or {{var.expected_event}}" />
            </label>
          )}
          {action.type === 'ws_send' && (
            <label className="full-width">
              <span>Message Body</span>
              <ActionBodyTextarea
                onChange={value => onUpdate(index, 'body', value)}
                onContextMenu={onSelectionMenu}
                placeholder='{"type":"ping","id":"{{param.request_id}}"}'
                rows={5}
                value={action.body || ''}
              />
            </label>
          )}
        </div>
      </div>

      {action.type === 'ws_connect' && (
        <ProtocolHeadersSection action={action} index={index} onAddHeader={onAddHeader} onRemoveHeader={onRemoveHeader} onUpdate={onUpdate} onUpdateHeader={onUpdateHeader} />
      )}

      <ProtocolExtractorsSection action={action} index={index} protocol="websocket" onAddExtractor={onAddExtractor} onRemoveExtractor={onRemoveExtractor} onUpdateExtractor={onUpdateExtractor} />
      <ProtocolAssertionsSection action={action} index={index} protocol="websocket" onAddAssertion={onAddAssertion} onRemoveAssertion={onRemoveAssertion} onUpdateAssertion={onUpdateAssertion} />
    </>
  )
}

export interface GrpcActionFieldsProps {
  action: Action
  index: number
  onAddAssertion: (i: number) => void
  onAddExtractor: (i: number) => void
  onAddHeader: (i: number) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onRemoveHeader: (ai: number, k: string) => void
  onSelectionMenu: (event: ReactMouseEvent<HTMLTextAreaElement>) => void
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
  onUpdateHeader: (ai: number, ok: string, nk: string, v: string) => void
}

export function GrpcActionFields({
  action,
  index,
  onAddAssertion,
  onAddExtractor,
  onAddHeader,
  onRemoveAssertion,
  onRemoveExtractor,
  onRemoveHeader,
  onSelectionMenu,
  onUpdate,
  onUpdateAssertion,
  onUpdateExtractor,
  onUpdateHeader,
}: GrpcActionFieldsProps) {
  const params = action.params || {}
  return (
    <>
      <div className="sub-section">
        <div className="sub-header"><h4>gRPC Message</h4><ActionBodyWrapToggle /></div>
        <div className="form-grid">
          <label>
            <span>Timeout (ms)</span>
            <input type="number" value={typeof params.timeout_ms === 'number' ? params.timeout_ms : 30000}
              onChange={e => onUpdate(index, 'params', { ...params, timeout_ms: Number(e.target.value) || undefined })} />
          </label>
          {(action.type === 'grpc_server_stream' || action.type === 'grpc_bidi_stream') && (
            <label>
              <span>Max Messages</span>
              <input type="number" value={typeof params.max_messages === 'number' ? params.max_messages : 1}
                onChange={e => onUpdate(index, 'params', { ...params, max_messages: Number(e.target.value) || undefined })} />
            </label>
          )}
          {(action.type === 'grpc_client_stream' || action.type === 'grpc_bidi_stream') && (
            <label className="full-width">
              <span>Stream Messages</span>
              <ActionBodyTextarea
                rows={5}
                value={formatActionStringList(params.messages)}
                onChange={value => onUpdate(index, 'params', { ...params, messages: parseActionStringList(value) })}
                onContextMenu={onSelectionMenu}
                placeholder={'{"id":"1"}\n{"id":"2"}'}
              />
            </label>
          )}
          {(action.type === 'grpc_unary' || action.type === 'grpc_server_stream') && (
            <label className="full-width">
              <span>Request Body</span>
              <ActionBodyTextarea
                onChange={value => onUpdate(index, 'body', value)}
                onContextMenu={onSelectionMenu}
                placeholder='{"id":"{{param.order_id}}"}'
                rows={5}
                value={action.body || ''}
              />
            </label>
          )}
        </div>
      </div>

      <ProtocolHeadersSection action={action} index={index} onAddHeader={onAddHeader} onRemoveHeader={onRemoveHeader} onUpdate={onUpdate} onUpdateHeader={onUpdateHeader} />
      <ProtocolExtractorsSection action={action} index={index} protocol="grpc" onAddExtractor={onAddExtractor} onRemoveExtractor={onRemoveExtractor} onUpdateExtractor={onUpdateExtractor} />
      <ProtocolAssertionsSection action={action} index={index} protocol="grpc" onAddAssertion={onAddAssertion} onRemoveAssertion={onRemoveAssertion} onUpdateAssertion={onUpdateAssertion} />
    </>
  )
}

export interface DatabaseActionFieldsProps {
  action: Action
  index: number
  onAddAssertion: (i: number) => void
  onAddExtractor: (i: number) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onSelectionMenu: (event: ReactMouseEvent<HTMLTextAreaElement>) => void
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
}

export function DatabaseActionFields({
  action,
  index,
  onAddAssertion,
  onAddExtractor,
  onRemoveAssertion,
  onRemoveExtractor,
  onSelectionMenu,
  onUpdate,
  onUpdateAssertion,
  onUpdateExtractor,
}: DatabaseActionFieldsProps) {
  const params = action.params || {}
  const isSQLAction = ['db_query', 'db_exec', 'db_procedure'].includes(action.type)
  return (
    <>
      {isSQLAction && (
        <div className="sub-section">
          <div className="sub-header"><h4>SQL And Binds</h4><ActionBodyWrapToggle /></div>
          <div className="form-grid">
            <label className="full-width">
              <span>{action.type === 'db_procedure' ? 'Procedure / Call' : 'SQL'}</span>
              <ActionBodyTextarea
                onChange={value => onUpdate(index, 'target', value)}
                onContextMenu={onSelectionMenu}
                placeholder={action.type === 'db_query' ? 'select id, name from users where id = ?' : 'update users set last_seen = current_timestamp where id = ?'}
                rows={5}
                value={action.target || ''}
              />
            </label>
            <label className="full-width">
              <span>Bind Parameters</span>
              <ActionBodyTextarea
                rows={3}
                value={formatActionStringList(params.binds)}
                onChange={value => onUpdate(index, 'params', { ...params, binds: parseActionStringList(value) })}
                onContextMenu={onSelectionMenu}
                placeholder={'{{param.user_id}}\n{{data.orders.id}}'}
              />
            </label>
            <label>
              <span>Max Rows</span>
              <input type="number" value={typeof params.max_rows === 'number' ? params.max_rows : ''}
                onChange={e => onUpdate(index, 'params', { ...params, max_rows: Number(e.target.value) || undefined })} placeholder="Use runtime default" />
            </label>
          </div>
        </div>
      )}

      <ProtocolExtractorsSection action={action} index={index} protocol="database" onAddExtractor={onAddExtractor} onRemoveExtractor={onRemoveExtractor} onUpdateExtractor={onUpdateExtractor} />
      <ProtocolAssertionsSection action={action} index={index} protocol="database" onAddAssertion={onAddAssertion} onRemoveAssertion={onRemoveAssertion} onUpdateAssertion={onUpdateAssertion} />
    </>
  )
}

export interface NetworkActionFieldsProps {
  action: Action
  index: number
  onAddAssertion: (i: number) => void
  onAddExtractor: (i: number) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onSelectionMenu: (event: ReactMouseEvent<HTMLTextAreaElement>) => void
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
}

export function NetworkActionFields({
  action,
  index,
  onAddAssertion,
  onAddExtractor,
  onRemoveAssertion,
  onRemoveExtractor,
  onSelectionMenu,
  onUpdate,
  onUpdateAssertion,
  onUpdateExtractor,
}: NetworkActionFieldsProps) {
  const params = action.params || {}
  const hasPayload = ['tcp_request', 'udp_request', 'smtp_send'].includes(action.type)
  const hasCommand = ['ftp_command', 'pop3_command', 'imap_command'].includes(action.type)
  return (
    <>
      <div className="sub-section">
        <div className="sub-header">
          <h4>Protocol Controls</h4>
          {hasPayload && <ActionBodyWrapToggle />}
        </div>
        <div className="form-grid">
          {action.type === 'dns_query' && (
            <label>
              <span>Record Type</span>
              <select value={typeof params.record_type === 'string' ? params.record_type : 'A'} onChange={e => onUpdate(index, 'params', { ...params, record_type: e.target.value })}>
                <option value="A">A</option>
                <option value="AAAA">AAAA</option>
                <option value="CNAME">CNAME</option>
                <option value="MX">MX</option>
                <option value="NS">NS</option>
                <option value="TXT">TXT</option>
              </select>
            </label>
          )}
          {(action.type === 'tcp_request' || action.type === 'udp_request') && (
            <>
              <label className="checkbox-label">
                <input type="checkbox" checked={Boolean(params.read)}
                  onChange={e => onUpdate(index, 'params', { ...params, read: e.target.checked })} />
                <span>Read response</span>
              </label>
              <label>
                <span>Read Bytes</span>
                <input type="number" value={typeof params.read_bytes === 'number' ? params.read_bytes : 4096}
                  onChange={e => onUpdate(index, 'params', { ...params, read_bytes: Number(e.target.value) || undefined })} />
              </label>
            </>
          )}
          {hasCommand && (
            <label className="full-width">
              <span>Command</span>
              <input value={typeof params.command === 'string' ? params.command : action.body || ''} onChange={e => onUpdate(index, 'params', { ...params, command: e.target.value })} placeholder="NOOP, STAT, CAPABILITY" />
            </label>
          )}
          {(action.type === 'smtp_send' || action.type === 'ldap_bind' || action.type === 'ldap_search') && (
            <>
              <label>
                <span>Username</span>
                <input value={typeof params.username === 'string' ? params.username : ''} onChange={e => onUpdate(index, 'params', { ...params, username: e.target.value })} />
              </label>
              <label>
                <span>Password</span>
                <input type="password" value={typeof params.password === 'string' ? params.password : ''} onChange={e => onUpdate(index, 'params', { ...params, password: e.target.value })} />
              </label>
            </>
          )}
          {action.type === 'ldap_search' && (
            <>
              <label className="full-width">
                <span>LDAP Filter</span>
                <input value={typeof params.filter === 'string' ? params.filter : '(objectClass=*)'} onChange={e => onUpdate(index, 'params', { ...params, filter: e.target.value })} />
              </label>
              <label>
                <span>Max Entries</span>
                <input type="number" value={typeof params.max_entries === 'number' ? params.max_entries : 100} onChange={e => onUpdate(index, 'params', { ...params, max_entries: Number(e.target.value) || undefined })} />
              </label>
            </>
          )}
          {hasPayload && (
            <label className="full-width">
              <span>Payload</span>
              <ActionBodyTextarea
                onChange={value => onUpdate(index, 'body', value)}
                onContextMenu={onSelectionMenu}
                placeholder={action.type === 'smtp_send' ? 'Subject: Smoke\n\nHello' : 'ping'}
                rows={5}
                value={action.body || ''}
              />
            </label>
          )}
        </div>
      </div>

      <ProtocolExtractorsSection action={action} index={index} protocol="network" onAddExtractor={onAddExtractor} onRemoveExtractor={onRemoveExtractor} onUpdateExtractor={onUpdateExtractor} />
      <ProtocolAssertionsSection action={action} index={index} protocol="network" onAddAssertion={onAddAssertion} onRemoveAssertion={onRemoveAssertion} onUpdateAssertion={onUpdateAssertion} />
    </>
  )
}

export interface JmsActionFieldsProps {
  action: Action
  index: number
  protocol: string
  onAddAssertion: (i: number) => void
  onAddExtractor: (i: number) => void
  onAddHeader: (i: number) => void
  onAddMetadata: (i: number) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onRemoveHeader: (ai: number, k: string) => void
  onRemoveMetadata: (ai: number, k: string) => void
  onSelectionMenu: (event: ReactMouseEvent<HTMLTextAreaElement>) => void
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
  onUpdateHeader: (ai: number, ok: string, nk: string, v: string) => void
  onUpdateMetadata: (ai: number, ok: string, nk: string, v: string) => void
}

export function JmsActionFields({
  action,
  index,
  protocol,
  onAddAssertion,
  onAddExtractor,
  onAddHeader,
  onAddMetadata,
  onRemoveAssertion,
  onRemoveExtractor,
  onRemoveHeader,
  onRemoveMetadata,
  onSelectionMenu,
  onUpdate,
  onUpdateAssertion,
  onUpdateExtractor,
  onUpdateHeader,
  onUpdateMetadata,
}: JmsActionFieldsProps) {
  const params = action.params || {}

  return (
    <>
      <div className="sub-section">
        <div className="sub-header">
          <h4>Message</h4>
          {(action.type === 'jms_publish' || action.type === 'jms_request_reply') && <ActionBodyWrapToggle />}
        </div>
        <div className="form-grid">
          {(action.type === 'jms_publish' || action.type === 'jms_request_reply') && (
            <label className="full-width">
              <span>Body</span>
              <ActionBodyTextarea
                onChange={value => onUpdate(index, 'body', value)}
                onContextMenu={onSelectionMenu}
                placeholder='{"orderId": "{{data.orders.id}}"}'
                rows={5}
                value={action.body || ''}
              />
            </label>
          )}
          <label>
            <span>Correlation ID</span>
            <input
              value={typeof params.correlation_id === 'string' ? params.correlation_id : ''}
              onChange={e => onUpdate(index, 'params', { ...params, correlation_id: e.target.value })}
              placeholder="{{var.jms_message_id}} or {{fn.uuid()}}"
            />
          </label>
          {(action.type === 'jms_request_reply' || action.type === 'jms_publish' || action.type === 'jms_consume') && (
            <label>
              <span>Timeout (ms)</span>
              <input
                type="number"
                min="0"
                value={typeof params.timeout_ms === 'number' ? params.timeout_ms : ''}
                onChange={e => onUpdate(index, 'params', { ...params, timeout_ms: Number(e.target.value) || undefined })}
                placeholder="Use script default"
              />
            </label>
          )}
          {action.type === 'jms_request_reply' && (
            <label className="full-width">
              <span>Reply To Override</span>
              <input
                value={typeof params.reply_to === 'string' ? params.reply_to : ''}
                onChange={e => onUpdate(index, 'params', { ...params, reply_to: e.target.value })}
                placeholder="orders.reply"
              />
            </label>
          )}
        </div>
      </div>

      <div className="sub-section">
        <div className="sub-header"><h4>Message Headers</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddHeader(index)}>+ Add</button></div>
        {Object.entries(action.headers || {}).map(([key, value]) => (
          <div key={key} className="kv-row">
            <input className="kv-key" value={key} onChange={e => onUpdateHeader(index, key, e.target.value, value)} placeholder="Header" />
            <input className="kv-val" value={value} onChange={e => onUpdateHeader(index, key, key, e.target.value)} placeholder="Value" />
            <button className="btn-icon danger" onClick={() => onRemoveHeader(index, key)}>✕</button>
          </div>
        ))}
      </div>

      <div className="sub-section">
        <div className="sub-header"><h4>Message Properties</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddMetadata(index)}>+ Add</button></div>
        {Object.entries(action.metadata || {}).map(([key, value]) => (
          <div key={key} className="kv-row">
            <input className="kv-key" value={key} onChange={e => onUpdateMetadata(index, key, e.target.value, String(value || ''))} placeholder="Property" />
            <input className="kv-val" value={String(value || '')} onChange={e => onUpdateMetadata(index, key, key, e.target.value)} placeholder="Value" />
            <button className="btn-icon danger" onClick={() => onRemoveMetadata(index, key)}>✕</button>
          </div>
        ))}
      </div>

      <ProtocolExtractorsSection
        action={action}
        index={index}
        protocol={protocol}
        onAddExtractor={onAddExtractor}
        onRemoveExtractor={onRemoveExtractor}
        onUpdateExtractor={onUpdateExtractor}
      />

      <ProtocolAssertionsSection
        action={action}
        index={index}
        protocol={protocol}
        onAddAssertion={onAddAssertion}
        onRemoveAssertion={onRemoveAssertion}
        onUpdateAssertion={onUpdateAssertion}
      />
    </>
  )
}

export interface HttpActionFieldsProps {
  protocol: string
  scriptHTTPVersion?: string
  action: Action
  index: number
  isBrowserScript: boolean
  isSoapScript: boolean
  onAddAssertion: (i: number) => void
  onAddExtractor: (i: number) => void
  onAddHeader: (i: number) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onRemoveHeader: (ai: number, k: string) => void
  onSelectionMenu: (event: ReactMouseEvent<HTMLTextAreaElement>) => void
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
  onUpdateHeader: (ai: number, ok: string, nk: string, v: string) => void
}

export function HttpActionFields({
  protocol,
  scriptHTTPVersion,
  action,
  index,
  isBrowserScript,
  isSoapScript,
  onAddAssertion,
  onAddExtractor,
  onAddHeader,
  onRemoveAssertion,
  onRemoveExtractor,
  onRemoveHeader,
  onSelectionMenu,
  onUpdate,
  onUpdateAssertion,
  onUpdateExtractor,
  onUpdateHeader,
}: HttpActionFieldsProps) {
  const openBody = useContext(OpenActionBody)
  const [wrapLines, setWrapLines] = useCodeLineWrapPreference()
  const method = (action.method || action.type || '').trim().toUpperCase()
  const bodyVisible = action.body !== undefined
    || action.body_encoding !== undefined
    || ['POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS'].includes(method)
  const bodyEncoding = action.body_encoding || ''
  const knownEncoding = knownBodyEncoding(bodyEncoding)
  const bodyEncodingKnown = Boolean(knownEncoding)
  const bodyEncodingSelection = knownEncoding ? knownEncoding.value : bodyEncoding
  const formattedXmlBody = useMemo(
    () => wrapLines || bodyEncodingSelection === 'base64' || bodyEncodingSelection === 'c_escaped' ? null : formatXMLForDisplay(action.body || ''),
    [action.body, bodyEncodingSelection, wrapLines],
  )
  const showFormattedXmlBody = !wrapLines && Boolean(formattedXmlBody)

  return (
    <>
      <HTTPActionVersionFields action={action} index={index} protocol={protocol} scriptVersion={scriptHTTPVersion} onUpdate={onUpdate} />
      {isSoapScript && (
        <SoapActionFields action={action} index={index} onUpdate={onUpdate} />
      )}

      {!isBrowserScript && (
        <ProtocolHeadersSection
          action={action}
          index={index}
          onAddHeader={onAddHeader}
          onRemoveHeader={onRemoveHeader}
          onUpdate={onUpdate}
          onUpdateHeader={onUpdateHeader}
        />
      )}

      {!isBrowserScript && bodyVisible && openBody && <div className="sub-section"><h4>Request body</h4><p>{(action.body || '').length.toLocaleString()} characters · {action.body_encoding ? bodyEncodingLabel(action.body_encoding) : bodyLanguage(action).toUpperCase()}</p><button type="button" className="btn btn-secondary" onClick={openBody}>Edit request body in center</button></div>}
      {!isBrowserScript && bodyVisible && !openBody && (
        <div className="sub-section">
          <div className="sub-header">
            <h4>Body</h4>
            <CodeLineWrapToggle wrapLines={wrapLines} onChange={setWrapLines} />
          </div>
          <div className="form-grid">
            <label>
              <span>Body Representation</span>
              <select
                value={bodyEncodingSelection}
                onChange={event => onUpdate(index, 'body_encoding', event.target.value || undefined)}
              >
                {BODY_ENCODING_OPTIONS.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
                {!bodyEncodingKnown && <option value={bodyEncoding}>Legacy/unknown: {bodyEncoding}</option>}
              </select>
            </label>
          </div>
          {knownEncoding?.note && (
            <p className="sequence-summary-note">{knownEncoding.note}</p>
          )}
          {!bodyEncodingKnown && (
            <p className="sequence-summary-note">
              The runtime does not decode this representation and sends the body text as authored. Select an explicit representation to remove ambiguity.
            </p>
          )}
          {showFormattedXmlBody ? (
            <>
              <p className="body-xml-display-note" role="status">
                Formatted XML preview only. The stored request body and executable DSL payload are unchanged.
              </p>
              <div className="body-xml-formatted-view">
                <SourceCodeViewer
                  ariaLabel="Formatted XML body preview"
                  sourcePath="request-body.xml"
                  value={formattedXmlBody || ''}
                />
              </div>
              <details className="body-xml-raw-editor">
                <summary>Edit raw XML payload</summary>
                <p>Changes here update the exact request body sent by the Go engine.</p>
                <ActionBodyTextarea
                  onChange={value => onUpdate(index, 'body', value)}
                  onContextMenu={onSelectionMenu}
                  placeholder='<root />'
                  rows={8}
                  value={action.body || ''}
                />
              </details>
            </>
          ) : (
            <ActionBodyTextarea
              onChange={value => onUpdate(index, 'body', value)}
              onContextMenu={onSelectionMenu}
              placeholder='{"key": "value"}'
              rows={4}
              value={action.body || ''}
            />
          )}
        </div>
      )}

      {!isBrowserScript && (
        <ProtocolExtractorsSection
          action={action}
          index={index}
          protocol={isSoapScript ? 'soap' : 'http'}
          onAddExtractor={onAddExtractor}
          onRemoveExtractor={onRemoveExtractor}
          onUpdateExtractor={onUpdateExtractor}
        />
      )}

      {(
        <ProtocolAssertionsSection
          action={action}
          index={index}
          protocol={isBrowserScript ? 'browser' : isSoapScript ? 'soap' : protocol}
          onAddAssertion={onAddAssertion}
          onRemoveAssertion={onRemoveAssertion}
          onUpdateAssertion={onUpdateAssertion}
        />
      )}
    </>
  )
}

export interface SoapActionFieldsProps {
  action: Action
  index: number
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
}

export function SoapActionFields({ action, index, onUpdate }: SoapActionFieldsProps) {
  return (
    <div className="sub-section">
      <div className="sub-header"><h4>SOAP Action</h4></div>
      <div className="form-grid">
        <label>
          <span>SOAPAction</span>
          <input value={action.soap?.action || ''} onChange={e => onUpdate(index, 'soap', { ...(action.soap || {}), action: e.target.value })} placeholder="urn:CreateOrder" />
        </label>
        <label>
          <span>Operation</span>
          <input value={action.soap?.operation || ''} onChange={e => onUpdate(index, 'soap', { ...(action.soap || {}), operation: e.target.value })} placeholder="CreateOrder" />
        </label>
        <label>
          <span>Body Mode</span>
          <select value={action.soap?.body_mode || 'raw_xml'} onChange={e => onUpdate(index, 'soap', { ...(action.soap || {}), body_mode: e.target.value })}>
            <option value="raw_xml">raw_xml</option>
            <option value="generated">generated</option>
          </select>
        </label>
      </div>
    </div>
  )
}

export interface ProtocolHeadersSectionProps {
  action: Action
  index: number
  onAddHeader: (i: number) => void
  onRemoveHeader: (ai: number, k: string) => void
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
  onUpdateHeader: (ai: number, ok: string, nk: string, v: string) => void
}

export function ProtocolHeadersSection({ action, index, onAddHeader, onRemoveHeader, onUpdate, onUpdateHeader }: ProtocolHeadersSectionProps) {
  const orderedHeaders = action.header_list
  const usesOrderedHeaders = Array.isArray(orderedHeaders)
    && (orderedHeaders.length > 0 || Object.keys(action.headers || {}).length === 0)
  const orderedHeaderNames = new Set(
    (orderedHeaders || []).map(header => header.name.trim().toLowerCase()).filter(Boolean),
  )
  const compatibilityOnlyHeaders = Object.fromEntries(
    Object.entries(action.headers || {})
      .filter(([name]) => !orderedHeaderNames.has(name.trim().toLowerCase())),
  )
  const updateOrderedHeaders = (next: NonNullable<Action['header_list']>) => {
    onUpdate(index, 'header_list', next)
    onUpdate(index, 'headers', headerListToCompatibilityMap(next, compatibilityOnlyHeaders))
  }
  const addHeader = () => {
    if (usesOrderedHeaders && orderedHeaders) {
      updateOrderedHeaders([...orderedHeaders, { name: '', value: '' }])
      return
    }
    onAddHeader(index)
  }
  const renameCompatibilityOnlyHeader = (oldName: string, nextName: string, value: string) => {
    if (orderedHeaders && orderedHeaderNames.has(nextName.trim().toLowerCase())) {
      const remainingCompatibilityHeaders = { ...compatibilityOnlyHeaders }
      delete remainingCompatibilityHeaders[oldName]
      const next = [...orderedHeaders, { name: nextName, value }]
      onUpdate(index, 'header_list', next)
      onUpdate(index, 'headers', headerListToCompatibilityMap(next, remainingCompatibilityHeaders))
      return
    }
    onUpdateHeader(index, oldName, nextName, value)
  }

  return (
    <div className="sub-section">
      <div className="sub-header"><h4>Headers</h4><button type="button" className="btn btn-sm btn-ghost" onClick={addHeader}>+ Add</button></div>
      {usesOrderedHeaders && orderedHeaders
        ? <>
          {orderedHeaders.map((header, headerIndex) => (
            <div key={headerIndex} className="kv-row">
              <input
                aria-label={`Header ${headerIndex + 1} name`}
                className="kv-key"
                value={header.name}
                onChange={event => updateOrderedHeaders(orderedHeaders.map((entry, entryIndex) => (
                  entryIndex === headerIndex ? { ...entry, name: event.target.value } : entry
                )))}
                placeholder="Key"
              />
              <input
                aria-label={`Header ${headerIndex + 1} value`}
                className="kv-val"
                value={header.value}
                onChange={event => updateOrderedHeaders(orderedHeaders.map((entry, entryIndex) => (
                  entryIndex === headerIndex ? { ...entry, value: event.target.value } : entry
                )))}
                placeholder="Value"
              />
              <button
                type="button"
                aria-label={`Remove header ${headerIndex + 1}`}
                className="btn-icon danger"
                onClick={() => updateOrderedHeaders(orderedHeaders.filter((_, entryIndex) => entryIndex !== headerIndex))}
              >
                ✕
              </button>
            </div>
          ))}
          {Object.entries(compatibilityOnlyHeaders).map(([key, value]) => (
            <div key={`compatibility-${key}`} className="kv-row">
              <input aria-label={`Header ${key} name`} className="kv-key" value={key} onChange={e => renameCompatibilityOnlyHeader(key, e.target.value, value)} placeholder="Key" />
              <input aria-label={`Header ${key} value`} className="kv-val" value={value} onChange={e => onUpdateHeader(index, key, key, e.target.value)} placeholder="Value" />
              <button type="button" aria-label={`Remove header ${key}`} className="btn-icon danger" onClick={() => onRemoveHeader(index, key)}>✕</button>
            </div>
          ))}
        </>
        : Object.entries(action.headers || {}).map(([key, value]) => (
            <div key={key} className="kv-row">
              <input aria-label={`Header ${key} name`} className="kv-key" value={key} onChange={e => onUpdateHeader(index, key, e.target.value, value)} placeholder="Key" />
              <input aria-label={`Header ${key} value`} className="kv-val" value={value} onChange={e => onUpdateHeader(index, key, key, e.target.value)} placeholder="Value" />
              <button type="button" aria-label={`Remove header ${key}`} className="btn-icon danger" onClick={() => onRemoveHeader(index, key)}>✕</button>
            </div>
          ))}
    </div>
  )
}

export interface ProtocolExtractorsSectionProps {
  action: Action
  index: number
  protocol: string
  onAddExtractor: (i: number) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
}

function extractorSourceOptions(protocol: string): Array<[string, string]> {
  switch (protocol) {
    case 'jms-solace':
    case 'jms-servicebus':
    case 'jms-ibmmq':
      return [['body', 'Body'], ['header', 'Header'], ['property', 'Property'], ['message_id', 'Message ID'], ['correlation_id', 'Correlation ID']]
    case 'database':
      return [['body', 'Result Body'], ['rows', 'Rows'], ['scalar', 'Scalar'], ['row_count', 'Row Count'], ['rows_affected', 'Rows Affected'], ['last_insert_id', 'Last Insert ID']]
    case 'network':
      return [['body', 'Body'], ['response', 'Response'], ['records', 'Records'], ['record_count', 'Record Count'], ['entry_count', 'Entry Count'], ['target', 'Target']]
    case 'grpc':
      return [['body', 'Message Body'], ['metadata', 'Metadata'], ['trailer', 'Trailer'], ['status', 'Status']]
    case 'websocket':
      return [['body', 'Message Body'], ['header', 'Handshake Header'], ['message', 'Message'], ['close_code', 'Close Code']]
    default:
      return [['body', 'Body'], ['header', 'Header'], ['all', 'Headers and body'], ['cookie', 'Cookie']]
  }
}

function extractorTypeOptions(protocol: string): Array<[string, string]> {
  if (protocol === 'database' || protocol === 'network' || protocol === 'grpc') {
    return [['jsonpath', 'JSONPath'], ['jmespath', 'JMESPath'], ['regex', 'Regex'], ['boundary', 'Boundary']]
  }
  if (protocol.startsWith('jms') || protocol === 'websocket') {
    return [['jsonpath', 'JSONPath'], ['regex', 'Regex'], ['boundary', 'Boundary']]
  }
  // HTTP family: include SSE modes for consuming text/event-stream responses
  // (Agentforce streaming) — sse_concat joins data: frames, sse_event picks a named event.
  return [['jsonpath', 'JSONPath'], ['jmespath', 'JMESPath'], ['regex', 'Regex'], ['boundary', 'Boundary'], ['xpath', 'XPath'], ['css', 'CSS'], ['sse_concat', 'SSE concat'], ['sse_event', 'SSE event']]
}

function extractorExpressionPlaceholder(protocol: string, source: string) {
  if (protocol === 'database') return source === 'scalar' ? '$.scalar' : '$.rows[0].id'
  if (protocol === 'network') return source === 'record_count' ? '$.record_count' : '$.response'
  if (protocol === 'grpc') return '$.id'
  if (protocol === 'websocket') return '$.type'
  if (protocol.startsWith('jms')) return '$.orderId'
  return '$.data.id'
}

export function ProtocolExtractorsSection({ action, index, protocol, onAddExtractor, onRemoveExtractor, onUpdateExtractor }: ProtocolExtractorsSectionProps) {
  const managed = useContext(ManagedActionChecks)
  const sourceOptions = extractorSourceOptions(protocol)
  const typeOptions = extractorTypeOptions(protocol)
  if (managed) return null
  return (
    <div className="sub-section">
      <div className="sub-header"><h4>Extractors</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddExtractor(index)}>+ Add</button></div>
      {(action.extractors || []).map((extractor, extractorIndex) => (
        <div key={extractorIndex} className="extractor-row">
          <input aria-label={`Extractor ${extractorIndex + 1} variable name`} value={extractor.name} onChange={e => onUpdateExtractor(index, extractorIndex, 'name', e.target.value)} placeholder="Variable name" />
          <select aria-label={`Extractor ${extractorIndex + 1} source`} value={extractor.source} onChange={e => onUpdateExtractor(index, extractorIndex, 'source', e.target.value)}>
            {sourceOptions.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
          <select aria-label={`Extractor ${extractorIndex + 1} type`} value={extractor.type} onChange={e => onUpdateExtractor(index, extractorIndex, 'type', e.target.value)}>
            {typeOptions.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
          <input aria-label={`Extractor ${extractorIndex + 1} expression`} value={extractor.expression} onChange={e => onUpdateExtractor(index, extractorIndex, 'expression', e.target.value)} placeholder={extractorExpressionPlaceholder(protocol, extractor.source)} className="ext-expr" />
          <button aria-label={`Remove extractor ${extractorIndex + 1}`} className="btn-icon danger" onClick={() => onRemoveExtractor(index, extractorIndex)}>✕</button>
        </div>
      ))}
    </div>
  )
}

export interface ProtocolAssertionsSectionProps {
  action: Action
  index: number
  protocol: string
  onAddAssertion: (i: number) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
}

function assertionOptions(protocol: string): Array<[string, string]> {
  switch (protocol) {
    case 'browser':
      return [['element_text_contains', 'Element text contains'], ['element_text_equals', 'Element text equals'], ['element_visible', 'Element visible'], ['page_title', 'Page title'], ['url_contains', 'URL contains'], ['body_contains', 'Page HTML contains'], ['body_not_contains', 'Page HTML excludes'], ['response_time_ms', 'Response time (ms)']]
    case 'jms-solace':
    case 'jms-servicebus':
    case 'jms-ibmmq':
      return [['message_received', 'Message Received'], ['body_contains', 'Body Contains'], ['property_equals', 'Property Equals'], ['header_equals', 'Header Equals'], ['correlation_match', 'Correlation Match'], ['response_time_ms', 'Response Time (ms)']]
    case 'websocket':
      return [['message_received', 'Message Received'], ['body_contains', 'Message Contains'], ['close_code', 'Close Code'], ['response_time_ms', 'Response Time (ms)']]
    case 'grpc':
      return [['grpc_status', 'gRPC Status'], ['message_count', 'Message Count'], ['body_contains', 'Body Contains'], ['metadata_contains', 'Metadata Contains'], ['response_time_ms', 'Response Time (ms)']]
    case 'database':
      return [['row_count', 'Row Count'], ['rows_affected', 'Rows Affected'], ['scalar_equals', 'Scalar Equals'], ['body_contains', 'Result Contains'], ['body_not_contains', 'Result Excludes'], ['response_time_ms', 'Response Time (ms)']]
    case 'network':
      return [['record_count', 'Record Count'], ['entry_count', 'Entry Count'], ['messages_received', 'Messages Received'], ['body_contains', 'Transcript Contains'], ['body_not_contains', 'Transcript Excludes'], ['status_code', 'Status Code'], ['response_time_ms', 'Response Time (ms)']]
    case 'http':
    case 'http/1.1':
    case 'http/2':
      return [['status_code', 'Status Code'], ['response_time_ms', 'Response Time (ms)'], ['body_contains', 'Body Contains'], ['header_contains', 'Header Contains'], ['body_regex_capture_equals', 'Body Regex Capture Equals'], ['aura_actions_success', 'Aura actions succeed']]
    default:
      return [['status_code', 'Status Code'], ['response_time_ms', 'Response Time (ms)'], ['body_contains', 'Body Contains'], ['header_contains', 'Header Contains'], ['body_regex_capture_equals', 'Body Regex Capture Equals']]
  }
}

function assertionUsesNumericExpected(type: string) {
  return ['status_code', 'grpc_status', 'row_count', 'rows_affected', 'record_count', 'entry_count', 'message_count', 'messages_received', 'close_code'].includes(type)
}

function assertionUsesTextValue(type: string) {
  return ['body_contains', 'body_not_contains', 'metadata_contains', 'correlation_match', 'element_visible', 'url_contains'].includes(type)
}

function assertionUsesNamedExpected(type: string) {
  return ['property_equals', 'header_equals', 'scalar_equals', 'header_contains', 'body_regex_capture_equals', 'element_text_contains', 'element_text_equals'].includes(type)
}

function assertionValuePlaceholder(type: string) {
  if (type.startsWith('element_text_')) return 'Element selector'
  if (type === 'header_contains') return 'Header name'
  if (type === 'body_regex_capture_equals') return 'RE2 pattern'
  return 'Name'
}

export function ProtocolAssertionsSection({ action, index, protocol, onAddAssertion, onRemoveAssertion, onUpdateAssertion }: ProtocolAssertionsSectionProps) {
  const managed = useContext(ManagedActionChecks)
  if (managed) return null
  const options = assertionOptions(protocol)
  return (
    <div className="sub-section" data-action-section="assertions">
      <div className="sub-header"><h4>Assertions</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddAssertion(index)}>+ Add</button></div>
      {(action.assertions || []).map((assertion, assertionIndex) => (
        <div key={assertionIndex} className="assertion-row">
          <select aria-label={`Assertion ${assertionIndex + 1} type`} value={assertion.type} onChange={e => {
            const type = e.target.value
            onUpdateAssertion(index, assertionIndex, 'type', type)
            if (type === 'aura_actions_success') {
              for (const field of ['value', 'expected', 'min', 'max', 'legacy_html_check'] as const) {
                if (assertion[field] !== undefined) onUpdateAssertion(index, assertionIndex, field, undefined)
              }
            }
          }}>
            {!options.some(([value]) => value === assertion.type) && <option value={assertion.type}>{assertion.type} (preserved)</option>}
            {options.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
          {assertion.type === 'page_title' && <input aria-label={`Assertion ${assertionIndex + 1} expected title`} value={typeof assertion.value === 'string' ? assertion.value : String(assertion.expected ?? '')} onChange={e => onUpdateAssertion(index, assertionIndex, typeof assertion.value === 'string' ? 'value' : 'expected', e.target.value)} placeholder="Expected title" />}
          {assertionUsesNumericExpected(assertion.type) && <input aria-label={`Assertion ${assertionIndex + 1} expected number`} type="number" value={typeof assertion.expected === 'number' ? assertion.expected : 0} onChange={e => onUpdateAssertion(index, assertionIndex, 'expected', Number(e.target.value))} />}
          {assertion.type === 'response_time_ms' && <input aria-label={`Assertion ${assertionIndex + 1} maximum milliseconds`} type="number" min="1" value={assertion.max ?? 5000} onChange={e => onUpdateAssertion(index, assertionIndex, 'max', Number(e.target.value))} placeholder="Max ms" />}
          {assertionUsesTextValue(assertion.type) && <input aria-label={`Assertion ${assertionIndex + 1} ${assertion.type.startsWith('element_') ? 'selector' : 'value'}`} value={assertion.value ?? ''} onChange={e => onUpdateAssertion(index, assertionIndex, 'value', e.target.value)} placeholder={assertion.type === 'element_visible' ? 'Element selector' : 'Expected text'} />}
          {assertionUsesNamedExpected(assertion.type) && assertion.type !== 'scalar_equals' && <input aria-label={`Assertion ${assertionIndex + 1} ${assertion.type.startsWith('element_') ? 'selector' : 'value'}`} value={assertion.value ?? ''} onChange={e => onUpdateAssertion(index, assertionIndex, 'value', e.target.value)} placeholder={assertionValuePlaceholder(assertion.type)} />}
          {assertionUsesNamedExpected(assertion.type) && <input aria-label={`Assertion ${assertionIndex + 1} expected value`} value={typeof assertion.expected === 'string' || typeof assertion.expected === 'number' ? String(assertion.expected) : ''} onChange={e => onUpdateAssertion(index, assertionIndex, 'expected', e.target.value)} placeholder="Expected value" />}
          <button aria-label={`Remove assertion ${assertionIndex + 1}`} className="btn-icon danger" onClick={() => onRemoveAssertion(index, assertionIndex)}>✕</button>
        </div>
      ))}
    </div>
  )
}
