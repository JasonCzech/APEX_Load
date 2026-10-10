import { useEffect, useState } from 'react'

import { request } from '../hooks/apiClient'
import type { ProjectAnalysisGates, ProjectAnalysisSettingsResponse } from '../types/rca'
import { formatDateTime } from '../utils/dateTime'
import { getErrorMessage } from '../utils/errorUtils'

interface Props {
  projectId: string
  projectName?: string
  // Project administrators change the settings; others read them.
  canEdit: boolean
  // onSaved receives the settings a save stored, so the page's copy of the
  // project stays current.
  onSaved?: (settings: ProjectAnalysisSettingsResponse['settings']) => void
}

type Flag = { group: 'root_cause'; key: keyof ProjectAnalysisGates['root_cause'] } | { group: 'business_keys'; key: keyof ProjectAnalysisGates['business_keys'] }

interface FlagRow {
  flag: Flag
  label: string
  description: string
}

const ROWS: FlagRow[] = [
  { flag: { group: 'root_cause', key: 'enabled' }, label: 'Compute root-cause analyses', description: 'Editors can analyze an archived run; analyses already saved stay readable when this is off.' },
  { flag: { group: 'root_cause', key: 'present' }, label: 'Show root cause in summaries and reports', description: 'Run summaries, Results, the dashboard, Diagnostics and exported reports show the generated root cause and run validity.' },
  { flag: { group: 'root_cause', key: 'auto_analyze' }, label: 'Analyze every archived run automatically', description: 'An analysis is queued as soon as a run’s archive is finalized. Requested analyses still run first.' },
  { flag: { group: 'root_cause', key: 'auto_enrich' }, label: 'Capture provider evidence when a run is archived', description: 'Freezes the monitor sources and datasets as they stand at archive time, for later analyses to reuse.' },
  { flag: { group: 'business_keys', key: 'enabled' }, label: 'Allow business-key lookups', description: 'Approved business-key mappings digest values such as order IDs, and editors can find a run’s requests and logs by them.' },
]

const read = (gates: ProjectAnalysisGates, flag: Flag) => (flag.group === 'root_cause' ? gates.root_cause[flag.key] : gates.business_keys[flag.key])

function settingsPath(projectId: string) {
  return `/projects/${encodeURIComponent(projectId)}/analysis-settings`
}

// ProjectAnalysisSettingsPanel is Settings › Project › Analysis: the
// project's root-cause and business-key opt-ins. A flag the deployment turns
// on for the project is shown on and cannot be turned off here. Saves send
// the revision that was read, so a newer save is never overwritten.
export default function ProjectAnalysisSettingsPanel({ projectId, projectName, canEdit, onSaved }: Props) {
  const [current, setCurrent] = useState<ProjectAnalysisSettingsResponse | null>(null)
  const [draft, setDraft] = useState<ProjectAnalysisGates | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [status, setStatus] = useState('')
  const [reloadKey, setReloadKey] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    request<ProjectAnalysisSettingsResponse>(settingsPath(projectId), { signal: controller.signal })
      .then(response => {
        if (controller.signal.aborted) return
        setCurrent(response)
        setDraft({ root_cause: { ...response.settings.root_cause }, business_keys: { ...response.settings.business_keys } })
      })
      .catch(err => { if (!controller.signal.aborted) setError(getErrorMessage(err, 'Unable to load the project’s analysis settings')) })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [projectId, reloadKey])

  const changed = Boolean(current && draft && ROWS.some(row => read(draft, row.flag) !== read(current.settings, row.flag)))

  const toggle = (flag: Flag, value: boolean) => {
    setStatus('')
    setDraft(prev => {
      if (!prev) return prev
      if (flag.group === 'root_cause') return { ...prev, root_cause: { ...prev.root_cause, [flag.key]: value } }
      return { ...prev, business_keys: { ...prev.business_keys, [flag.key]: value } }
    })
  }

  const save = async () => {
    if (!current || !draft) return
    setSaving(true)
    setError('')
    setStatus('')
    try {
      const saved = await request<ProjectAnalysisSettingsResponse>(settingsPath(projectId), {
        method: 'PUT',
        body: JSON.stringify({ expected_revision: current.settings.revision, root_cause: draft.root_cause, business_keys: draft.business_keys }),
      })
      setCurrent(saved)
      setDraft({ root_cause: { ...saved.settings.root_cause }, business_keys: { ...saved.settings.business_keys } })
      setStatus(`Saved as revision ${saved.settings.revision}.`)
      onSaved?.(saved.settings)
    } catch (err) {
      const conflict = (err as { status?: number })?.status === 409
      setError(conflict ? 'These settings changed since you opened them. Reload to see the newest revision, then make your change again.' : getErrorMessage(err, 'Unable to save the project’s analysis settings'))
    } finally {
      setSaving(false)
    }
  }

  if (loading && !current) return <p role="status">Loading analysis settings…</p>
  if (!current || !draft) return <p className="settings-error-inline" role="alert">{error || 'Analysis settings are unavailable.'}</p>

  const rootCauseOn = draft.root_cause.enabled || current.forced_by_env.root_cause.enabled
  return (
    <div className="settings-subsection project-analysis-settings">
      <h3>Analysis{projectName ? ` · ${projectName}` : ''}</h3>
      <p className="setting-desc">
        Root-cause analysis judges an archived run’s validity, finds where its series changed and ranks the likely causes. It runs on the controller from the run’s archive and monitor evidence.
        The CI validity gate is set in the project’s GitHub Actions policy, not here.
      </p>
      <div className="project-analysis-settings-rows">
        {ROWS.map(row => {
          const forced = read(current.forced_by_env, row.flag)
          const value = forced || read(draft, row.flag)
          // Automatic analysis needs analyses to be computed.
          const needsRootCause = row.flag.group === 'root_cause' && row.flag.key === 'auto_analyze' && !rootCauseOn
          const id = `analysis-${row.flag.group}-${row.flag.key}`
          return (
            <div className="setting-row" key={id}>
              <label className="checkbox-label" htmlFor={id}>
                <input id={id} type="checkbox" checked={value} disabled={!canEdit || saving || forced || needsRootCause} onChange={event => toggle(row.flag, event.target.checked)} />
                <span className="setting-label">{row.label}</span>
                {forced && <span className="project-analysis-forced" title="The deployment’s environment lists turn this on for the project.">On for this deployment</span>}
              </label>
              <span className="setting-desc">{needsRootCause ? `${row.description} Turn on computing analyses first.` : row.description}</span>
            </div>
          )
        })}
      </div>
      <p className="setting-desc">
        {current.settings.revision > 0
          ? `Revision ${current.settings.revision}${current.settings.updated_by ? ` by ${current.settings.updated_by}` : ''}${current.settings.updated_at ? ` · ${formatDateTime(current.settings.updated_at)}` : ''}.`
          : 'Not changed yet; only the deployment’s lists apply.'}
      </p>
      {!canEdit && <p className="settings-error-inline">A project administrator changes these settings.</p>}
      {error && <p className="settings-error-inline" role="alert">{error}</p>}
      {status && <p className="setting-desc" role="status">{status}</p>}
      <div className="settings-inline-actions">
        {canEdit && <button className="btn btn-primary" type="button" disabled={!changed || saving} onClick={() => void save()}>{saving ? 'Saving…' : 'Save analysis settings'}</button>}
        <button className="btn btn-ghost" type="button" disabled={saving} onClick={() => setReloadKey(key => key + 1)}>Reload</button>
      </div>
    </div>
  )
}
