import { confirmAction } from './ux/confirmAction'
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'

import type { ImportedDashboard, ImportedDashboardGraphsResponse, ImportedDashboardImportReview, Project } from '../types'
import { useSystemApi } from '../hooks/api/useSystemApi'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { getErrorMessage } from '../utils/errorUtils'
import ImportDropzone from './ImportDropzone'
import PrometheusDashboardAdoption from './PrometheusDashboardAdoption'
import PrometheusPanelCard from './PrometheusPanelCard'
import { listAllImportedDashboards } from './monitoringCatalogModel'

interface Props {
  resourceId?: string
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
export default function ImportedDashboardsPanel({ projects, defaultProjectId, prometheusConfigured, resourceId = '' }: Props) {
  const { listImportedDashboards, importGrafanaDashboard, importGrafanaDashboardByUID, commitGrafanaDashboardImport, previewGrafanaDashboardImport, resolveGrafanaImportLabelValues, getImportedDashboardPanels, deleteImportedDashboard, patchImportedDashboard } = useSystemApi()
  const { runGuarded } = useNavigationGuardRunner()

  const [projectId, setProjectId] = useState(defaultProjectId || projects[0]?.id || '')
  const [dashboards, setDashboards] = useState<ImportedDashboard[]>([])
  const [mode, setMode] = useState<PanelMode>('list')
  const [review, setReview] = useState<ImportedDashboardImportReview | null>(null)
  const [reviewProjectId, setReviewProjectId] = useState('')
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
	const scopeRef = useRef({ projectId, generation: 0 })
	if (scopeRef.current.projectId !== projectId) {
		scopeRef.current = { projectId, generation: scopeRef.current.generation + 1 }
	}
	const isCurrentScope = useCallback((scope: { projectId: string; generation: number }) => (
		scopeRef.current.projectId === scope.projectId && scopeRef.current.generation === scope.generation
	), [])
	const controlsBusy = busy || mutatingDashboardId !== ''
	const discardReview = useCallback(() => {
		setMode('list')
		setReview(null)
		setReviewProjectId('')
		setBindingValues({})
		setBindingOptions({})
		setPreview(null)
		setPreviewTestId('')
		setError('')
	}, [])
	const requestDiscardReview = useCallback(() => runGuarded(discardReview), [discardReview, runGuarded])

  const refresh = useCallback(async (pid: string) => {
		const generation = ++refreshGeneration.current
    if (!pid) {
      setDashboards([])
      return
    }
    try {
			const nextDashboards = await listAllImportedDashboards(listImportedDashboards, pid)
		if (generation === refreshGeneration.current) {
			setDashboards(nextDashboards)
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

	useLayoutEffect(() => {
		refreshGeneration.current += 1
		operationInFlight.current = false
		setDashboards([])
		setMode('list')
		setReview(null)
		setReviewProjectId('')
		setBindingValues({})
		setBindingOptions({})
		setLoadingBinding('')
		setPreview(null)
		setPreviewLoading(false)
		setPreviewTestId('')
		setTestedDashboardId('')
		setTestedResult(null)
		setBusy(false)
		setMutatingDashboardId('')
		setError('')
		setNotice('')
	}, [projectId])

  useEffect(() => {
    void refresh(projectId)
		return () => { refreshGeneration.current += 1 }
  }, [projectId, refresh])

	const activeReview = reviewProjectId === projectId ? review : null
  const requiredBindings = useMemo(() => activeReview?.required_bindings ?? [], [activeReview])
	const reviewNeedsTestId = useMemo(() => dashboardUsesTestID(activeReview?.dashboard.panels ?? []) || (activeReview?.bindings ?? []).some(binding => expressionUsesTestID(binding.query ?? '')), [activeReview])

	useEffect(() => {
		if (!activeReview || activeReview.has_blockers || !prometheusConfigured || (reviewNeedsTestId && !previewTestId.trim()) || !requiredBindings.every(name => (bindingValues[name] ?? '').trim())) {
			setPreview(null)
			setPreviewLoading(false)
			return
		}
		let cancelled = false
		const timer = window.setTimeout(async () => {
			setPreviewLoading(true)
			try {
				const end = new Date()
				const response = await previewGrafanaDashboardImport({ project_id: projectId, import_session_id: activeReview.import_session_id, bindings: bindingValues, test_id: previewTestId.trim() || undefined, start: new Date(end.getTime() - 15 * 60_000).toISOString(), end: end.toISOString(), step_seconds: 15 })
				if (!cancelled) setPreview(response)
			} catch (err) {
				if (!cancelled) setPreview({ enabled: true, configured: true, generated_at: new Date().toISOString(), dashboards: [{ dashboard_id: activeReview.import_session_id, dashboard_title: activeReview.dashboard.source_title, panels: (activeReview.dashboard.panels ?? []).map(panel => ({ ...panel, error: getErrorMessage(err, 'Preview unavailable.') })) }] })
			} finally {
				if (!cancelled) setPreviewLoading(false)
			}
		}, 500)
		return () => { cancelled = true; window.clearTimeout(timer) }
	}, [activeReview, bindingValues, previewGrafanaDashboardImport, previewTestId, projectId, prometheusConfigured, requiredBindings, reviewNeedsTestId])

	const handleFile = useCallback(async (file: File | null) => {
		if (!file) return
		if (operationInFlight.current) return
    if (!projectId) {
      setError('Select a project before importing a dashboard.')
      return
    }
		operationInFlight.current = true
		const scope = { ...scopeRef.current }
		refreshGeneration.current += 1
		setBusy(true)
    setError('')
    setNotice('')
    try {
      const content = await file.text()
		if (!isCurrentScope(scope)) return
      const result = await importGrafanaDashboard({ project_id: scope.projectId, name: file.name.replace(/\.json$/i, ''), content })
		if (!isCurrentScope(scope)) return
      setReview(result)
		setReviewProjectId(scope.projectId)
      setBindingValues({})
      setBindingOptions({})
		setPreviewTestId('')
      setMode('review')
    } catch (err) {
		if (isCurrentScope(scope)) setError(getErrorMessage(err, 'Failed to parse the dashboard JSON.'))
		} finally {
			if (isCurrentScope(scope)) {
				operationInFlight.current = false
				setBusy(false)
			}
    }
	}, [importGrafanaDashboard, isCurrentScope, projectId])

  const handleLoadBindingOptions = useCallback(async (name: string) => {
		const scopedReview = activeReview
    if (!scopedReview || loadingBinding) return
		const scope = { ...scopeRef.current }
    setLoadingBinding(name)
    setError('')
    try {
		const response = await resolveGrafanaImportLabelValues({ project_id: scope.projectId, import_session_id: scopedReview.import_session_id, variable: name, bindings: bindingValues, test_id: previewTestId.trim() || undefined })
		if (isCurrentScope(scope)) setBindingOptions(current => ({ ...current, [name]: response.options ?? [] }))
    } catch (err) {
		if (isCurrentScope(scope)) setError(getErrorMessage(err, 'Failed to resolve label values.'))
    } finally {
		if (isCurrentScope(scope)) setLoadingBinding('')
    }
	}, [activeReview, bindingValues, isCurrentScope, loadingBinding, previewTestId, resolveGrafanaImportLabelValues])

	const handleLiveUID = useCallback(async () => {
		if (!projectId || !grafanaUID.trim() || operationInFlight.current) return
		operationInFlight.current = true
		const scope = { ...scopeRef.current }
		setBusy(true); setError('')
		try {
			const result = await importGrafanaDashboardByUID({ project_id: scope.projectId, uid: grafanaUID.trim() })
			if (!isCurrentScope(scope)) return
			setReview(result); setReviewProjectId(scope.projectId); setBindingValues({}); setBindingOptions({}); setPreviewTestId(''); setMode('review')
		} catch (err) { if (isCurrentScope(scope)) setError(getErrorMessage(err, 'Failed to fetch Grafana dashboard.')) }
		finally {
			if (isCurrentScope(scope)) {
				operationInFlight.current = false
				setBusy(false)
			}
		}
	}, [grafanaUID, importGrafanaDashboardByUID, isCurrentScope, projectId])

	const handleCommit = useCallback(async (): Promise<boolean> => {
		const scopedReview = activeReview
		if (!scopedReview) return false
		if (scopedReview.has_blockers || !requiredBindings.every(name => (bindingValues[name] ?? '').trim())) {
			setError('Resolve the dashboard import blockers and required variables before importing.')
			return false
		}
		if (operationInFlight.current) return false
		operationInFlight.current = true
		const scope = { ...scopeRef.current }
    setBusy(true)
    setError('')
    try {
      const committed = await commitGrafanaDashboardImport({
				project_id: scope.projectId,
				import_session_id: scopedReview.import_session_id,
        bindings: bindingValues,
      })
			if (!isCurrentScope(scope)) return false
			setDashboards(current => [...current.filter(dashboard => dashboard.id !== committed.id), committed])
      setReview(null)
			setReviewProjectId('')
		setPreviewTestId('')
      setMode('list')
      setNotice('Dashboard imported.')
			await refresh(scope.projectId)
			return true
    } catch (err) {
			if (isCurrentScope(scope)) setError(getErrorMessage(err, 'Failed to commit the dashboard import.'))
			return false
		} finally {
			if (isCurrentScope(scope)) {
				operationInFlight.current = false
				setBusy(false)
			}
    }
	}, [activeReview, bindingValues, commitGrafanaDashboardImport, isCurrentScope, refresh, requiredBindings])

  const handleToggle = useCallback(async (dashboard: ImportedDashboard) => {
		if (operationInFlight.current) return
		operationInFlight.current = true
		setMutatingDashboardId(dashboard.id)
    setError('')
    try {
			const saved = await patchImportedDashboard(dashboard.id, { enabled: !dashboard.enabled })
			setDashboards(current => current.map(candidate => candidate.id === saved.id ? saved : candidate))
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
		if (!(await confirmAction(`Delete imported dashboard "${dashboard.name}"? This cannot be undone.`, { title: 'Delete imported dashboard', confirmLabel: 'Delete', destructive: true }))) return
		operationInFlight.current = true
		setMutatingDashboardId(dashboard.id)
    setError('')
    try {
      await deleteImportedDashboard(dashboard.id)
			setDashboards(current => current.filter(candidate => candidate.id !== dashboard.id))
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
			const saved = await patchImportedDashboard(dashboard.id, { bindings: next })
			setDashboards(current => current.map(candidate => candidate.id === saved.id ? saved : candidate))
			setNotice('Dashboard bindings updated.')
			await refresh(projectId)
		} catch (err) {
			setError(getErrorMessage(err, 'Failed to update dashboard bindings.'))
		} finally {
			operationInFlight.current = false
			setMutatingDashboardId('')
		}
	}, [patchImportedDashboard, projectId, refresh])

	const reviewDirty = activeReview !== null
	const reviewDirtyRef = useRef(reviewDirty)
	reviewDirtyRef.current = reviewDirty
	const handleCommitRef = useRef(handleCommit)
	handleCommitRef.current = handleCommit
	const importNavigationGuard = useMemo<NavigationGuard>(() => ({
		id: 'imported-dashboard-review',
		label: 'Imported dashboard review',
		isDirty: () => reviewDirtyRef.current,
		save: () => handleCommitRef.current(),
		discard: discardReview,
	}), [discardReview])
	useNavigationGuard(importNavigationGuard, reviewDirty)

  const canCommit = activeReview !== null && !activeReview.has_blockers && requiredBindings.every(name => (bindingValues[name] ?? '').trim() !== '')

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
				const nextProjectId = event.target.value
				runGuarded(() => setProjectId(nextProjectId))
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
              {resourceId && <p role="status">Selected dashboard: {resourceId}. {!dashboards.some(item => item.id === resourceId) && 'This dashboard is not available in the selected project.'} <a href={window.location.hash.replace(/([?&])resourceId=[^&]*&?/, '$1').replace(/[?&]$/, '')}>Show all dashboards</a></p>}
              {dashboards.filter(dashboard => !resourceId || dashboard.id === resourceId).map(dashboard => (
                <li key={dashboard.id} className="imported-dashboards-item">
                  <div className="imported-dashboards-item-main">
                    <strong>{dashboard.name}</strong>
										<span className="setting-desc">{(dashboard.panels ?? []).length} panel{(dashboard.panels ?? []).length === 1 ? '' : 's'}</span>
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
                  <PrometheusDashboardAdoption key={`${projectId}:${dashboard.id}`} dashboard={dashboard} projectId={projectId} />
					{testedDashboardId === dashboard.id && testedResult?.dashboards.flatMap(result => result.panels).map(panel => (
						<PrometheusPanelCard key={panel.panel_id} title={panel.title} unit={panel.unit} kind={panel.kind} series={panel.series} value={panel.value} error={panel.error} thresholds={panel.thresholds} height={150} />
					))}
					{testedDashboardId === dashboard.id && testedResult && (!testedResult.enabled || !testedResult.configured) && <div className="settings-inline-error" role="status">The dashboard datasource is disabled or not fully configured.</div>}
                </li>
              ))}
              {dashboards.length === 0 && projectId && <li className="setting-desc">No dashboards imported yet.</li>}
            </ul>
          </>
        ) : activeReview ? (
          <div className="imported-dashboards-review">
            <h4>Review import{activeReview.dashboard.source_title ? `: ${activeReview.dashboard.source_title}` : ''}</h4>
            <p className="setting-desc">
              {activeReview.stats.panels_converted} of {activeReview.stats.panels_seen} panels importable
              ({activeReview.stats.line_charts} chart{activeReview.stats.line_charts === 1 ? '' : 's'}, {activeReview.stats.single_value_tiles} tile{activeReview.stats.single_value_tiles === 1 ? '' : 's'}, {activeReview.stats.tables ?? 0} table{activeReview.stats.tables === 1 ? '' : 's'}, {activeReview.stats.heatmaps ?? 0} heatmap{activeReview.stats.heatmaps === 1 ? '' : 's'}).
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
			{(activeReview.dashboard.panels ?? []).map((panel, panelIndex) => (
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
									const definition = activeReview.bindings?.find(binding => binding.name === name)
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

						{activeReview.review_sections && activeReview.review_sections.length > 0 && (
              <div className="imported-dashboards-unsupported">
								{activeReview.review_sections.map(section => (
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

						{activeReview.has_blockers && (
              <div className="settings-inline-error" role="alert">This dashboard has blocking issues and cannot be imported until they are resolved.</div>
            )}

            <div className="imported-dashboards-review-actions">
								<button type="button" className="btn btn-secondary" onClick={requestDiscardReview} disabled={busy}>Cancel</button>
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
