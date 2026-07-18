import type { TestSummary } from '../../types'
import { useImportedDashboardGraphs } from '../useImportedDashboardGraphs'
import PrometheusPanelCard from '../PrometheusPanelCard'

interface Props {
  test: TestSummary | null
}

// ImportedDashboardPanels renders the imported Grafana dashboard panels captured for a
// completed run. It reads the frozen snapshot through the same aggregate endpoint the
// live Command Center uses — for a terminal test that endpoint returns the stored
// snapshot rather than re-querying Prometheus — so results survive Prometheus retention
// and later edits to the dashboard definitions. It renders nothing when the run had no
// imported dashboards.
export default function ImportedDashboardPanels({ test }: Props) {
  const { importedDashboards, importedDashboardsLoading, importedDashboardsError } = useImportedDashboardGraphs(
    test,
    [],
    test?.project_id,
  )

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
            <div className="imported-dashboards-report-grid">
              {dashboard.panels.map(panel => (
                <PrometheusPanelCard
                  key={panel.panel_id}
                  title={panel.title}
                  unit={panel.unit}
                  kind={panel.kind}
                  series={panel.series}
                  value={panel.value}
                  error={panel.error}
                />
              ))}
            </div>
          </div>
        ))
      )}
    </section>
  )
}
