import { useState } from 'react'

import { useSystemApi } from '../hooks/api/useSystemApi'
import type { DashboardPowerBISettings, PowerBITestConnectionResponse } from '../types/settings'
import { getErrorMessage } from '../utils/errorUtils'

type PowerBIIntegrationPanelProps = {
  value: DashboardPowerBISettings
  onChange: (next: DashboardPowerBISettings) => void
  canEdit?: boolean
}

const DEFAULT_AUTH_BASE_URL = 'https://login.microsoftonline.com'
const DEFAULT_API_BASE_URL = 'https://api.powerbi.com/v1.0/myorg'
const DEFAULT_DATASET_NAME = 'APEX Load Performance Runs'

export default function PowerBIIntegrationPanel({ value, onChange, canEdit = true }: PowerBIIntegrationPanelProps) {
  const systemApi = useSystemApi()
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<PowerBITestConnectionResponse | null>(null)
  const [testError, setTestError] = useState('')
  const [recreating, setRecreating] = useState(false)
  const [recreateNotice, setRecreateNotice] = useState('')
  const [confirmRecreate, setConfirmRecreate] = useState(false)

  const update = (patch: Partial<DashboardPowerBISettings>) => {
    // A connection-test result or recreate notice describes the previously
    // saved config; editing any field makes it stale.
    setTestResult(null)
    setTestError('')
    setRecreateNotice('')
    onChange({ ...value, ...patch })
  }

  const testConnection = async () => {
    setTesting(true)
    setTestError('')
    setTestResult(null)
    try {
      setTestResult(await systemApi.testPowerBIConnection())
    } catch (error) {
      setTestError(getErrorMessage(error, 'Connection test failed'))
    } finally {
      setTesting(false)
    }
  }

  const recreateDataset = async () => {
    setRecreating(true)
    setRecreateNotice('')
    setTestError('')
    try {
      const response = await systemApi.recreatePowerBIDataset()
      setRecreateNotice(`Dataset "${response.dataset_name}" recreated (schema v${response.schema_version}). Previously pushed rows in Power BI were deleted.`)
    } catch (error) {
      setTestError(getErrorMessage(error, 'Dataset recreate failed'))
    } finally {
      setRecreating(false)
      setConfirmRecreate(false)
    }
  }

  return (
    <div className="settings-subsection">
      <div className="settings-manager-summary">
        <div>
          <span className="setting-label">Power BI Publishing</span>
          <span className="setting-desc">
            Publish completed run results (run KPIs + per-transaction stats) to a Power BI push dataset for release-trend reporting. Publishing is manual, from a run&apos;s report page.
          </span>
        </div>
        <strong>{value.enabled ? 'Enabled' : 'Disabled'}</strong>
      </div>

      <div className="setting-row">
        <div className="setting-info">
          <span className="setting-label">Enable Power BI publishing</span>
          <span className="setting-desc">Shows the Publish to Power BI action on completed runs.</span>
        </div>
        <label className="settings-checkbox-label">
          <input
            type="checkbox"
            checked={value.enabled}
            disabled={!canEdit}
            onChange={event => update({ enabled: event.target.checked })}
          />
          <span>Enabled</span>
        </label>
      </div>

      <div className="setting-row">
        <div className="setting-info">
          <span className="setting-label">Azure AD tenant ID</span>
          <span className="setting-desc">Directory (tenant) ID of the app registration used as the publishing service principal.</span>
        </div>
        <input
          className="settings-input cell-mono"
          aria-label="Power BI tenant ID"
          value={value.tenant_id || ''}
          disabled={!canEdit}
          onChange={event => update({ tenant_id: event.target.value })}
          placeholder="00000000-0000-0000-0000-000000000000"
        />
      </div>

      <div className="setting-row">
        <div className="setting-info">
          <span className="setting-label">Client ID</span>
          <span className="setting-desc">Application (client) ID of the service principal. It needs Member access on the target workspace.</span>
        </div>
        <input
          className="settings-input cell-mono"
          aria-label="Power BI client ID"
          value={value.client_id || ''}
          disabled={!canEdit}
          onChange={event => update({ client_id: event.target.value })}
        />
      </div>

      <div className="setting-row">
        <div className="setting-info">
          <span className="setting-label">Client secret environment variable</span>
          <span className="setting-desc">Name of the controller environment variable holding the client secret. The secret itself is never stored.</span>
        </div>
        <input
          className="settings-input cell-mono"
          aria-label="Power BI client secret environment variable"
          value={value.client_secret_env_var || ''}
          disabled={!canEdit}
          onChange={event => update({ client_secret_env_var: event.target.value })}
          placeholder="APEXLOAD_POWERBI_CLIENT_SECRET"
        />
      </div>

      <div className="setting-row">
        <div className="setting-info">
          <span className="setting-label">Workspace ID</span>
          <span className="setting-desc">GUID of the Power BI workspace (group) that receives the dataset.</span>
        </div>
        <input
          className="settings-input cell-mono"
          aria-label="Power BI workspace ID"
          value={value.workspace_id || ''}
          disabled={!canEdit}
          onChange={event => update({ workspace_id: event.target.value })}
          placeholder="00000000-0000-0000-0000-000000000000"
        />
      </div>

      <div className="setting-row">
        <div className="setting-info">
          <span className="setting-label">Dataset name</span>
          <span className="setting-desc">Push dataset created on first publish if it does not exist.</span>
        </div>
        <input
          className="settings-input"
          aria-label="Power BI dataset name"
          value={value.dataset_name || ''}
          disabled={!canEdit}
          onChange={event => update({ dataset_name: event.target.value })}
          placeholder={DEFAULT_DATASET_NAME}
        />
      </div>

      <div className="setting-row">
        <div className="setting-info">
          <span className="setting-label">Authority URL</span>
          <span className="setting-desc">Azure AD login endpoint. Change only for sovereign or internal clouds.</span>
        </div>
        <input
          className="settings-input cell-mono"
          aria-label="Power BI authority URL"
          value={value.auth_base_url || ''}
          disabled={!canEdit}
          onChange={event => update({ auth_base_url: event.target.value })}
          placeholder={DEFAULT_AUTH_BASE_URL}
        />
      </div>

      <div className="setting-row">
        <div className="setting-info">
          <span className="setting-label">API base URL</span>
          <span className="setting-desc">Power BI REST API base. Change only for sovereign or internal deployments.</span>
        </div>
        <input
          className="settings-input cell-mono"
          aria-label="Power BI API base URL"
          value={value.api_base_url || ''}
          disabled={!canEdit}
          onChange={event => update({ api_base_url: event.target.value })}
          placeholder={DEFAULT_API_BASE_URL}
        />
      </div>

      <div className="setting-row">
        <div className="setting-info">
          <span className="setting-label">Dashboard base URL (optional)</span>
          <span className="setting-desc">Absolute base used for the deep links pushed into the dataset (e.g. https://apexload.example.com). When empty, links are derived from the publish request&apos;s host.</span>
        </div>
        <input
          className="settings-input cell-mono"
          aria-label="Power BI dashboard base URL"
          value={value.dashboard_base_url || ''}
          disabled={!canEdit}
          onChange={event => update({ dashboard_base_url: event.target.value })}
          placeholder="https://apexload.example.com"
        />
      </div>

      <div className="setting-row">
        <div className="setting-info">
          <span className="setting-label">Timeout (seconds)</span>
          <span className="setting-desc">Maximum backend wait per Power BI API call.</span>
        </div>
        <input
          className="settings-input settings-input-number"
          aria-label="Power BI timeout seconds"
          type="number"
          min={1}
          max={60}
          step={1}
          value={value.timeout_seconds ?? ''}
          placeholder="30"
          disabled={!canEdit}
          onChange={event => {
            // A cleared field means "use the default", not 0 — storing 0 would
            // trip validation against a value the user cannot see.
            const raw = event.target.value
            update({ timeout_seconds: raw === '' ? undefined : Number(raw) })
          }}
        />
      </div>

      <div className="setting-row">
        <div className="setting-info">
          <span className="setting-label">Connection</span>
          <span className="setting-desc">Save settings first — the test uses the stored configuration.</span>
        </div>
        <div className="settings-inline-actions">
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => void testConnection()} disabled={testing}>
            {testing ? 'Testing…' : 'Test connection'}
          </button>
        </div>
      </div>
      {testError && <div className="settings-error" role="alert">{testError}</div>}
      {testResult && (
        <div className="settings-manager-summary" data-testid="powerbi-test-result">
          <div>
            <span className="setting-label">{testResult.ok ? 'Connection OK' : 'Connection failed'}</span>
            <span className="setting-desc">
              {testResult.ok
                ? testResult.dataset_exists
                  ? `Dataset "${testResult.dataset_name}" found in the workspace.`
                  : `Workspace reachable. Dataset "${testResult.dataset_name}" will be created on first publish.`
                : testResult.error || 'Unknown error'}
            </span>
          </div>
        </div>
      )}

      <div className="setting-row">
        <div className="setting-info">
          <span className="setting-label">Recreate dataset</span>
          <span className="setting-desc">
            Deletes and recreates the push dataset with the current schema. All rows previously pushed to Power BI are permanently removed (publish history in APEX Load is kept). Use this only when a schema-drift error blocks publishing.
          </span>
        </div>
        <div className="settings-inline-actions">
          {!confirmRecreate ? (
            <button type="button" className="btn btn-danger-outline btn-sm" onClick={() => setConfirmRecreate(true)} disabled={recreating || !value.enabled}>
              Recreate dataset…
            </button>
          ) : (
            <>
              <button type="button" className="btn btn-danger-outline btn-sm" onClick={() => void recreateDataset()} disabled={recreating || !value.enabled}>
                {recreating ? 'Recreating…' : 'Confirm: delete Power BI rows and recreate'}
              </button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setConfirmRecreate(false)} disabled={recreating}>
                Cancel
              </button>
            </>
          )}
        </div>
      </div>
      {recreateNotice && <div className="settings-notice" role="status">{recreateNotice}</div>}
    </div>
  )
}
