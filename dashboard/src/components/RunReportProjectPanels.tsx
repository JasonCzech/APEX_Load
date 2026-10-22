import { useEffect, useRef, useState } from 'react'
import { useDashboardDefinitionsApi, type DashboardDefinition } from '../hooks/api/useDashboardDefinitionsApi'
import type { MetricsHistoryPoint } from '../types'
import { NativePanelAuthoring, NativeProjectPanels } from './NativeAnalyticsPanel'
import LoadingState from './ux/LoadingState'
import EmptyState from './ux/EmptyState'

// Key this component by run/project so in-flight work cannot cross report scope.
export default function RunReportProjectPanels({ testId, projectId, history, properties, canManage }: { testId: string; projectId: string; history: MetricsHistoryPoint[]; properties: string[]; canManage: boolean }) {
  const { listDashboardDefinitions, createDashboardDefinition } = useDashboardDefinitionsApi()
  const [definitions, setDefinitions] = useState<DashboardDefinition[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const lifetime = useRef<AbortController | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    lifetime.current = controller
    setLoading(true); setError(''); setDefinitions([])
    void listDashboardDefinitions(projectId, { signal: controller.signal }).then(result => {
      if (!controller.signal.aborted) setDefinitions(result.dashboards)
    }).catch(reason => {
      if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Unable to load project panels')
    }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [projectId, listDashboardDefinitions])
  return <section aria-label="Archived project analytics">
    <h2>Project analytics</h2>
    {loading ? <LoadingState what="project panels" inline /> : error ? <p role="alert">{error}</p> : definitions.length ? <NativeProjectPanels definitions={definitions} history={history} testId={testId} /> : <EmptyState title="No project panels yet." hint={canManage ? 'Create a project analytics panel below.' : 'Ask a project editor to create a panel.'} compact />}
    {canManage && !loading && !error && <NativePanelAuthoring runId={testId} properties={properties} onSave={async definition => {
      const controller = lifetime.current
      if (!controller || controller.signal.aborted) throw new Error('Report scope changed; reopen the panel editor')
      const created = await createDashboardDefinition({ ...definition, project_id: projectId, visibility: 'project' }, { signal: controller.signal })
      if (!controller.signal.aborted) setDefinitions(current => [...current, created])
    }} />}
  </section>
}
