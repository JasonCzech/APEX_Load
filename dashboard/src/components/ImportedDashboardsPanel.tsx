import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import type { ImportedDashboard, ImportedDashboardImportReview, Project } from '../types'
import { useSystemApi } from '../hooks/api/useSystemApi'
import { getErrorMessage } from '../utils/errorUtils'
import ImportDropzone from './ImportDropzone'

interface Props {
  projects: Project[]
  defaultProjectId?: string
  prometheusConfigured: boolean
}

type PanelMode = 'list' | 'review'

// ImportedDashboardsPanel is the Settings → Integrations → Dashboards surface: upload a
// Grafana dashboard JSON, review which panels imported vs are unsupported, bind any
// required variables, and manage the imported dashboards. It lives next to the
// Prometheus datasource it depends on — not in the script-import wizard, which produces
// a very different artifact.
export default function ImportedDashboardsPanel({ projects, defaultProjectId, prometheusConfigured }: Props) {
  const { listImportedDashboards, importGrafanaDashboard, commitGrafanaDashboardImport, deleteImportedDashboard, patchImportedDashboard } = useSystemApi()

  const [projectId, setProjectId] = useState(defaultProjectId || projects[0]?.id || '')
  const [dashboards, setDashboards] = useState<ImportedDashboard[]>([])
  const [mode, setMode] = useState<PanelMode>('list')
  const [review, setReview] = useState<ImportedDashboardImportReview | null>(null)
  const [bindingValues, setBindingValues] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
	const [mutatingDashboardId, setMutatingDashboardId] = useState('')
  const [error, setError] = useState('')
	const [notice, setNotice] = useState('')
	const operationInFlight = useRef(false)
	const refreshGeneration = useRef(0)
	const previousDefaultProjectId = useRef(defaultProjectId)
	const controlsBusy = busy || mutatingDashboardId !== ''

  const refresh = useCallback(async (pid: string) => {
		const generation = ++refreshGeneration.current
    if (!pid) {
      setDashboards([])
      return
    }
    try {
      const response = await listImportedDashboards({ project_id: pid })
		if (generation === refreshGeneration.current) {
			setDashboards(response.dashboards ?? [])
			setError('')
		}
    } catch (err) {
		if (generation === refreshGeneration.current) setError(getErrorMessage(err, 'Failed to load imported dashboards.'))
    }
  }, [listImportedDashboards])

	useEffect(() => {
		const defaultChanged = defaultProjectId !== previousDefaultProjectId.current
		previousDefaultProjectId.current = defaultProjectId
		setProjectId(current => {
			if (defaultChanged && defaultProjectId && projects.some(project => project.id === defaultProjectId)) return defaultProjectId
			if (current && projects.some(project => project.id === current)) return current
			if (defaultProjectId && projects.some(project => project.id === defaultProjectId)) return defaultProjectId
			return projects[0]?.id || ''
		})
	}, [defaultProjectId, projects])

  useEffect(() => {
    void refresh(projectId)
		return () => { refreshGeneration.current += 1 }
  }, [projectId, refresh])

  const requiredBindings = useMemo(() => review?.required_bindings ?? [], [review])

	const handleFile = useCallback(async (file: File | null) => {
		if (!file) return
		if (operationInFlight.current) return
    if (!projectId) {
      setError('Select a project before importing a dashboard.')
      return
    }
		operationInFlight.current = true
		refreshGeneration.current += 1
		setBusy(true)
    setError('')
    setNotice('')
    try {
      const content = await file.text()
      const result = await importGrafanaDashboard({ project_id: projectId, name: file.name.replace(/\.json$/i, ''), content })
      setReview(result)
      setBindingValues({})
      setMode('review')
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to parse the dashboard JSON.'))
		} finally {
			operationInFlight.current = false
			setBusy(false)
    }
  }, [importGrafanaDashboard, projectId])

	const handleCommit = useCallback(async () => {
		if (!review) return
		if (operationInFlight.current) return
		operationInFlight.current = true
    setBusy(true)
    setError('')
    try {
      await commitGrafanaDashboardImport({
        project_id: projectId,
        import_session_id: review.import_session_id,
        bindings: bindingValues,
      })
      setReview(null)
      setMode('list')
      setNotice('Dashboard imported.')
      await refresh(projectId)
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to commit the dashboard import.'))
		} finally {
			operationInFlight.current = false
      setBusy(false)
    }
  }, [bindingValues, commitGrafanaDashboardImport, projectId, refresh, review])

  const handleToggle = useCallback(async (dashboard: ImportedDashboard) => {
		if (operationInFlight.current) return
		operationInFlight.current = true
		setMutatingDashboardId(dashboard.id)
		setError('')
    try {
      await patchImportedDashboard(dashboard.id, { enabled: !dashboard.enabled })
      await refresh(projectId)
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to update the dashboard.'))
		} finally {
			operationInFlight.current = false
			setMutatingDashboardId('')
    }
  }, [patchImportedDashboard, projectId, refresh])

  const handleDelete = useCallback(async (dashboard: ImportedDashboard) => {
		if (operationInFlight.current) return
		if (!window.confirm(`Delete imported dashboard "${dashboard.name}"? This cannot be undone.`)) return
		operationInFlight.current = true
		setMutatingDashboardId(dashboard.id)
		setError('')
    try {
      await deleteImportedDashboard(dashboard.id)
      await refresh(projectId)
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to delete the dashboard.'))
		} finally {
			operationInFlight.current = false
			setMutatingDashboardId('')
    }
  }, [deleteImportedDashboard, projectId, refresh])

  const canCommit = review !== null && !review.has_blockers && requiredBindings.every(name => (bindingValues[name] ?? '').trim() !== '')

  return (
    <div className="settings-group imported-dashboards-panel">
      <div className="settings-subsection">
        <div className="settings-manager-summary">
          <div>
            <span className="setting-label">Imported Dashboards</span>
            <span className="setting-desc">Import a Grafana dashboard JSON; its panels render as graph cards in the Command Center during a run and in run results.</span>
          </div>
          <strong>{dashboards.length} imported</strong>
        </div>

        {!prometheusConfigured && (
          <div className="settings-inline-warning" role="status">
            Imported dashboards query the Prometheus configured in the Prometheus tab. Configure it first, or panels will render empty.
          </div>
        )}
        {error && <div className="settings-inline-error" role="alert">{error}</div>}
        {notice && <div className="settings-inline-notice" role="status">{notice}</div>}

        <div className="setting-row">
          <div className="setting-info">
            <span className="setting-label">Project</span>
            <span className="setting-desc">Imported dashboards are scoped to a project.</span>
          </div>
          <select
            className="settings-input"
            aria-label="Dashboard import project"
			value={projectId}
			disabled={controlsBusy}
			onChange={event => {
				refreshGeneration.current += 1
				setDashboards([])
				setProjectId(event.target.value)
				setMode('list')
				setReview(null)
				setError('')
				setNotice('')
			}}
          >
            <option value="">Select a project…</option>
            {projects.map(project => (
              <option key={project.id} value={project.id}>{project.name}</option>
            ))}
          </select>
        </div>

        {mode === 'list' ? (
          <>
            <ImportDropzone
              accept=".json,application/json"
              file={null}
              onFile={handleFile}
              dropHint="Drop a Grafana dashboard JSON export here, or click to browse"
              onError={setError}
            />
            {busy && <div className="settings-inline-notice">Parsing…</div>}
            <ul className="imported-dashboards-list" aria-label="Imported dashboards">
              {dashboards.map(dashboard => (
                <li key={dashboard.id} className="imported-dashboards-item">
                  <div className="imported-dashboards-item-main">
                    <strong>{dashboard.name}</strong>
                    <span className="setting-desc">{dashboard.panels.length} panel{dashboard.panels.length === 1 ? '' : 's'}</span>
                  </div>
                  <div className="imported-dashboards-item-actions">
                    <label className="settings-checkbox-label">
							<input type="checkbox" checked={dashboard.enabled} disabled={controlsBusy} onChange={() => handleToggle(dashboard)} />
                      <span>Enabled</span>
                    </label>
							<button type="button" className="btn btn-sm btn-ghost" disabled={controlsBusy} onClick={() => handleDelete(dashboard)} aria-label={`Delete ${dashboard.name}`}>Delete</button>
                  </div>
                </li>
              ))}
              {dashboards.length === 0 && projectId && <li className="setting-desc">No dashboards imported yet.</li>}
            </ul>
          </>
        ) : review ? (
          <div className="imported-dashboards-review">
            <h4>Review import{review.dashboard.source_title ? `: ${review.dashboard.source_title}` : ''}</h4>
            <p className="setting-desc">
              {review.stats.panels_converted} of {review.stats.panels_seen} panels importable
              ({review.stats.line_charts} chart{review.stats.line_charts === 1 ? '' : 's'}, {review.stats.single_value_tiles} tile{review.stats.single_value_tiles === 1 ? '' : 's'}).
            </p>

            <div className="imported-dashboards-review-panels">
			{(review.dashboard.panels ?? []).map((panel, panelIndex) => (
                <div key={`${panel.panel_id}-${panelIndex}`} className="imported-dashboards-review-panel">
                  <span className={`imported-dashboards-badge badge-${panel.kind}`}>{panel.kind === 'line' ? 'line chart' : 'tile'}</span>
                  <strong>{panel.title}</strong>
                  {panel.targets?.map((target, targetIndex) => (
                    <code key={`${target.ref_id || 'target'}-${targetIndex}`} className="cell-mono">
                      {target.ref_id ? `${target.ref_id}: ` : ''}{target.expr}
                    </code>
                  ))}
                </div>
              ))}
            </div>

            {requiredBindings.length > 0 && (
              <div className="imported-dashboards-bindings">
                <h5>Required variables</h5>
                {requiredBindings.map(name => (
                  <div key={name} className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">${name}</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      aria-label={`Value for variable ${name}`}
                      value={bindingValues[name] ?? ''}
                      onChange={event => setBindingValues(prev => ({ ...prev, [name]: event.target.value }))}
                      placeholder="value"
                    />
                  </div>
                ))}
              </div>
            )}

            {review.review_sections && review.review_sections.length > 0 && (
              <div className="imported-dashboards-unsupported">
                {review.review_sections.map(section => (
                  <div key={section.key}>
                    <h5>{section.title}</h5>
                    <ul>
                      {section.items.map((item, index) => (
                        <li key={index} className="setting-desc">{item}</li>
                      ))}
                    </ul>
                  </div>
                ))}
              </div>
            )}

            {review.has_blockers && (
              <div className="settings-inline-error" role="alert">This dashboard has blocking issues and cannot be imported until they are resolved.</div>
            )}

            <div className="imported-dashboards-review-actions">
              <button type="button" className="btn btn-secondary" onClick={() => { setMode('list'); setReview(null) }} disabled={busy}>Cancel</button>
              <button type="button" className="btn btn-primary" onClick={handleCommit} disabled={!canCommit || busy}>Import dashboard</button>
            </div>
          </div>
        ) : null}
      </div>
    </div>
  )
}
