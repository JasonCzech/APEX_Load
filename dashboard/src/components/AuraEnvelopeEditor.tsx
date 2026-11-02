import { useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { AuraEnvelopeDraftContext } from './AuraEnvelopeEditorDrafts'
import {
  applyAuraEnvelopeDrafts,
  EMPTY_AURA_ENVELOPE_DRAFTS,
  formatAuraActionTitle,
  hasAuraEnvelopeDrafts,
  parseAuraFormEnvelope,
  parseAuraResponseEnvelope,
  restoreUnquotedTemplates,
  serializeAuraFormEnvelope,
  type AuraEnvelopeDrafts,
  type ParsedAuraEnvelope,
} from './auraEnvelopeModel'
import './AuraEnvelopeEditor.css'

export function AuraEnvelopeEditor({
  envelope,
  onChangeBody,
}: {
  envelope: ParsedAuraEnvelope
  onChangeBody: (nextBody: string) => void
}) {
  // Inside the script editor the drafts live in its registry, so saving,
  // replaying, leaving, or selecting another step never drops them silently.
  const owner = useContext(AuraEnvelopeDraftContext)
  const controlled = owner !== null
  const [localDrafts, setLocalDrafts] = useState<AuraEnvelopeDrafts>(EMPTY_AURA_ENVELOPE_DRAFTS)
  const drafts = owner ? owner.drafts : localDrafts
  const commitDrafts = (next: AuraEnvelopeDrafts, baseBody?: string) => {
    if (owner) owner.change(next, baseBody)
    else setLocalDrafts(next)
  }
  const paramDrafts = drafts.params
  const [paramErrors, setParamErrors] = useState<Record<number, string>>({})
  const [statusMessage, setStatusMessage] = useState('')

  const formattedParamsByIndex = useMemo(() => (
    envelope.actions.map(sub => restoreUnquotedTemplates(
      JSON.stringify(sub.params ?? {}, null, 2),
      envelope.unquotedTemplates,
    ))
  ), [envelope.actions, envelope.unquotedTemplates])

  useEffect(() => {
    // An owned draft set is checked against the body it was typed on instead.
    if (!controlled) setLocalDrafts(EMPTY_AURA_ENVELOPE_DRAFTS)
    setParamErrors({})
  }, [envelope.actions.length, controlled])

  // The whole decoded aura.context (loaded, dn, globals, ...), as the
  // sub-actions' parameters are shown: templates as written.
  const formattedContext = useMemo(() => (
    envelope.contextObject
      ? restoreUnquotedTemplates(JSON.stringify(envelope.contextObject, null, 2), envelope.unquotedTemplates)
      : ''
  ), [envelope.contextObject, envelope.unquotedTemplates])
  const contextDraft = drafts.context
  const [contextError, setContextError] = useState('')
  const contextValue = contextDraft ?? formattedContext
  const contextDirty = contextDraft !== null && contextDraft !== formattedContext

  const withoutParam = (actionIdx: number): AuraEnvelopeDrafts => {
    const params = { ...drafts.params }
    delete params[actionIdx]
    return { ...drafts, params }
  }

  const setParamDraft = (actionIdx: number, text: string) => {
    // Text equal to the body's own is no edit.
    if (text === formattedParamsByIndex[actionIdx]) commitDrafts(withoutParam(actionIdx))
    else commitDrafts({ ...drafts, params: { ...drafts.params, [actionIdx]: text } })
  }

  const setContextDraft = (text: string | null) => {
    commitDrafts({ ...drafts, context: text === null || text === formattedContext ? null : text })
  }

  const applyContext = () => {
    if (contextDraft === null) return
    const result = applyAuraEnvelopeDrafts(envelope, { params: {}, context: contextDraft })
    if (!('body' in result)) {
      setContextError(result.error)
      setStatusMessage('')
      return
    }
    onChangeBody(result.body)
    commitDrafts({ ...drafts, context: null }, result.body)
    setContextError('')
    setStatusMessage('Applied changes to the Aura context.')
  }

  const contextMode = typeof envelope.contextObject?.mode === 'string' ? envelope.contextObject.mode : ''
  const contextFwuid = typeof envelope.contextObject?.fwuid === 'string' ? envelope.contextObject.fwuid : ''
  const contextApp = typeof envelope.contextObject?.app === 'string' ? envelope.contextObject.app : ''

  const updateMetaField = (patch: Partial<Pick<ParsedAuraEnvelope, 'token' | 'pageURI'>> & { fwuid?: string }) => {
    let nextContextObject = envelope.contextObject
    if (patch.fwuid !== undefined && envelope.contextObject) {
      nextContextObject = { ...envelope.contextObject, fwuid: patch.fwuid }
    }
    const nextEnvelope: ParsedAuraEnvelope = {
      ...envelope,
      ...(patch.token !== undefined ? { token: patch.token } : {}),
      ...(patch.pageURI !== undefined ? { pageURI: patch.pageURI } : {}),
      contextObject: nextContextObject,
    }
    const nextBody = serializeAuraFormEnvelope(nextEnvelope)
    onChangeBody(nextBody)
    // Pending drafts still apply to the body this field edit wrote.
    if (hasAuraEnvelopeDrafts(drafts)) commitDrafts(drafts, nextBody)
    setStatusMessage('')
  }

  const applySubActionParams = (actionIdx: number) => {
    const rawDraft = paramDrafts[actionIdx] ?? formattedParamsByIndex[actionIdx] ?? '{}'
    const result = applyAuraEnvelopeDrafts(envelope, { params: { [actionIdx]: rawDraft }, context: null })
    if (!('body' in result)) {
      setParamErrors(prev => ({ ...prev, [actionIdx]: result.error }))
      setStatusMessage('')
      return
    }
    onChangeBody(result.body)
    commitDrafts(withoutParam(actionIdx), result.body)
    setParamErrors(prev => {
      const copy = { ...prev }
      delete copy[actionIdx]
      return copy
    })
    setStatusMessage(`Applied parameter changes to ${formatAuraActionTitle(envelope.actions[actionIdx], actionIdx)}.`)
  }

  const resetSubActionParams = (actionIdx: number) => {
    commitDrafts(withoutParam(actionIdx))
    setParamErrors(prev => {
      const copy = { ...prev }
      delete copy[actionIdx]
      return copy
    })
    setStatusMessage('')
  }

  if (owner?.stale) {
    // The drafts index sub-actions of a body that has since changed, or of a
    // step another layout may have moved: showing them here could put one
    // sub-action's edit on another.
    return (
      <div className="aura-envelope-editor" role="region" aria-label="Aura envelope">
        <p className="aura-envelope-error" role="alert">
          {owner.staleMessage}{' '}
          <button type="button" className="btn btn-sm btn-secondary" onClick={() => { commitDrafts(EMPTY_AURA_ENVELOPE_DRAFTS); setParamErrors({}); setContextError('') }}>
            Discard Aura edits
          </button>
        </p>
      </div>
    )
  }

  return (
    <div className="aura-envelope-editor" role="region" aria-label="Aura envelope">
      <div className="aura-envelope-meta-grid">
        <label className="aura-envelope-field">
          <span>Aura token</span>
          <input
            type="text"
            aria-label="Aura token"
            value={envelope.token}
            onChange={event => updateMetaField({ token: event.target.value })}
            placeholder="{{var.aura_token}}"
          />
        </label>
        <label className="aura-envelope-field">
          <span>Page URI</span>
          <input
            type="text"
            aria-label="Page URI"
            value={envelope.pageURI}
            onChange={event => updateMetaField({ pageURI: event.target.value })}
            placeholder="/lightning/page/home"
          />
        </label>
        {envelope.contextObject && (
          <label className="aura-envelope-field">
            <span>Framework UID (fwuid)</span>
            <input
              type="text"
              aria-label="Framework UID (fwuid)"
              value={contextFwuid}
              // An unapplied context draft would write its own fwuid back.
              disabled={contextDirty}
              title={contextDirty ? 'Apply or reset the Aura context JSON first' : undefined}
              onChange={event => updateMetaField({ fwuid: event.target.value })}
              placeholder="{{var.aura_fwuid}}"
            />
          </label>
        )}
      </div>

      {envelope.contextObject && (
        <div className="aura-envelope-context-card">
          <div className="aura-envelope-context-summary">
            <span><strong>Aura context</strong></span>
            {contextMode && <span>Mode: <code>{contextMode}</code></span>}
            {contextApp && <span>App: <code>{contextApp}</code></span>}
          </div>
          <label className="aura-envelope-field">
            <span>Context (JSON)</span>
            <textarea
              className="aura-envelope-params-textarea"
              aria-label="Aura context (JSON)"
              aria-invalid={contextError ? true : undefined}
              value={contextValue}
              onChange={event => {
                setContextDraft(event.target.value)
                if (contextError) setContextError('')
              }}
            />
          </label>
          {contextError && <p className="aura-envelope-error" role="alert">{contextError}</p>}
          <div className="aura-envelope-action-controls">
            <button type="button" className="btn btn-sm btn-secondary" disabled={!contextDirty} onClick={applyContext}>
              Apply context
            </button>
            {contextDirty && (
              <button type="button" className="btn btn-sm btn-secondary" onClick={() => { setContextDraft(null); setContextError(''); setStatusMessage('') }}>
                Reset
              </button>
            )}
          </div>
        </div>
      )}

      <div className="aura-envelope-actions-list" aria-label="Aura actions">
        {envelope.actions.map((subAction, idx) => {
          const title = formatAuraActionTitle(subAction, idx)
          const draftValue = paramDrafts[idx] ?? formattedParamsByIndex[idx] ?? '{}'
          const isDirty = paramDrafts[idx] !== undefined && paramDrafts[idx] !== formattedParamsByIndex[idx]
          const errorText = paramErrors[idx]
          const calling = typeof subAction.callingDescriptor === 'string' && subAction.callingDescriptor !== 'UNKNOWN'
            ? subAction.callingDescriptor
            : ''
          return (
            <article key={`${subAction.id || idx}-${idx}`} className="aura-envelope-action-card">
              <div className="aura-envelope-action-header">
                <span className="aura-envelope-action-title">{idx + 1}. {title}</span>
                {subAction.id && <span className="aura-envelope-action-id">Action ID {subAction.id}</span>}
              </div>
              <div className="aura-envelope-action-meta">
                {subAction.descriptor && <span>Descriptor: <code>{subAction.descriptor}</code></span>}
                {calling && <span>Caller: <code>{calling}</code></span>}
              </div>
              <label className="aura-envelope-field">
                <span>Parameters (JSON)</span>
                <textarea
                  className="aura-envelope-params-textarea"
                  aria-label={`Parameters (JSON) for ${title}`}
                  aria-invalid={errorText ? true : undefined}
                  value={draftValue}
                  onChange={event => {
                    setParamDraft(idx, event.target.value)
                    if (paramErrors[idx]) {
                      setParamErrors(prev => {
                        const copy = { ...prev }
                        delete copy[idx]
                        return copy
                      })
                    }
                  }}
                />
              </label>
              {errorText && <p className="aura-envelope-error" role="alert">{errorText}</p>}
              <div className="aura-envelope-action-controls">
                <button
                  type="button"
                  className="btn btn-sm btn-secondary"
                  disabled={!isDirty}
                  onClick={() => applySubActionParams(idx)}
                >
                  Apply parameters
                </button>
                {isDirty && (
                  <button
                    type="button"
                    className="btn btn-sm btn-secondary"
                    onClick={() => resetSubActionParams(idx)}
                  >
                    Reset
                  </button>
                )}
              </div>
            </article>
          )
        })}
      </div>

      {statusMessage && <p className="aura-envelope-status" role="status">{statusMessage}</p>}
    </div>
  )
}

export function AuraEnvelopePreview({ body }: { body?: string }) {
  const envelope = useMemo(() => parseAuraFormEnvelope(body), [body])
  if (!envelope) return null
  return (
    <div className="aura-envelope-preview" role="region" aria-label="Decoded Aura request envelope">
      <div className="aura-envelope-preview-heading">
        <span>Decoded Aura envelope ({envelope.actions.length} {envelope.actions.length === 1 ? 'action' : 'actions'})</span>
        {envelope.pageURI && <code>{envelope.pageURI}</code>}
      </div>
      <ul className="aura-envelope-preview-list">
        {envelope.actions.map((sub, idx) => (
          <li key={`${sub.id || idx}-${idx}`} className="aura-envelope-preview-item">
            <div className="aura-envelope-preview-row">
              <strong>{idx + 1}. {formatAuraActionTitle(sub, idx)}</strong>
              {sub.id && <code>{sub.id}</code>}
            </div>
            {sub.descriptor && <code>{sub.descriptor}</code>}
          </li>
        ))}
      </ul>
    </div>
  )
}

/**
 * A sent /aura request body in Debug, as the Author body workspace shows one:
 * an "Aura envelope" tab with each sub-action's decoded parameters and a "Raw
 * form body" tab (children). The body is replay evidence with runtime values
 * resolved, so it is read-only here: writing it back would bake those values
 * (credentials among them) into the script. Edit the authored body in Author.
 */
export function AuraRequestBodyTabs({ body, children }: { body?: string; children: ReactNode }) {
  const envelope = useMemo(() => parseAuraFormEnvelope(body), [body])
  const [mode, setMode] = useState<'aura' | 'raw'>('aura')
  if (!envelope) return <>{children}</>
  return (
    <>
      <div className="aura-envelope-mode-bar" role="group" aria-label="Aura body view">
        <div className="aura-envelope-mode-tabs">
          <button className={`btn btn-sm ${mode === 'aura' ? 'btn-primary' : 'btn-secondary'}`} type="button" aria-pressed={mode === 'aura'} onClick={() => setMode('aura')}>
            Aura envelope
          </button>
          <button className={`btn btn-sm ${mode === 'raw' ? 'btn-primary' : 'btn-secondary'}`} type="button" aria-pressed={mode === 'raw'} onClick={() => setMode('raw')}>
            Raw form body
          </button>
        </div>
        <span className="aura-envelope-mode-summary">
          {envelope.actions.length} decoded {envelope.actions.length === 1 ? 'action' : 'actions'} · as sent, read-only (edit the action in Author)
        </span>
      </div>
      {mode === 'aura' ? (
        <div className="aura-envelope-preview" role="region" aria-label="Decoded Aura request envelope">
          <div className="aura-envelope-preview-heading">
            <span>Decoded Aura envelope ({envelope.actions.length} {envelope.actions.length === 1 ? 'action' : 'actions'})</span>
            {envelope.pageURI && <code>{envelope.pageURI}</code>}
          </div>
          <ul className="aura-envelope-preview-list">
            {envelope.actions.map((sub, idx) => (
              <li key={`${sub.id || idx}-${idx}`} className="aura-envelope-preview-item">
                <div className="aura-envelope-preview-row">
                  <strong>{idx + 1}. {formatAuraActionTitle(sub, idx)}</strong>
                  {sub.id && <code>{sub.id}</code>}
                </div>
                {sub.descriptor && <code>{sub.descriptor}</code>}
                <pre aria-label={`Parameters (JSON) for ${formatAuraActionTitle(sub, idx)}`}>
                  {restoreUnquotedTemplates(JSON.stringify(sub.params ?? {}, null, 2), envelope.unquotedTemplates)}
                </pre>
              </li>
            ))}
          </ul>
        </div>
      ) : children}
    </>
  )
}

export function AuraResponsePreview({ body }: { body?: string }) {
  const parsed = useMemo(() => parseAuraResponseEnvelope(body), [body])
  if (!parsed) return null
  return (
    <div className="aura-envelope-preview" role="region" aria-label="Decoded Aura response actions">
      <div className="aura-envelope-preview-heading">
        <span>Decoded Aura response ({parsed.actions.length} {parsed.actions.length === 1 ? 'action' : 'actions'})</span>
        {parsed.hasActionError && <span className="aura-state-badge aura-state-badge--error">Aura action error</span>}
      </div>
      <ul className="aura-envelope-preview-list">
        {parsed.actions.map((item, idx) => {
          const stateMod = item.state === 'SUCCESS'
            ? 'aura-state-badge--success'
            : item.state === 'ERROR'
              ? 'aura-state-badge--error'
              : 'aura-state-badge--incomplete'
          return (
            <li key={`${item.id}-${idx}`} className="aura-envelope-preview-item">
              <div className="aura-envelope-preview-row">
                <span>Action <code>{item.id}</code></span>
                <span className={`aura-state-badge ${stateMod}`}>{item.state}</span>
              </div>
              {item.errorSummary && <span className="aura-envelope-error">{item.errorSummary}</span>}
              {!item.errorSummary && item.returnValuePreview && <code>{item.returnValuePreview}</code>}
            </li>
          )
        })}
      </ul>
    </div>
  )
}
