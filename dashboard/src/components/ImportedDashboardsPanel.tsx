import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import type { ImportedDashboard, ImportedDashboardGraphsResponse, ImportedDashboardImportReview, Project } from '../types'
import { useSystemApi } from '../hooks/api/useSystemApi'
import { getErrorMessage } from '../utils/errorUtils'
import ImportDropzone from './ImportDropzone'
import PrometheusPanelCard from './PrometheusPanelCard'

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
  const { listImportedDashboards, importGrafanaDashboard, importGrafanaDashboardByUID, commitGrafanaDashboardImport, previewGrafanaDashboardImport, resolveGrafanaImportLabelValues, getImportedDashboardPanels, deleteImportedDashboard, patchImportedDashboard } = useSystemApi()

  const [projectId, setProjectId] = useState(defaultProjectId || projects[0]?.id || '')
  const [dashboards, setDashboards] = useState<ImportedDashboard[]>([])
  const [mode, setMode] = useState<PanelMode>('list')
  const [review, setReview] = useState<ImportedDashboardImportReview | null>(null)
  const [bindingValues, setBindingValues] = useState<Record<string, string>>({})
  const [bindingOptions, setBindingOptions] = useState<Record<string, string[]>>({})
  const [loadingBinding, setLoadingBinding] = useState('')
  const [busy, setBusy] = useState(false)
	const [mutatingDashboardId, setMutatingDashboardId] = useState('')
  const [error, setError] = useState('')
	const [notice, setNotice] = useState('')
	const [preview, setPreview] = useState<ImportedDashboardGraphsResponse | null>(null)
	const [previewLoading, setPreviewLoading] = useState(false)
	const [testedDashboardId, setTestedDashboardId] = useState('')
	const [testedResult, setTestedResult] = useState<ImportedDashboardGraphsResponse | null>(null)
	const [grafanaUID, setGrafanaUID] = useState('')
	const [previewTestId, setPreviewTestId] = useState('')
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
	const reviewNeedsTestId = useMemo(() => dashboardUsesTestID(review?.dashboard.panels ?? []) || (review?.bindings ?? []).some(binding => expressionUsesTestID(binding.query ?? '')), [review])

	useEffect(() => {
		if (!review || review.has_blockers || !prometheusConfigured || (reviewNeedsTestId && !previewTestId.trim()) || !requiredBindings.every(name => (bindingValues[name] ?? '').trim())) {
			setPreview(null)
			setPreviewLoading(false)
			return
		}
		let cancelled = false
		const timer = window.setTimeout(async () => {
			setPreviewLoading(true)
			try {
				const end = new Date()
				const response = await previewGrafanaDashboardImport({ project_id: projectId, import_session_id: review.import_session_id, bindings: bindingValues, test_id: previewTestId.trim() || undefined, start: new Date(end.getTime() - 15 * 60_000).toISOString(), end: end.toISOString(), step_seconds: 15 })
				if (!cancelled) setPreview(response)
			} catch (err) {
				if (!cancelled) setPreview({ enabled: true, configured: true, generated_at: new Date().toISOString(), dashboards: [{ dashboard_id: review.import_session_id, dashboard_title: review.dashboard.source_title, panels: (review.dashboard.panels ?? []).map(panel => ({ ...panel, error: getErrorMessage(err, 'Preview unavailable.') })) }] })
			} finally {
				if (!cancelled) setPreviewLoading(false)
			}
		}, 500)
		return () => { cancelled = true; window.clearTimeout(timer) }
	}, [bindingValues, previewGrafanaDashboardImport, previewTestId, projectId, prometheusConfigured, requiredBindings, review, reviewNeedsTestId])

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
      setBindingOptions({})
		setPreviewTestId('')
      setMode('review')
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to parse the dashboard JSON.'))
		} finally {
			operationInFlight.current = false
			setBusy(false)
    }
	}, [importGrafanaDashboard, projectId])

  const handleLoadBindingOptions = useCallback(async (name: string) => {
    if (!review || loadingBinding) return
    setLoadingBinding(name)
    setError('')
    try {
		const response = await resolveGrafanaImportLabelValues({ project_id: projectId, import_session_id: review.import_session_id, variable: name, bindings: bindingValues, test_id: previewTestId.trim() || undefined })
      setBindingOptions(current => ({ ...current, [name]: response.options ?? [] }))
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to resolve label values.'))
    } finally {
      setLoadingBinding('')
    }
	}, [bindingValues, loadingBinding, previewTestId, projectId, resolveGrafanaImportLabelValues, review])

	const handleLiveUID = useCallback(async () => {
		if (!projectId || !grafanaUID.trim() || operationInFlight.current) return
		operationInFlight.current = true
		setBusy(true); setError('')
		try {
			const result = await importGrafanaDashboardByUID({ project_id: projectId, uid: grafanaUID.trim() })
			setReview(result); setBindingValues({}); setBindingOptions({}); setPreviewTestId(''); setMode('review')
		} catch (err) { setError(getErrorMessage(err, 'Failed to fetch Grafana dashboard.')) }
		finally { operationInFlight.current = false; setBusy(false) }
	}, [grafanaUID, importGrafanaDashboardByUID, projectId])

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
		setPreviewTestId('')
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

	const handleTestDashboard = useCallback(async (dashboard: ImportedDashboard) => {
		if (operationInFlight.current) return
		let testId = ''
		if (dashboardUsesTestID(dashboard.panels ?? [])) {
			const supplied = window.prompt('Run ID to use for test-scoped dashboard queries')
			if (supplied === null) return
			testId = supplied.trim()
			if (!testId) {
				setError('A run ID is required to test this dashboard.')
				return
			}
		}
		operationInFlight.current = true
		setTestedDashboardId(dashboard.id)
		setTestedResult(null)
		setError('')
		try {
			const end = new Date()
			setTestedResult(await getImportedDashboardPanels(dashboard.id, { test_id: testId || undefined, start: new Date(end.getTime() - 15 * 60_000).toISOString(), end: end.toISOString(), step_seconds: 15 }))
		} catch (err) {
			setError(getErrorMessage(err, 'Prometheus test failed.'))
		} finally {
			operationInFlight.current = false
		}
	}, [getImportedDashboardPanels])

	const handleEditBindings = useCallback(async (dashboard: ImportedDashboard) => {
		const current = dashboard.bindings ?? {}
		if (Object.keys(current).length === 0 || operationInFlight.current) return
		const next: Record<string, string> = {}
		for (const [name, value] of Object.entries(current)) {
			const edited = window.prompt(`Value for $${name}`, value)
			if (edited === null) return
			next[name] = edited.trim()
		}
		operationInFlight.current = true
		setMutatingDashboardId(dashboard.id)
		try {
			await patchImportedDashboard(dashboard.id, { bindings: next })
			setNotice('Dashboard bindings updated.')
			await refresh(projectId)
		} catch (err) {
			setError(getErrorMessage(err, 'Failed to update dashboard bindings.'))
		} finally {
			operationInFlight.current = false
			setMutatingDashboardId('')
		}
	}, [patchImportedDashboard, projectId, refresh])

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
            Imported dashboards need a configured Prometheus or Grafana datasource. Configure one first, or panels will render empty.
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
				setPreviewTestId('')
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
            <div className="setting-row">
              <div className="setting-info"><span className="setting-label">Import live Grafana UID</span><span className="setting-desc">Fetch a dashboard through the configured Grafana service account.</span></div>
              <div className="settings-inline-actions"><input className="settings-input cell-mono" aria-label="Grafana dashboard UID" value={grafanaUID} onChange={event => setGrafanaUID(event.target.value)} placeholder="dashboard-uid" /><button type="button" className="btn btn-secondary" disabled={controlsBusy || !grafanaUID.trim()} onClick={handleLiveUID}>Fetch</button></div>
            </div>
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
							<button type="button" className="btn btn-sm btn-secondary" disabled={controlsBusy} onClick={() => handleTestDashboard(dashboard)}>Test now</button>
							{Object.keys(dashboard.bindings ?? {}).length > 0 && <button type="button" className="btn btn-sm btn-secondary" disabled={controlsBusy} onClick={() => handleEditBindings(dashboard)}>Edit bindings</button>}
							<button type="button" className="btn btn-sm btn-ghost" disabled={controlsBusy} onClick={() => handleDelete(dashboard)} aria-label={`Delete ${dashboard.name}`}>Delete</button>
                  </div>
					{testedDashboardId === dashboard.id && testedResult?.dashboards.flatMap(result => result.panels).map(panel => (
						<PrometheusPanelCard key={panel.panel_id} title={panel.title} unit={panel.unit} kind={panel.kind} series={panel.series} value={panel.value} error={panel.error} thresholds={panel.thresholds} height={150} />
					))}
					{testedDashboardId === dashboard.id && testedResult && (!testedResult.enabled || !testedResult.configured) && <div className="settings-inline-error" role="status">The dashboard datasource is disabled or not fully configured.</div>}
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
              ({review.stats.line_charts} chart{review.stats.line_charts === 1 ? '' : 's'}, {review.stats.single_value_tiles} tile{review.stats.single_value_tiles === 1 ? '' : 's'}, {review.stats.tables ?? 0} table{review.stats.tables === 1 ? '' : 's'}, {review.stats.heatmaps ?? 0} heatmap{review.stats.heatmaps === 1 ? '' : 's'}).
            </p>

            <div className="imported-dashboards-review-panels">
			{reviewNeedsTestId && <div className="setting-row">
				<div className="setting-info"><span className="setting-label">Preview run ID</span><span className="setting-desc">Required because this dashboard filters by $test_id. The run must belong to the selected project.</span></div>
				<input className="settings-input cell-mono" aria-label="Preview run ID" value={previewTestId} onChange={event => setPreviewTestId(event.target.value)} placeholder="run-id" />
			</div>}
			{previewLoading && <div className="settings-inline-notice" role="status">Refreshing live preview…</div>}
			{preview && (!preview.enabled || !preview.configured) && <div className="settings-inline-warning" role="status">Live preview is unavailable because the dashboard datasource is disabled or its credential environment variable is not set.</div>}
			{preview?.dashboards.flatMap(dashboard => dashboard.panels).map(panel => (
				<PrometheusPanelCard key={`preview-${panel.panel_id}`} title={panel.title} unit={panel.unit} kind={panel.kind} series={panel.series} value={panel.value} error={panel.error} thresholds={panel.thresholds} height={160} />
			))}
			{(review.dashboard.panels ?? []).map((panel, panelIndex) => (
                <div key={`${panel.panel_id}-${panelIndex}`} className="imported-dashboards-review-panel">
				  <span className={`imported-dashboards-badge badge-${panel.kind}`}>{panelKindLabel(panel.kind)}</span>
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
                {requiredBindings.map(name => {
                  const definition = review.bindings?.find(binding => binding.name === name)
                  const options = bindingOptions[name] ?? definition?.options ?? []
                  return <div key={name} className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">${name}</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      aria-label={`Value for variable ${name}`}
                      value={bindingValues[name] ?? ''}
                      onChange={event => setBindingValues(prev => ({ ...prev, [name]: event.target.value }))}
                      placeholder="value"
                      list={options.length ? `grafana-binding-${name}` : undefined}
                    />
					{options.length > 0 && <datalist id={`grafana-binding-${name}`}>{options.map(option => <option value={option} key={option}>{option}</option>)}</datalist>}
                    {definition?.grafana_type === 'query' && /^label_values\s*\(/i.test(definition.query?.trim() ?? '') && <button type="button" className="btn btn-sm btn-secondary" disabled={Boolean(loadingBinding)} onClick={() => handleLoadBindingOptions(name)}>{loadingBinding === name ? 'Loading…' : 'Load options'}</button>}
                  </div>
                })}
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
              <button type="button" className="btn btn-secondary" onClick={() => { setMode('list'); setReview(null); setPreviewTestId('') }} disabled={busy}>Cancel</button>
              <button type="button" className="btn btn-primary" onClick={handleCommit} disabled={!canCommit || busy}>Import dashboard</button>
            </div>
          </div>
        ) : null}
      </div>
    </div>
  )
}

function panelKindLabel(kind: string) {
  switch (kind) {
    case 'line': return 'line chart'
    case 'single_value': return 'tile'
    case 'table': return 'table'
    case 'heatmap': return 'heatmap'
    default: return kind
  }
}

function dashboardUsesTestID(panels: ImportedDashboard['panels']): boolean {
	return panels.some(panel => (panel.targets ?? []).some(target => expressionUsesTestID(target.expr)))
}

function expressionUsesTestID(expression: string): boolean {
	return /\$test_id\b|\$\{test_id\}|\[\[test_id\]\]/.test(expression)
}
