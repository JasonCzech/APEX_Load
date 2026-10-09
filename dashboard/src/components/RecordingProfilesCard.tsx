import { useCallback, useEffect, useMemo, useState } from 'react'

import { useApi } from '../hooks/useApi'
import type {
  CorrelationRuleInfo,
  RecordingProfile,
  RecordingProfileCorrelation,
  RecordingProfileUserRule,
} from '../types/recordingProfiles'

// RecordingProfilesCard picks the project recording profile a recording
// applies and edits the project's profiles, grouped the way VuGen groups its
// Recording Options (General, Code generation, HTTP properties, Recording
// filters, Correlations). The server validates and stores them.

type Props = {
  projectId?: string
  selectedId: string
  onSelect: (id: string, profile?: RecordingProfile) => void
  disabled?: boolean
}

const RULE_FAMILIES = ['json', 'html', 'xml', 'header', 'cookie', 'form', 'messaging']

function splitList(text: string): string[] | undefined {
  const list = text.split(/[,\n]/).map(entry => entry.trim()).filter(Boolean)
  return list.length > 0 ? list : undefined
}

function optionalNumber(text: string): number | undefined {
  if (text.trim() === '') return undefined
  const value = Number(text)
  return Number.isFinite(value) ? Math.trunc(value) : undefined
}

function newProfileID(profiles: RecordingProfile[]): string {
  for (let n = profiles.length + 1; ; n++) {
    const id = `profile-${n}`
    if (!profiles.some(profile => profile.id.toLowerCase() === id)) return id
  }
}

// ListInput edits a string list as comma-separated text; it keeps the text
// the user typed and reports the parsed list.
function ListInput({ label, value, onChange, placeholder, disabled }: {
  label: string
  value?: string[]
  onChange: (list: string[] | undefined) => void
  placeholder?: string
  disabled?: boolean
}) {
  const [text, setText] = useState((value || []).join(', '))
  return (
    <label className="rec-profile-field">
      <span className="rec-label">{label}</span>
      <input
        className="rec-input"
        aria-label={label}
        value={text}
        disabled={disabled}
        placeholder={placeholder}
        onChange={event => {
          setText(event.target.value)
          onChange(splitList(event.target.value))
        }}
      />
    </label>
  )
}

function NumberInput({ label, value, onChange, placeholder, disabled }: {
  label: string
  value?: number
  onChange: (value: number | undefined) => void
  placeholder?: string
  disabled?: boolean
}) {
  return (
    <label className="rec-profile-field">
      <span className="rec-label">{label}</span>
      <input
        className="rec-input"
        aria-label={label}
        type="number"
        min={0}
        value={value ?? ''}
        disabled={disabled}
        placeholder={placeholder}
        onChange={event => onChange(optionalNumber(event.target.value))}
      />
    </label>
  )
}

function UserRulesEditor({ rules, onChange, disabled }: {
  rules: RecordingProfileUserRule[]
  onChange: (rules: RecordingProfileUserRule[]) => void
  disabled?: boolean
}) {
  const update = (index: number, patch: Partial<RecordingProfileUserRule>) =>
    onChange(rules.map((rule, i) => (i === index ? { ...rule, ...patch } : rule)))
  return (
    <div className="rec-profile-rules" data-testid="recording-profile-user-rules">
      {rules.length === 0 && <p className="rec-hint">No project rules. Automatic detection still runs unless it is turned off above.</p>}
      {rules.map((rule, index) => (
        <div key={`${index}-${rules.length}`} className="rec-profile-rule">
          <input className="rec-input" aria-label={`Rule ${index + 1} parameter name`} value={rule.name} disabled={disabled} placeholder="ParameterName" onChange={event => update(index, { name: event.target.value })} />
          <select className="rec-input" aria-label={`Rule ${index + 1} type`} value={rule.type} disabled={disabled} onChange={event => update(index, { type: event.target.value === 'regex' ? 'regex' : 'boundary' })}>
            <option value="boundary">Boundaries</option>
            <option value="regex">Regular expression</option>
          </select>
          {rule.type === 'regex' ? (
            <input className="rec-input rec-profile-rule-wide" aria-label={`Rule ${index + 1} regex`} value={rule.regex || ''} disabled={disabled} placeholder="id=(\d+)" onChange={event => update(index, { regex: event.target.value })} />
          ) : (
            <>
              <input className="rec-input" aria-label={`Rule ${index + 1} left boundary`} value={rule.left || ''} disabled={disabled} placeholder="Left boundary" onChange={event => update(index, { left: event.target.value })} />
              <input className="rec-input" aria-label={`Rule ${index + 1} right boundary`} value={rule.right || ''} disabled={disabled} placeholder="Right boundary" onChange={event => update(index, { right: event.target.value })} />
            </>
          )}
          <input className="rec-input" aria-label={`Rule ${index + 1} hosts`} defaultValue={(rule.hosts || []).join(', ')} disabled={disabled} placeholder="Hosts (all)" onChange={event => update(index, { hosts: splitList(event.target.value) })} />
          <label className="rec-checkbox-row">
            <input type="checkbox" aria-label={`Rule ${index + 1} enabled`} checked={!rule.disabled} disabled={disabled} onChange={event => update(index, { disabled: !event.target.checked || undefined })} />
            <span>Enabled</span>
          </label>
          <button type="button" className="btn btn-sm btn-secondary" disabled={disabled} onClick={() => onChange(rules.filter((_, i) => i !== index))}>Remove</button>
        </div>
      ))}
      <button type="button" className="btn btn-sm btn-secondary" disabled={disabled} onClick={() => onChange([...rules, { name: '', type: 'boundary' }])}>Add rule</button>
    </div>
  )
}

