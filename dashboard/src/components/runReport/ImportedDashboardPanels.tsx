import type { CSSProperties } from 'react'
import type { ImportedDashboardResolvedPanel, TestSummary } from '../../types'
import { useImportedDashboardGraphs } from '../useImportedDashboardGraphs'
import PrometheusPanelCard from '../PrometheusPanelCard'

interface Props {
  test: TestSummary | null
  baselineTest?: TestSummary | null
}

// ImportedDashboardPanels renders the imported Grafana dashboard panels captured for a
// completed run. It reads the frozen snapshot through the same aggregate endpoint the
// live Command Center uses — for a terminal test that endpoint returns the stored
// snapshot rather than re-querying Prometheus — so results survive Prometheus retention
// and later edits to the dashboard definitions. It renders nothing when the run had no
// imported dashboards.
export default function ImportedDashboardPanels({ test, baselineTest }: Props) {
  const { importedDashboards, importedDashboardsLoading, importedDashboardsError } = useImportedDashboardGraphs(
    test,
    [],
    test?.project_id,
  )
  const { importedDashboards: baselineDashboards } = useImportedDashboardGraphs(baselineTest ?? null, [], baselineTest?.project_id)

  const dashboardsWithPanels = importedDashboards.filter(dashboard => dashboard.panels.length > 0)
  if (!importedDashboardsLoading && !importedDashboardsError && dashboardsWithPanels.length === 0) {
    return null
  }

  return (
    <section className="run-report-panel imported-dashboards-report" aria-label="Imported dashboards">
      <div className="run-report-section-heading">
        <div>
          <h2>Imported Dashboards</h2>
          <p>Grafana panels captured over the run window.</p>
        </div>
      </div>
      {importedDashboardsError && dashboardsWithPanels.length === 0 ? (
        <div className="settings-inline-error" role="alert">{importedDashboardsError}</div>
      ) : importedDashboardsLoading && dashboardsWithPanels.length === 0 ? (
        <div className="chart-empty-state" role="status">Loading imported dashboards…</div>
      ) : (
        dashboardsWithPanels.map(dashboard => (
          <div key={dashboard.dashboard_id} className="imported-dashboards-report-group">
            {dashboard.dashboard_title ? <h3>{dashboard.dashboard_title}</h3> : null}
            <div className="imported-dashboards-report-grid imported-dashboards-report-grid-faithful">
              {dashboard.panels.map(panel => (
                <div key={panel.panel_id} className="imported-dashboard-grid-cell" style={panelGridStyle(panel)}>
                  <PrometheusPanelCard title={panel.title} unit={panel.unit} kind={panel.kind} series={panel.series} value={panel.value} error={panel.error} thresholds={panel.thresholds} baselineSeries={baselineDashboards.find(item => item.dashboard_id === dashboard.dashboard_id)?.panels.find(item => item.panel_id === panel.panel_id)?.series} baselineValue={baselineDashboards.find(item => item.dashboard_id === dashboard.dashboard_id)?.panels.find(item => item.panel_id === panel.panel_id)?.value} height={Math.max(140, Math.min(460, (panel.grid_pos?.h ?? 8) * 22))} />
                </div>
              ))}
            </div>
          </div>
        ))
      )}
    </section>
  )
}

function panelGridStyle(panel: ImportedDashboardResolvedPanel): CSSProperties {
  const x = Math.max(0, Math.min(23, panel.grid_pos?.x ?? 0))
  const y = Math.max(0, Math.min(512, panel.grid_pos?.y ?? 0))
  const width = Math.max(1, Math.min(24 - x, panel.grid_pos?.w ?? 12))
  const height = Math.max(1, Math.min(26, panel.grid_pos?.h ?? 8))
  return { gridColumn: `${x + 1} / span ${width}`, gridRow: `${y + 1} / span ${height}`, minHeight: Math.max(160, Math.min(520, height * 24)) }
}
