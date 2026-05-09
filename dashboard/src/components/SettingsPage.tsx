export default function SettingsPage() {
  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Settings</h2>
          <p className="page-subtitle">Configuration, thresholds, and integrations</p>
        </div>
      </div>

      <div className="settings-grid">
        <div className="settings-card">
          <h3>General</h3>
          <div className="settings-group">
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">API Endpoint</span>
                <span className="setting-desc">Controller API server address</span>
              </div>
              <span className="setting-value cell-mono">:8080</span>
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Concurrent Tests</span>
                <span className="setting-desc">Maximum tests running simultaneously</span>
              </div>
              <span className="setting-value">10</span>
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Metrics Interval</span>
                <span className="setting-desc">How often metrics are broadcast via WebSocket</span>
              </div>
              <span className="setting-value">1s</span>
            </div>
          </div>
        </div>

        <div className="settings-card">
          <h3>Performance Thresholds</h3>
          <div className="settings-group">
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Error Rate</span>
                <span className="setting-desc">Auto-stop test if error rate exceeds this</span>
              </div>
              <span className="setting-value text-muted">Not configured</span>
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Response Time (P99)</span>
                <span className="setting-desc">Alert threshold for P99 latency</span>
              </div>
              <span className="setting-value text-muted">Not configured</span>
            </div>
          </div>
        </div>

        <div className="settings-card">
          <h3>Integrations</h3>
          <div className="settings-group">
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Prometheus</span>
                <span className="setting-desc">Export metrics to Prometheus endpoint</span>
              </div>
              <span className="setting-value text-muted">Phase 2</span>
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Dynatrace</span>
                <span className="setting-desc">Push metrics to Dynatrace via OpenTelemetry</span>
              </div>
              <span className="setting-value text-muted">Phase 2</span>
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">ELK Stack</span>
                <span className="setting-desc">Send logs and metrics to Elasticsearch</span>
              </div>
              <span className="setting-value text-muted">Phase 2</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
