import { useEffect, useMemo, useState } from 'react'
import {
  formatAuraActionTitle,
  parseAuraFormEnvelope,
  parseAuraResponseEnvelope,
  parseJsonObjectWithTemplates,
  restoreUnquotedTemplates,
  serializeAuraFormEnvelope,
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
  const [paramDrafts, setParamDrafts] = useState<Record<number, string>>({})
  const [paramErrors, setParamErrors] = useState<Record<number, string>>({})
  const [statusMessage, setStatusMessage] = useState('')

  const formattedParamsByIndex = useMemo(() => (
    envelope.actions.map(sub => restoreUnquotedTemplates(
      JSON.stringify(sub.params ?? {}, null, 2),
      envelope.unquotedTemplates,
    ))
  ), [envelope.actions, envelope.unquotedTemplates])

  useEffect(() => {
    setParamDrafts({})
    setParamErrors({})
  }, [envelope.actions.length])

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
    onChangeBody(serializeAuraFormEnvelope(nextEnvelope))
    setStatusMessage('')
  }

  const applySubActionParams = (actionIdx: number) => {
    const rawDraft = paramDrafts[actionIdx] ?? formattedParamsByIndex[actionIdx] ?? '{}'
    const nextUnquoted = new Map(envelope.unquotedTemplates)
    const parsedParams = parseJsonObjectWithTemplates(rawDraft, nextUnquoted)
    if (!parsedParams) {
      setParamErrors(prev => ({ ...prev, [actionIdx]: 'Parameters must be a valid JSON object.' }))
      setStatusMessage('')
      return
    }
    const nextActions = envelope.actions.map((item, idx) => (
      idx === actionIdx ? { ...item, params: parsedParams } : item
    ))
    onChangeBody(serializeAuraFormEnvelope({
      ...envelope,
      actions: nextActions,
      unquotedTemplates: nextUnquoted,
    }))
    setParamDrafts(prev => {
      const copy = { ...prev }
      delete copy[actionIdx]
      return copy
    })
    setParamErrors(prev => {
      const copy = { ...prev }
      delete copy[actionIdx]
      return copy
    })
    setStatusMessage(`Applied parameter changes to ${formatAuraActionTitle(envelope.actions[actionIdx], actionIdx)}.`)
  }

  const resetSubActionParams = (actionIdx: number) => {
    setParamDrafts(prev => {
      const copy = { ...prev }
      delete copy[actionIdx]
      return copy
    })
    setParamErrors(prev => {
      const copy = { ...prev }
      delete copy[actionIdx]
      return copy
    })
    setStatusMessage('')
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
              onChange={event => updateMetaField({ fwuid: event.target.value })}
              placeholder="{{var.aura_fwuid}}"
            />
          </label>
        )}
      </div>

      {envelope.contextObject && (contextMode || contextApp) && (
        <div className="aura-envelope-context-card">
          <div className="aura-envelope-context-summary">
            <span><strong>Aura context</strong></span>
            {contextMode && <span>Mode: <code>{contextMode}</code></span>}
            {contextApp && <span>App: <code>{contextApp}</code></span>}
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
                    const nextVal = event.target.value
                    setParamDrafts(prev => ({ ...prev, [idx]: nextVal }))
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
