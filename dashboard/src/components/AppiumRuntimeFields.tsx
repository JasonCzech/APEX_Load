import { useEffect, useRef, useState } from 'react'

import type { AppiumConfig } from '../types/scripts'
import SecondsFromMillisecondsInput from './SecondsFromMillisecondsInput'

function formatCapabilities(capabilities?: Record<string, unknown>): string {
  return JSON.stringify(capabilities || {}, null, 2)
}

/**
 * The connection of a device observer script: the Appium server (a device
 * cloud's hub, with its credentials as {{secret.NAME}} references, or Appium
 * on a Mac driving a simulator) and the capabilities naming the device and app.
 */
export default function AppiumRuntimeFields({
  config,
  onChange,
}: {
  config?: AppiumConfig
  onChange: (config: AppiumConfig) => void
}) {
  const current = config || {}
  // The capabilities are edited as JSON; a draft that does not parse stays
  // here, with the reason, until it does.
  const [draft, setDraft] = useState(() => formatCapabilities(current.capabilities))
  const [draftError, setDraftError] = useState('')
  const draftRef = useRef(draft)
  const committed = formatCapabilities(current.capabilities)
  // Capabilities changed elsewhere (a reset, a protocol switch) replace a
  // draft that does not already say the same.
  useEffect(() => {
    let same = false
    try {
      same = formatCapabilities(JSON.parse(draftRef.current)) === committed
    } catch {
      // A draft that does not parse yet is replaced by the saved value.
    }
    if (!same) {
      draftRef.current = committed
      setDraft(committed)
      setDraftError('')
    }
  }, [committed])

  const editCapabilities = (text: string) => {
    draftRef.current = text
    setDraft(text)
    try {
      const parsed = JSON.parse(text)
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
        setDraftError('Capabilities must be a JSON object.')
        return
      }
      setDraftError('')
      onChange({ ...current, capabilities: parsed as Record<string, unknown> })
    } catch {
      setDraftError('Capabilities must be valid JSON.')
    }
  }

  return (
    <div className="form-grid">
      <label className="full-width">
        <span>Appium server URL</span>
        <input
          value={current.server_url || ''}
          onChange={e => onChange({ ...current, server_url: e.target.value })}
          placeholder="http://127.0.0.1:4723 or https://{{secret.CLOUD_USER}}:{{secret.CLOUD_KEY}}@hub.example.com/wd/hub"
        />
      </label>
      <label className="full-width">
        <span>Capabilities (JSON)</span>
        <textarea
          rows={7}
          value={draft}
          onChange={e => editCapabilities(e.target.value)}
          aria-invalid={!!draftError}
          spellCheck={false}
        />
        {draftError
          ? <span className="field-error" role="alert">{draftError}</span>
          : <span className="field-hint">platformName, appium:automationName, appium:deviceName or appium:udid, and appium:app or appium:bundleId.</span>}
      </label>
      <label>
        <span>Session start timeout (s)</span>
        <SecondsFromMillisecondsInput
          valueMs={current.session_timeout_ms}
          fallbackMs={180000}
          onChangeMs={value => onChange({ ...current, session_timeout_ms: value || undefined })}
        />
      </label>
      <label>
        <span>Element wait (s)</span>
        <SecondsFromMillisecondsInput
          valueMs={current.element_timeout_ms}
          fallbackMs={15000}
          onChangeMs={value => onChange({ ...current, element_timeout_ms: value || undefined })}
        />
      </label>
    </div>
  )
}