function ProfileEditor({ profile, rules, onChange, disabled }: {
  profile: RecordingProfile
  rules: CorrelationRuleInfo[]
  onChange: (profile: RecordingProfile) => void
  disabled?: boolean
}) {
  const set = (patch: Partial<RecordingProfile>) => onChange({ ...profile, ...patch })
  const correlation = profile.correlation || {}
  const setCorrelation = (patch: Partial<RecordingProfileCorrelation>) => set({ correlation: { ...correlation, ...patch } })
  const disabledRules = new Set((correlation.disabled_rules || []).map(rule => rule.toLowerCase()))
  const toggleRule = (id: string, on: boolean) => {
    const next = new Set(disabledRules)
    if (on) next.delete(id)
    else next.add(id)
    setCorrelation({ disabled_rules: next.size > 0 ? [...next] : undefined })
  }
  return (
    <div className="rec-profile-editor" data-testid="recording-profile-editor">
      <fieldset className="rec-profile-group">
        <legend>General</legend>
        <label className="rec-profile-field">
          <span className="rec-label">Profile name</span>
          <input className="rec-input" aria-label="Profile name" value={profile.name} disabled={disabled} onChange={event => set({ name: event.target.value })} />
        </label>
        <label className="rec-profile-field">
          <span className="rec-label">Description</span>
          <input className="rec-input" aria-label="Profile description" value={profile.description || ''} disabled={disabled} onChange={event => set({ description: event.target.value || undefined })} />
        </label>
        <label className="rec-profile-field">
          <span className="rec-label">HTTP script level</span>
          <select className="rec-input" aria-label="Profile HTTP script level" value={profile.http_script_level || ''} disabled={disabled} onChange={event => set({ http_script_level: (event.target.value || undefined) as RecordingProfile['http_script_level'] })}>
            <option value="">Recorder default (URL-based)</option>
            <option value="url">URL-based</option>
            <option value="html">HTML-based</option>
          </select>
        </label>
        <label className="rec-profile-field">
          <span className="rec-label">Non-HTML resources</span>
          <select className="rec-input" aria-label="Profile non-HTML resources" value={profile.resource_mode || ''} disabled={disabled} onChange={event => set({ resource_mode: (event.target.value || undefined) as RecordingProfile['resource_mode'] })}>
            <option value="">Recorder default (downloaded at replay)</option>
            <option value="runtime">Downloaded at replay (not recorded)</option>
            <option value="in_step">In the page step (EXTRARES)</option>
            <option value="separate">Separate steps</option>
          </select>
        </label>
        <NumberInput label="Think-time threshold (ms)" value={profile.think_time_threshold_ms} placeholder="500" disabled={disabled} onChange={value => set({ think_time_threshold_ms: value })} />
      </fieldset>

      <fieldset className="rec-profile-group">
        <legend>Code generation</legend>
        <label className="rec-checkbox-row">
          <input type="checkbox" checked={profile.page_title_checks !== false} disabled={disabled} onChange={event => set({ page_title_checks: event.target.checked ? undefined : false })} />
          <span>Add page title checks</span>
        </label>
        <label className="rec-checkbox-row">
          <input type="checkbox" checked={profile.async_scan !== false} disabled={disabled} onChange={event => set({ async_scan: event.target.checked ? undefined : false })} />
          <span>Scan for asynchronous traffic (polling, long-polling, push)</span>
        </label>
      </fieldset>

      <fieldset className="rec-profile-group">
        <legend>HTTP properties: headers</legend>
        <ListInput key={`${profile.id}-record`} label="Always record headers" value={profile.record_headers} placeholder="User-Agent, Accept-Language" disabled={disabled} onChange={list => set({ record_headers: list })} />
        <ListInput key={`${profile.id}-drop`} label="Never record headers" value={profile.drop_headers} placeholder="X-Debug-Token" disabled={disabled} onChange={list => set({ drop_headers: list })} />
      </fieldset>

      <fieldset className="rec-profile-group">
        <legend>Recording filters</legend>
        <ListInput key={`${profile.id}-include`} label="Record only hosts" value={profile.include_hosts} placeholder="*.example.com" disabled={disabled} onChange={list => set({ include_hosts: list })} />
        <ListInput key={`${profile.id}-exclude`} label="Exclude hosts" value={profile.exclude_hosts} placeholder="analytics.example.com" disabled={disabled} onChange={list => set({ exclude_hosts: list })} />
        <ListInput key={`${profile.id}-types`} label="Exclude content types" value={profile.exclude_content_types} placeholder="image/*, font/woff2" disabled={disabled} onChange={list => set({ exclude_content_types: list })} />
        <NumberInput label="Body capture limit (bytes)" value={profile.body_capture_limit_bytes} placeholder="Recorder default" disabled={disabled} onChange={value => set({ body_capture_limit_bytes: value })} />
        <label className="rec-checkbox-row">
          <input type="checkbox" checked={profile.capture_cookies === true} disabled={disabled} onChange={event => set({ capture_cookies: event.target.checked ? true : undefined })} />
          <span>Record Cookie headers (replay's cookie jar sends them otherwise)</span>
        </label>
      </fieldset>

      <fieldset className="rec-profile-group">
        <legend>Correlations: configuration</legend>
        <label className="rec-checkbox-row">
          <input type="checkbox" checked={!correlation.disabled} disabled={disabled} onChange={event => setCorrelation({ disabled: event.target.checked ? undefined : true })} />
          <span>Detect correlations automatically</span>
        </label>
        <div className="rec-profile-families" role="group" aria-label="Automatic rule families">
          {RULE_FAMILIES.map(family => (
            <label key={family} className="rec-checkbox-row">
              <input type="checkbox" checked={!disabledRules.has(family)} disabled={disabled || correlation.disabled} onChange={event => toggleRule(family, event.target.checked)} />
              <span>{family}</span>
            </label>
          ))}
        </div>
        {rules.length > 0 && (
          <details className="rec-profile-rule-catalog">
            <summary>Individual automatic rules ({rules.length})</summary>
            {rules.map(rule => (
              <label key={rule.id} className="rec-checkbox-row" title={rule.description}>
                <input type="checkbox" checked={!disabledRules.has(rule.id.toLowerCase()) && !disabledRules.has(rule.family)} disabled={disabled || correlation.disabled || disabledRules.has(rule.family)} onChange={event => toggleRule(rule.id.toLowerCase(), event.target.checked)} />
                <span>{rule.title} <span className="rec-hint">({rule.family})</span></span>
              </label>
            ))}
          </details>
        )}
        <ListInput key={`${profile.id}-values`} label="Never correlate values" value={correlation.excluded_values} disabled={disabled} onChange={list => setCorrelation({ excluded_values: list })} />
        <ListInput key={`${profile.id}-ctypes`} label="Skip response content types" value={correlation.excluded_content_types} placeholder="text/css" disabled={disabled} onChange={list => setCorrelation({ excluded_content_types: list })} />
        <NumberInput label="Minimum value length" value={correlation.min_value_length} placeholder="4" disabled={disabled} onChange={value => setCorrelation({ min_value_length: value })} />
        <NumberInput label="Maximum value length" value={correlation.max_value_length} placeholder="400" disabled={disabled} onChange={value => setCorrelation({ max_value_length: value })} />
        <label className="rec-checkbox-row">
          <input type="checkbox" checked={!!correlation.case_insensitive} disabled={disabled} onChange={event => setCorrelation({ case_insensitive: event.target.checked || undefined })} />
          <span>Match rule boundaries without regard to case</span>
        </label>
        <label className="rec-checkbox-row">
          <input type="checkbox" checked={!!correlation.prefer_regex} disabled={disabled} onChange={event => setCorrelation({ prefer_regex: event.target.checked || undefined })} />
          <span>Generate regular expressions instead of boundaries</span>
        </label>
      </fieldset>

      <fieldset className="rec-profile-group">
        <legend>Correlations: project rules</legend>
        <UserRulesEditor key={profile.id} rules={correlation.user_rules || []} disabled={disabled} onChange={next => setCorrelation({ user_rules: next.length > 0 ? next : undefined })} />
      </fieldset>
    </div>
  )
}

export default function RecordingProfilesCard({ projectId, selectedId, onSelect, disabled }: Props) {
  const api = useApi()
  const [profiles, setProfiles] = useState<RecordingProfile[]>([])
  const [rules, setRules] = useState<CorrelationRuleInfo[]>([])
  const [updatedAt, setUpdatedAt] = useState('')
  const [draft, setDraft] = useState<RecordingProfile[] | null>(null)
  const [editingId, setEditingId] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    setProfiles([])
    setDraft(null)
    setError('')
    if (!projectId) return
    let current = true
    api.getRecordingProfiles(projectId).then(
      response => {
        if (!current) return
        setProfiles(response.profiles || [])
        setRules(response.rules || [])
        setUpdatedAt(response.updated_at || '')
      },
      (failure: Error) => {
        if (current) setError(failure.message)
      },
    )
    return () => {
      current = false
    }
  }, [api, projectId])

  // A selection the project no longer has is cleared; a saved change to the
  // selected profile reaches the parent.
  useEffect(() => {
    if (!selectedId) return
    const selected = profiles.find(profile => profile.id === selectedId)
    onSelect(selected ? selectedId : '', selected)
  }, [profiles, selectedId, onSelect])

  const editing = useMemo(() => draft?.find(profile => profile.id === editingId) || null, [draft, editingId])

  const openEditor = () => {
    const copy = profiles.map(profile => structuredClone(profile))
    setDraft(copy)
    setEditingId(selectedId || copy[0]?.id || '')
    setError('')
  }

  const addProfile = (base?: RecordingProfile) => {
    if (!draft) return
    const id = newProfileID(draft)
    const profile: RecordingProfile = base ? { ...structuredClone(base), id, name: `${base.name} copy` } : { id, name: 'New profile' }
    setDraft([...draft, profile])
    setEditingId(id)
  }

  const save = useCallback(async () => {
    if (!projectId || !draft) return
    setSaving(true)
    setError('')
    try {
      const response = await api.saveRecordingProfiles(projectId, draft, updatedAt)
      setProfiles(response.profiles || [])
      setRules(response.rules || rules)
      setUpdatedAt(response.updated_at || '')
      setDraft(null)
    } catch (failure) {
      setError((failure as Error).message)
    } finally {
      setSaving(false)
    }
  }, [api, draft, projectId, rules, updatedAt])

  if (!projectId) return null
  return (
    <div className="rec-controls-card rec-profiles-card" data-testid="recording-profiles-card">
      <div className="rec-profile-picker">
        <label className="rec-profile-field">
          <span className="rec-label">Recording profile</span>
          <select className="rec-input" aria-label="Recording profile" value={selectedId} disabled={disabled} onChange={event => onSelect(event.target.value, profiles.find(profile => profile.id === event.target.value))}>
            <option value="">None (recorder defaults)</option>
            {profiles.map(profile => (
              <option key={profile.id} value={profile.id}>{profile.name}</option>
            ))}
          </select>
        </label>
        {!draft && (
          <button type="button" className="rec-inline-action" disabled={disabled} onClick={openEditor}>Manage profiles</button>
        )}
      </div>
      {error && <div className="rec-feedback error" role="alert">{error}</div>}
      {draft && (
        <div className="rec-profiles-manager">
          <div className="rec-profile-tabs" role="group" aria-label="Recording profiles">
            {draft.map(profile => (
              <button key={profile.id} type="button" className="btn btn-sm btn-secondary" aria-pressed={profile.id === editingId} onClick={() => setEditingId(profile.id)}>
                {profile.name || profile.id}
              </button>
            ))}
            <button type="button" className="btn btn-sm btn-secondary" onClick={() => addProfile()}>New profile</button>
            {editing && <button type="button" className="btn btn-sm btn-secondary" onClick={() => addProfile(editing)}>Duplicate</button>}
            {editing && (
              <button type="button" className="btn btn-sm btn-danger" onClick={() => {
                const rest = draft.filter(profile => profile.id !== editing.id)
                setDraft(rest)
                setEditingId(rest[0]?.id || '')
              }}>Delete</button>
            )}
          </div>
          {editing ? (
            <ProfileEditor
              key={editing.id}
              profile={editing}
              rules={rules}
              disabled={saving}
              onChange={next => setDraft(draft.map(profile => (profile.id === editing.id ? next : profile)))}
            />
          ) : (
            <p className="rec-hint">This project has no recording profiles. A profile saves recording options once for every recording that selects it.</p>
          )}
          <div className="rec-profile-actions">
            <button type="button" className="btn btn-primary" disabled={saving} onClick={save}>{saving ? 'Saving…' : 'Save profiles'}</button>
            <button type="button" className="btn btn-secondary" disabled={saving} onClick={() => { setDraft(null); setError('') }}>Cancel</button>
          </div>
        </div>
      )}
    </div>
  )
}
