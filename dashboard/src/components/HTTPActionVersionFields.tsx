import { useId } from 'react'
import type { ScriptActionDefinition } from '../types/scripts'
import { httpCapturedVersionEvidence, HTTP_ACTION_VERSION_OPTIONS, httpActionVersionSettings, isHTTPActionVersion } from './httpActionVersionModel'
import { HTTP3ExecutionNote } from './HTTP3ExecutionNote'
import { http3PolicyIssues, type HTTP3ScriptContext } from './httpQUICModel'
import './HTTPQUICSettingsFields.css'

type Props = {
  action: ScriptActionDefinition
  index: number
  protocol: string
  scriptVersion?: string
  scriptContext?: HTTP3ScriptContext
  onUpdate: (index: number, field: keyof ScriptActionDefinition, value: unknown) => void
}

export function HTTPActionVersionFields({ action, index, protocol, scriptVersion, scriptContext, onUpdate }: Props) {
  const id = useId()
  const settings = httpActionVersionSettings(action, protocol, scriptVersion)
  if (!settings) return null
  const observations = httpCapturedVersionEvidence(action.metadata)
  return <section className="sub-section" aria-label="Request HTTP version policy">
    <label htmlFor={id}>Request HTTP version</label>
    <select id={id} className="settings-input" aria-invalid={!settings.valid} value={settings.selection}
      onChange={event => {
        const value = event.target.value
        if (value === 'inherit') onUpdate(index, 'http_version', undefined)
        else if (isHTTPActionVersion(value)) onUpdate(index, 'http_version', value)
      }}>
      <option value="inherit">Inherit script setting ({settings.inheritedLabel})</option>
      {HTTP_ACTION_VERSION_OPTIONS.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
      {!settings.valid && <option value="invalid" disabled>Invalid authored HTTP version</option>}
    </select>
    {!settings.valid && <p role="alert">This request has an unsupported HTTP version value. Choose a supported policy or review the source JSON. Its original value is preserved until you edit it.</p>}
    {settings.inherited === null && <p role="alert">The script HTTP version is unsupported. Review Runtime Settings before replay.</p>}
    <p>Authored policy: {settings.present ? settings.effectiveLabel : `Inherit script setting (${settings.inheritedLabel})`}.</p>
    {http3PolicyIssues(settings.policy ?? undefined, { http: scriptContext?.http, web: scriptContext?.web_runtime, target: action.target, baseURL: scriptContext?.base_url }).map(issue => <p key={issue} className="http3-policy-issue" role="alert">{issue}</p>)}
    <HTTP3ExecutionNote as="p" policy={settings.policy ?? undefined} />
    <dl><dt>{observations.requestLabel}</dt><dd>{observations.request}</dd>
      <dt>{observations.responseLabel}</dt><dd>{observations.response}</dd></dl>
    {observations.proxyLegs && <p>The client-to-proxy and proxy-to-origin observations describe different connections.</p>}
    {observations.importMode && <p>Imported with HTTP version mode: {observations.importMode}.</p>}
    {observations.transportEdit && <p>Chosen in import review: {observations.transportEdit}.</p>}
    <small>Captured versions are observations from the source. Replay shows its actual negotiated version separately. Requests keep the current virtual user’s cookies and authentication.</small>
  </section>
}
