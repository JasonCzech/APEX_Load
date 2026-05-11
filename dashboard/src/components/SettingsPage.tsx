import { useEffect, useMemo, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { DashboardSystemSettings, HealthResponse } from '../types'

const DEFAULT_SETTINGS: DashboardSystemSettings = {
  general: {
    api_endpoint: ':8080',
    max_concurrent_tests: 10,
    metrics_interval_seconds: 1,
  },
  thresholds: {
    max_error_rate_pct: undefined,
    max_p99_latency_ms: undefined,
  },
  capacity_planning: {
    headroom_pct: 20,
    generator: {
      cpu_cores: 4,
      memory_mb: 8192,
      network_mbps: 1000,
      max_vusers: 10000,
    },
    availability: {
      source: 'configured',
      configured_available_generators: 1,
    },
  },
}

function parseOptionalNumber(value: string): number | undefined {
  const trimmed = value.trim()
  if (!trimmed) return undefined
  const parsed = Number(trimmed)
  return Number.isFinite(parsed) ? parsed : undefined
}

function validateSettings(settings: DashboardSystemSettings): string[] {
  const errors: string[] = []
  if (!settings.general.api_endpoint.trim()) {
    errors.push('API endpoint is required.')
  }
  if (!Number.isInteger(settings.general.max_concurrent_tests) || settings.general.max_concurrent_tests < 1) {
    errors.push('Max concurrent tests must be an integer >= 1.')
  }
  if (!Number.isInteger(settings.general.metrics_interval_seconds) || settings.general.metrics_interval_seconds < 1) {
    errors.push('Metrics interval must be an integer >= 1 second.')
  }
  if (settings.thresholds.max_error_rate_pct !== undefined) {
    if (settings.thresholds.max_error_rate_pct < 0 || settings.thresholds.max_error_rate_pct > 100) {
      errors.push('Max error rate must be between 0 and 100%.')
    }
  }
  if (settings.thresholds.max_p99_latency_ms !== undefined && settings.thresholds.max_p99_latency_ms <= 0) {
    errors.push('Max P99 latency must be greater than 0 ms.')
  }
  if (settings.capacity_planning.headroom_pct < 0 || settings.capacity_planning.headroom_pct >= 100) {
    errors.push('Capacity planning headroom must be between 0 and 100%.')
  }
  if (settings.capacity_planning.generator.cpu_cores <= 0) {
    errors.push('Generator CPU cores must be greater than 0.')
  }
  if (!Number.isInteger(settings.capacity_planning.generator.memory_mb) || settings.capacity_planning.generator.memory_mb < 1) {
    errors.push('Generator memory must be an integer >= 1 MB.')
  }
  if (settings.capacity_planning.generator.network_mbps <= 0) {
    errors.push('Generator network throughput must be greater than 0 Mbps.')
  }
  if (!Number.isInteger(settings.capacity_planning.generator.max_vusers) || settings.capacity_planning.generator.max_vusers < 1) {
    errors.push('Generator max VUsers must be an integer >= 1.')
  }
  if (!Number.isInteger(settings.capacity_planning.availability.configured_available_generators) || settings.capacity_planning.availability.configured_available_generators < 1) {
    errors.push('Configured available generators must be an integer >= 1.')
  }
  return errors
}

export default function SettingsPage() {
  const api = useApi()
  const [settings, setSettings] = useState<DashboardSystemSettings>(DEFAULT_SETTINGS)
  const [health, setHealth] = useState<HealthResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [saveMessage, setSaveMessage] = useState('')
  const [requestError, setRequestError] = useState('')

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      setLoading(true)
      setRequestError('')
      try {
        const response = await api.getSystemSettings()
        if (!cancelled) setSettings(response)
      } catch (error: any) {
        if (!cancelled) {
          setRequestError(error.message || 'Unable to load system settings')
          setSettings(DEFAULT_SETTINGS)
        }
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => {
      cancelled = true
    }
  }, [api])

  useEffect(() => {
    let cancelled = false
    const loadHealth = async () => {
      try {
        const response = await api.getHealth()
        if (!cancelled) setHealth(response)
      } catch {
        if (!cancelled) setHealth(null)
      }
    }
    loadHealth()
    const interval = setInterval(loadHealth, 5000)
    return () => {
      cancelled = true
      clearInterval(interval)
    }
  }, [api])

  const validationErrors = useMemo(() => validateSettings(settings), [settings])
  const canSave = validationErrors.length === 0 && !loading
  const availabilitySnapshot = health?.capacity_availability
  const availabilitySourceLabel = availabilitySnapshot?.source === 'configured_plus_live'
    ? availabilitySnapshot.live_available_generators !== undefined
      ? 'Live snapshot + configured ceiling'
      : 'Configured ceiling only'
    : 'Configured inventory only'

  const updateGeneral = (key: keyof DashboardSystemSettings['general'], value: string) => {
    setSettings((prev) => ({
      ...prev,
      general: {
        ...prev.general,
        [key]: key === 'api_endpoint' ? value : Math.max(0, Number(value) || 0),
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updateThreshold = (key: keyof DashboardSystemSettings['thresholds'], value: string) => {
    setSettings((prev) => ({
      ...prev,
      thresholds: {
        ...prev.thresholds,
        [key]: parseOptionalNumber(value),
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updateCapacityPlanning = (
    key: keyof DashboardSystemSettings['capacity_planning'],
    value: string,
  ) => {
    setSettings((prev) => ({
      ...prev,
      capacity_planning: {
        ...prev.capacity_planning,
        [key]: key === 'availability' || key === 'generator' ? prev.capacity_planning[key] : Number(value),
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updateGenerator = (key: keyof DashboardSystemSettings['capacity_planning']['generator'], value: string) => {
    setSettings((prev) => ({
      ...prev,
      capacity_planning: {
        ...prev.capacity_planning,
        generator: {
          ...prev.capacity_planning.generator,
          [key]: Math.max(0, Number(value) || 0),
        },
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updateAvailability = (key: keyof DashboardSystemSettings['capacity_planning']['availability'], value: string) => {
    setSettings((prev) => ({
      ...prev,
      capacity_planning: {
        ...prev.capacity_planning,
        availability: {
          ...prev.capacity_planning.availability,
          [key]: key === 'source' ? value : Math.max(0, Number(value) || 0),
        },
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const saveSettings = async () => {
    if (!canSave) return
    try {
      const response = await api.updateSystemSettings(settings)
      setSettings(response)
      setSaveMessage('Settings saved.')
      setRequestError('')
    } catch (error: any) {
      setRequestError(error.message || 'Unable to save settings')
      setSaveMessage('')
    }
  }

  const resetSettings = async () => {
    try {
      const response = await api.updateSystemSettings(DEFAULT_SETTINGS)
      setSettings(response)
      setSaveMessage('Settings reset to defaults.')
      setRequestError('')
    } catch (error: any) {
      setRequestError(error.message || 'Unable to reset settings')
      setSaveMessage('')
    }
  }

  return (
    <div className="page settings-page">
      <div className="page-header">
        <div>
          <h2>Settings</h2>
          <p className="page-subtitle">Configuration, thresholds, and integrations</p>
        </div>
        <div className="settings-actions">
          <button className="btn btn-secondary" onClick={resetSettings}>
            Reset
          </button>
          <button className="btn btn-primary" disabled={!canSave} onClick={saveSettings}>
            Save
          </button>
        </div>
      </div>

      {loading && <p className="settings-notice">Loading settings...</p>}
      {!loading && saveMessage && <p className="settings-notice">{saveMessage}</p>}
      {requestError && <p className="settings-error-inline">{requestError}</p>}
      {validationErrors.length > 0 && (
        <div className="settings-errors" role="alert">
          {validationErrors.map((error) => (
            <p key={error}>{error}</p>
          ))}
        </div>
      )}

      <div className="settings-grid">
        <div className="settings-card">
          <h3>General</h3>
          <div className="settings-group">
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">API Endpoint</span>
                <span className="setting-desc">Controller API server address</span>
              </div>
              <input
                className="settings-input cell-mono"
                value={settings.general.api_endpoint}
                onChange={(e) => updateGeneral('api_endpoint', e.target.value)}
                placeholder=":8080"
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Concurrent Tests</span>
                <span className="setting-desc">Maximum tests running simultaneously</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.general.max_concurrent_tests}
                onChange={(e) => updateGeneral('max_concurrent_tests', e.target.value)}
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Metrics Interval (seconds)</span>
                <span className="setting-desc">How often metrics are broadcast via WebSocket</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.general.metrics_interval_seconds}
                onChange={(e) => updateGeneral('metrics_interval_seconds', e.target.value)}
              />
            </div>
          </div>
        </div>

        <div className="settings-card">
          <h3>Performance Thresholds</h3>
          <div className="settings-group">
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Error Rate (%)</span>
                <span className="setting-desc">Optional: alert/stop threshold for high error rate</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={0}
                max={100}
                step={0.1}
                value={settings.thresholds.max_error_rate_pct ?? ''}
                onChange={(e) => updateThreshold('max_error_rate_pct', e.target.value)}
                placeholder="Not configured"
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Response Time (P99 ms)</span>
                <span className="setting-desc">Optional: alert threshold for P99 latency</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.thresholds.max_p99_latency_ms ?? ''}
                onChange={(e) => updateThreshold('max_p99_latency_ms', e.target.value)}
                placeholder="Not configured"
              />
            </div>
          </div>
        </div>

        <div className="settings-card">
          <h3>Capacity Planning</h3>
          <div className="settings-group">
            <div className="settings-status-block">
              <div className="settings-status-header">
                <span className="settings-status-title">Current Availability Snapshot</span>
                <span className={`settings-status-badge ${availabilitySnapshot ? 'ready' : 'unknown'}`}>
                  {availabilitySnapshot ? 'Live status' : 'Unavailable'}
                </span>
              </div>
              <div className="settings-status-grid">
                <div className="settings-status-item">
                  <span className="settings-status-label">Source</span>
                  <strong>{availabilitySourceLabel}</strong>
                </div>
                <div className="settings-status-item">
                  <span className="settings-status-label">Effective Available</span>
                  <strong>{availabilitySnapshot?.effective_available_generators ?? settings.capacity_planning.availability.configured_available_generators}</strong>
                </div>
                <div className="settings-status-item">
                  <span className="settings-status-label">Configured Ceiling</span>
                  <strong>{availabilitySnapshot?.configured_available_generators ?? settings.capacity_planning.availability.configured_available_generators}</strong>
                </div>
                <div className="settings-status-item">
                  <span className="settings-status-label">Live Snapshot</span>
                  <strong>{availabilitySnapshot?.live_available_generators ?? 'Not provided'}</strong>
                </div>
              </div>
              <p className="settings-status-copy">
                Advisory calculations and pre-start checks use this snapshot when availability source is set to configured plus live.
              </p>
              {availabilitySnapshot?.warnings && availabilitySnapshot.warnings.length > 0 && (
                <div className="settings-status-warnings" role="status">
                  {availabilitySnapshot.warnings.map((warning) => (
                    <p key={warning}>{warning}</p>
                  ))}
                </div>
              )}
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Headroom (%)</span>
                <span className="setting-desc">Reserve spare generator capacity when estimating pod requirements</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={0}
                max={99}
                step={1}
                value={settings.capacity_planning.headroom_pct}
                onChange={(e) => updateCapacityPlanning('headroom_pct', e.target.value)}
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Generator CPU (cores)</span>
                <span className="setting-desc">Baseline CPU available per load generator pod or machine</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={0.1}
                step={0.1}
                value={settings.capacity_planning.generator.cpu_cores}
                onChange={(e) => updateGenerator('cpu_cores', e.target.value)}
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Generator Memory (MB)</span>
                <span className="setting-desc">Baseline memory available per load generator pod or machine</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={256}
                value={settings.capacity_planning.generator.memory_mb}
                onChange={(e) => updateGenerator('memory_mb', e.target.value)}
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Generator Network (Mbps)</span>
                <span className="setting-desc">Estimated network throughput budget per load generator</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={50}
                value={settings.capacity_planning.generator.network_mbps}
                onChange={(e) => updateGenerator('network_mbps', e.target.value)}
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Generator Max VUsers</span>
                <span className="setting-desc">Safety ceiling for VUsers that one generator should carry before headroom</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={100}
                value={settings.capacity_planning.generator.max_vusers}
                onChange={(e) => updateGenerator('max_vusers', e.target.value)}
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Availability Source</span>
                <span className="setting-desc">Choose whether capacity advice should stop at configured inventory or also honor the latest live generator snapshot</span>
              </div>
              <select
                className="settings-input"
                value={settings.capacity_planning.availability.source}
                onChange={(e) => updateAvailability('source', e.target.value)}
              >
                <option value="configured">Configured generators only</option>
                <option value="configured_plus_live">Configured plus live availability</option>
              </select>
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Configured Available Generators</span>
                <span className="setting-desc">Configured inventory ceiling. When live snapshots are enabled, the effective available count is the lower of this value and the latest runtime snapshot.</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.capacity_planning.availability.configured_available_generators}
                onChange={(e) => updateAvailability('configured_available_generators', e.target.value)}
              />
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
