import { useCallback, useEffect, useMemo, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { DashboardServiceAccount, Project } from '../types'
import { getErrorMessage } from '../utils/errorUtils'

type AccessPageProps = {
  projects: Project[]
  activeProject?: Project | null
  canManageServiceAccounts: boolean
}

type PurposePreset = {
  id: string
  label: string
  description: string
  defaultName: string
  globalRoles: string[]
  scopes: string[]
  projectRole?: string
}

type ServiceAccountForm = {
  purpose: string
  name: string
  description: string
  globalRoles: string[]
  projectRoles: Record<string, string>
  scopes: string[]
  expiresAt: string
  rotationDays: string
}

type IssuedKeyState = {
  key: string
  accountName: string
  mode: 'created' | 'rotated'
}

const PURPOSE_PRESETS: PurposePreset[] = [
  {
    id: 'reporting',
    label: 'Read-only reporting',
    description: 'View projects, system health, reports, and archived results.',
    defaultName: 'Reporting Export',
    globalRoles: ['viewer'],
    scopes: ['results:read', 'system:read'],
    projectRole: 'viewer',
  },
  {
    id: 'ci',
    label: 'CI runner',
    description: 'Run tests, read results, and update scripts in the selected project.',
    defaultName: 'CI Runner',
    globalRoles: ['viewer'],
    scopes: ['tests:run', 'results:read', 'scripts:write'],
    projectRole: 'editor',
  },
  {
    id: 'script_importer',
    label: 'Script importer',
    description: 'Import and update script assets without broad system administration.',
    defaultName: 'Script Importer',
    globalRoles: ['viewer'],
    scopes: ['scripts:write', 'results:read'],
    projectRole: 'editor',
  },
  {
    id: 'mcp',
    label: 'MCP automation',
    description: 'Use the FastMCP bridge for agent-assisted project automation.',
    defaultName: 'MCP Automation',
    globalRoles: ['viewer'],
    scopes: ['automation:*'],
    projectRole: 'editor',
  },
  {
    id: 'system_admin',
    label: 'System admin automation',
    description: 'Full automation for platform administration and break-glass operations.',
    defaultName: 'System Admin Automation',
    globalRoles: ['system_admin'],
    scopes: ['automation:*'],
  },
]

const GLOBAL_ROLE_OPTIONS = [
  { id: 'viewer', label: 'Viewer' },
  { id: 'editor', label: 'Editor' },
  { id: 'project_admin', label: 'Project Admin' },
  { id: 'system_admin', label: 'System Admin' },
]

const PROJECT_ROLE_OPTIONS = [
  { id: 'viewer', label: 'Viewer' },
  { id: 'editor', label: 'Editor' },
  { id: 'project_admin', label: 'Project Admin' },
]

const SCOPE_OPTIONS = [
  { id: 'results:read', label: 'Results read' },
  { id: 'system:read', label: 'System read' },
  { id: 'tests:run', label: 'Tests run' },
  { id: 'scripts:write', label: 'Scripts write' },
  { id: 'settings:manage', label: 'Settings manage' },
  { id: 'service_accounts:manage', label: 'Service accounts manage' },
  { id: 'audit:read', label: 'Audit read' },
  { id: 'automation:*', label: 'Automation wildcard' },
]

const ROLE_LABELS: Record<string, string> = {
  viewer: 'Viewer',
  editor: 'Editor',
  project_admin: 'Project Admin',
  system_admin: 'System Admin',
}

function presetByID(id: string): PurposePreset {
  return PURPOSE_PRESETS.find(preset => preset.id === id) || PURPOSE_PRESETS[1]
}

function buildPresetForm(preset: PurposePreset, activeProjectID?: string): ServiceAccountForm {
  const projectRoles: Record<string, string> = {}
  if (activeProjectID && preset.projectRole) {
    projectRoles[activeProjectID] = preset.projectRole
  }
  return {
    purpose: preset.id,
    name: preset.defaultName,
    description: preset.description,
    globalRoles: preset.globalRoles,
    projectRoles,
    scopes: preset.scopes,
    expiresAt: '',
    rotationDays: '90',
  }
}

function toggleValue(values: string[], value: string): string[] {
  return values.includes(value)
    ? values.filter(item => item !== value)
    : [...values, value]
}

function toProjectRolesPayload(projectRoles: Record<string, string>): Record<string, string[]> {
  return Object.entries(projectRoles).reduce<Record<string, string[]>>((acc, [projectID, role]) => {
    if (projectID && role) {
      acc[projectID] = [role]
    }
    return acc
  }, {})
}

function parseOptionalDateTime(value: string): string | undefined {
  const trimmed = value.trim()
  if (!trimmed) return undefined
  const parsed = new Date(trimmed)
  return Number.isNaN(parsed.getTime()) ? undefined : parsed.toISOString()
}

function parseRotationDays(value: string): number {
  const parsed = Number(value)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : 90
}

function formatTimestamp(value?: string): string {
  if (!value) return 'Never'
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString()
}

function formatRoleList(roles?: string[]): string {
  if (!roles || roles.length === 0) return 'None'
  return roles.map(role => ROLE_LABELS[role] || role).join(', ')
}

function formatProjectRoles(projectRoles?: Record<string, string[]>): string {
  const entries = Object.entries(projectRoles || {})
  if (entries.length === 0) return 'None'
  return entries
    .map(([projectID, roles]) => `${projectID}: ${formatRoleList(roles)}`)
    .join(' | ')
}

function accountBadges(account: DashboardServiceAccount): { label: string; tone: string }[] {
  const now = Date.now()
  const expiresAt = account.expires_at ? Date.parse(account.expires_at) : Number.NaN
  const rotationDueAt = account.rotation_due_at ? Date.parse(account.rotation_due_at) : Number.NaN
  const badges: { label: string; tone: string }[] = []

  if (!account.active) {
    badges.push({ label: 'Disabled', tone: 'danger' })
  } else if (!Number.isNaN(expiresAt) && expiresAt <= now) {
    badges.push({ label: 'Expired', tone: 'danger' })
  } else {
    badges.push({ label: 'Active', tone: 'healthy' })
  }
  if (!account.last_used_at) {
    badges.push({ label: 'Never used', tone: 'warning' })
  }
  if (!Number.isNaN(rotationDueAt) && rotationDueAt <= now) {
    badges.push({ label: 'Rotation due', tone: 'warning' })
  }
  return badges
}

export default function AccessPage({ projects, activeProject, canManageServiceAccounts }: AccessPageProps) {
  const api = useApi()
  const [accounts, setAccounts] = useState<DashboardServiceAccount[]>([])
  const [form, setForm] = useState<ServiceAccountForm>(() => buildPresetForm(presetByID('ci'), activeProject?.id))
  const [loading, setLoading] = useState(true)
  const [actionError, setActionError] = useState('')
  const [actionMessage, setActionMessage] = useState('')
  const [issuedKey, setIssuedKey] = useState<IssuedKeyState | null>(null)
  const [copyMessage, setCopyMessage] = useState('')

  const activePreset = useMemo(() => presetByID(form.purpose), [form.purpose])

  const loadAccounts = useCallback(async () => {
    setLoading(true)
    try {
      const response = await api.listAuthServiceAccounts()
      setAccounts(response.service_accounts || [])
      setActionError('')
    } catch (error: unknown) {
      setActionError(getErrorMessage(error, 'Unable to load service accounts'))
      setAccounts([])
    } finally {
      setLoading(false)
    }
  }, [api])

  useEffect(() => {
    loadAccounts()
  }, [loadAccounts])

  const applyPreset = (preset: PurposePreset) => {
    setForm(buildPresetForm(preset, activeProject?.id))
    setActionError('')
    setActionMessage('')
  }

  const createAccount = async () => {
    if (!canManageServiceAccounts) {
      setActionError('Current session cannot manage service-account API keys.')
      setActionMessage('')
      return
    }
    if (!form.name.trim()) {
      setActionError('Service account name is required.')
      setActionMessage('')
      return
    }
    if (form.globalRoles.length === 0) {
      setActionError('Choose at least one global role.')
      setActionMessage('')
      return
    }
    if (form.scopes.length === 0) {
      setActionError('Choose at least one token scope.')
      setActionMessage('')
      return
    }

    try {
      const response = await api.createAuthServiceAccount({
        name: form.name.trim(),
        description: form.description.trim() || undefined,
        global_roles: form.globalRoles,
        project_roles: toProjectRolesPayload(form.projectRoles),
        scopes: form.scopes,
        expires_at: parseOptionalDateTime(form.expiresAt),
        rotation_interval_days: parseRotationDays(form.rotationDays),
      })
      setIssuedKey({ key: response.api_key, accountName: response.service_account.name, mode: 'created' })
      setActionMessage(`Created ${response.service_account.name}. Copy the issued key before closing the dialog.`)
      setActionError('')
      await loadAccounts()
    } catch (error: unknown) {
      setActionError(getErrorMessage(error, 'Unable to create service account'))
      setActionMessage('')
    }
  }

  const rotateAccount = async (account: DashboardServiceAccount) => {
    if (!canManageServiceAccounts) return
    if (typeof window !== 'undefined' && !window.confirm(`Rotate API key for ${account.name}? Existing automation must be updated with the new key.`)) {
      return
    }
    try {
      const response = await api.rotateAuthServiceAccount(account.id)
      setIssuedKey({ key: response.api_key, accountName: response.service_account.name, mode: 'rotated' })
      setActionMessage(`Rotated API key for ${response.service_account.name}.`)
      setActionError('')
      await loadAccounts()
    } catch (error: unknown) {
      setActionError(getErrorMessage(error, 'Unable to rotate service account key'))
      setActionMessage('')
    }
  }

  const setAccountActive = async (account: DashboardServiceAccount, active: boolean) => {
    if (!canManageServiceAccounts) return
    const action = active ? 'enable' : 'disable'
    if (typeof window !== 'undefined' && !window.confirm(`Really ${action} ${account.name}?`)) {
      return
    }
    try {
      await api.updateAuthServiceAccount(account.id, { active })
      setActionMessage(`${active ? 'Enabled' : 'Disabled'} ${account.name}.`)
      setActionError('')
      await loadAccounts()
    } catch (error: unknown) {
      setActionError(getErrorMessage(error, `Unable to ${action} service account`))
      setActionMessage('')
    }
  }

  const deleteAccount = async (account: DashboardServiceAccount) => {
    if (!canManageServiceAccounts) return
    if (typeof window !== 'undefined' && !window.confirm(`Delete ${account.name}? This cannot be undone.`)) {
      return
    }
    try {
      await api.deleteAuthServiceAccount(account.id)
      setActionMessage(`Deleted ${account.name}.`)
      setActionError('')
      await loadAccounts()
    } catch (error: unknown) {
      setActionError(getErrorMessage(error, 'Unable to delete service account'))
      setActionMessage('')
    }
  }

  return (
    <div className="page access-page">
      <div className="page-header">
        <div>
          <h2>Access & API Keys</h2>
          <p className="page-subtitle">Generate scoped service-account keys for CI, MCP, imports, reporting, and controlled automation.</p>
        </div>
        <div className="access-header-actions">
          <span className={`access-permission-badge ${canManageServiceAccounts ? 'healthy' : 'danger'}`}>
            {canManageServiceAccounts ? 'Manage access granted' : 'Read-only session'}
          </span>
        </div>
      </div>

      <section className="access-summary-grid">
        <div>
          <span>Service Accounts</span>
          <strong>{accounts.length}</strong>
        </div>
        <div>
          <span>Active Keys</span>
          <strong>{accounts.filter(account => account.active).length}</strong>
        </div>
        <div>
          <span>Rotation Due</span>
          <strong>{accounts.filter(account => accountBadges(account).some(badge => badge.label === 'Rotation due')).length}</strong>
        </div>
        <div>
          <span>Never Used</span>
          <strong>{accounts.filter(account => !account.last_used_at).length}</strong>
        </div>
      </section>

      <section className="access-layout">
        <article className="access-panel">
          <div className="access-panel-header">
            <div>
              <h3>Generate API Key</h3>
              <p>Choose a purpose preset, tune roles and scopes, then store the one-time key in your secret manager.</p>
            </div>
          </div>

          <div className="access-preset-grid">
            {PURPOSE_PRESETS.map(preset => (
              <button
                key={preset.id}
                type="button"
                className={`access-preset-card ${form.purpose === preset.id ? 'active' : ''}`}
                onClick={() => applyPreset(preset)}
              >
                <strong>{preset.label}</strong>
                <span>{preset.description}</span>
              </button>
            ))}
          </div>

          <div className="access-form-grid">
            <label>
              <span>Service Account Name</span>
              <input value={form.name} onChange={event => setForm(prev => ({ ...prev, name: event.target.value }))} placeholder="Nightly CI" />
            </label>
            <label>
              <span>Description</span>
              <input value={form.description} onChange={event => setForm(prev => ({ ...prev, description: event.target.value }))} placeholder="Runs nightly smoke coverage" />
            </label>
            <label>
              <span>Expires At</span>
              <input type="datetime-local" value={form.expiresAt} onChange={event => setForm(prev => ({ ...prev, expiresAt: event.target.value }))} />
            </label>
            <label>
              <span>Rotation Interval</span>
              <input type="number" min={1} step={1} value={form.rotationDays} onChange={event => setForm(prev => ({ ...prev, rotationDays: event.target.value }))} />
            </label>
          </div>

          <div className="access-control-section">
            <div>
              <h4>Global Roles</h4>
              <p>{activePreset.label} starts with {formatRoleList(activePreset.globalRoles)}.</p>
            </div>
            <div className="access-chip-row">
              {GLOBAL_ROLE_OPTIONS.map(option => (
                <button
                  key={option.id}
                  type="button"
                  className={`access-chip ${form.globalRoles.includes(option.id) ? 'active' : ''}`}
                  onClick={() => setForm(prev => ({ ...prev, globalRoles: toggleValue(prev.globalRoles, option.id) }))}
                >
                  {option.label}
                </button>
              ))}
            </div>
          </div>

          <div className="access-control-section">
            <div>
              <h4>Token Scopes</h4>
              <p>Scopes are enforced in addition to RBAC roles.</p>
            </div>
            <div className="access-chip-row">
              {SCOPE_OPTIONS.map(option => (
                <button
                  key={option.id}
                  type="button"
                  className={`access-chip ${form.scopes.includes(option.id) ? 'active' : ''}`}
                  onClick={() => setForm(prev => ({ ...prev, scopes: toggleValue(prev.scopes, option.id) }))}
                >
                  {option.label}
                </button>
              ))}
            </div>
          </div>

          <div className="access-control-section">
            <div>
              <h4>Project Roles</h4>
              <p>Select project assignments instead of typing raw role mappings.</p>
            </div>
            <div className="access-project-role-list">
              {projects.length > 0 ? projects.map(project => {
                const selectedRole = form.projectRoles[project.id]
                return (
                  <div className="access-project-role-row" key={project.id}>
                    <label className="access-project-checkbox">
                      <input
                        type="checkbox"
                        checked={Boolean(selectedRole)}
                        onChange={event => setForm(prev => {
                          const next = { ...prev.projectRoles }
                          if (event.target.checked) {
                            next[project.id] = selectedRole || activePreset.projectRole || 'viewer'
                          } else {
                            delete next[project.id]
                          }
                          return { ...prev, projectRoles: next }
                        })}
                      />
                      <span>{project.name}</span>
                    </label>
                    <select
                      value={selectedRole || activePreset.projectRole || 'viewer'}
                      disabled={!selectedRole}
                      onChange={event => setForm(prev => ({ ...prev, projectRoles: { ...prev.projectRoles, [project.id]: event.target.value } }))}
                    >
                      {PROJECT_ROLE_OPTIONS.map(option => (
                        <option key={option.id} value={option.id}>{option.label}</option>
                      ))}
                    </select>
                  </div>
                )
              }) : (
                <p className="access-muted">Create or select a project to assign project-scoped roles.</p>
              )}
            </div>
          </div>

          {(actionError || actionMessage) && (
            <div className={`access-alert ${actionError ? 'error' : 'success'}`}>
              {actionError || actionMessage}
            </div>
          )}

          <div className="access-form-actions">
            <button className="btn btn-primary" type="button" onClick={createAccount} disabled={!canManageServiceAccounts || !form.name.trim()}>
              Create Service Account
            </button>
          </div>
        </article>

        <aside className="access-side-stack">
          <section className="access-panel">
            <div className="access-panel-header">
              <div>
                <h3>Runtime Secrets Guidance</h3>
                <p>APEX Load does not store plaintext runtime secrets in this dashboard phase.</p>
              </div>
            </div>
            <div className="access-guidance-list">
              <div>
                <strong>Store secrets externally</strong>
                <span>Use Vault, Kubernetes secrets, CI secret stores, or environment variables.</span>
              </div>
              <div>
                <strong>Reference at runtime</strong>
                <span>Use safe script references like <code>{'{{env.API_TOKEN}}'}</code> or <code>{'{{env.CLIENT_SECRET}}'}</code>.</span>
              </div>
              <div>
                <strong>Keep API keys scoped</strong>
                <span>Service-account keys are for APEX Load control-plane automation, not application-under-test credentials.</span>
              </div>
            </div>
          </section>

          <section className="access-panel">
            <div className="access-panel-header">
              <div>
                <h3>Existing Keys</h3>
                <p>{loading ? 'Loading service accounts...' : `${accounts.length} automation principal${accounts.length === 1 ? '' : 's'}`}</p>
              </div>
            </div>
            <div className="access-account-list">
              {accounts.length > 0 ? accounts.map(account => (
                <article className="access-account-card" key={account.id}>
                  <div className="access-account-header">
                    <div>
                      <h4>{account.name}</h4>
                      <p>{account.description || 'No description provided.'}</p>
                    </div>
                    <div className="access-badge-row">
                      {accountBadges(account).map(badge => (
                        <span className={`access-status-badge ${badge.tone}`} key={badge.label}>{badge.label}</span>
                      ))}
                    </div>
                  </div>
                  <dl className="access-account-meta">
                    <div><dt>Global roles</dt><dd>{formatRoleList(account.global_roles)}</dd></div>
                    <div><dt>Project roles</dt><dd>{formatProjectRoles(account.project_roles)}</dd></div>
                    <div><dt>Scopes</dt><dd>{(account.scopes || ['automation:*']).join(', ')}</dd></div>
                    <div><dt>Last used</dt><dd>{formatTimestamp(account.last_used_at)}</dd></div>
                    <div><dt>Expires</dt><dd>{account.expires_at ? formatTimestamp(account.expires_at) : 'No expiration'}</dd></div>
                    <div><dt>Rotation</dt><dd>{account.rotation_due_at ? `Due ${formatTimestamp(account.rotation_due_at)}` : `Every ${account.rotation_interval_days || 90} days`}</dd></div>
                  </dl>
                  <div className="access-account-actions">
                    <button className="btn btn-secondary btn-sm" type="button" onClick={() => rotateAccount(account)} disabled={!canManageServiceAccounts}>Rotate Key</button>
                    <button className="btn btn-secondary btn-sm" type="button" onClick={() => setAccountActive(account, !account.active)} disabled={!canManageServiceAccounts}>
                      {account.active ? 'Disable' : 'Enable'}
                    </button>
                    <button className="btn btn-ghost btn-sm access-danger-action" type="button" onClick={() => deleteAccount(account)} disabled={!canManageServiceAccounts}>Delete</button>
                  </div>
                </article>
              )) : (
                <div className="access-empty-state">
                  <strong>No service accounts yet</strong>
                  <span>Create a scoped key when CI, MCP, or reporting automation needs API access.</span>
                </div>
              )}
            </div>
          </section>
        </aside>
      </section>

      {issuedKey && (
        <IssuedKeyModal
          issuedKey={issuedKey}
          copyMessage={copyMessage}
          onCopy={async () => {
            try {
              if (!navigator.clipboard?.writeText) {
                throw new Error('clipboard unavailable')
              }
              await navigator.clipboard.writeText(issuedKey.key)
              setCopyMessage('Copied')
            } catch {
              setCopyMessage('Copy unavailable')
            }
          }}
          onClose={() => {
            setIssuedKey(null)
            setCopyMessage('')
          }}
        />
      )}
    </div>
  )
}

function IssuedKeyModal({
  issuedKey,
  copyMessage,
  onCopy,
  onClose,
}: {
  issuedKey: IssuedKeyState
  copyMessage: string
  onCopy: () => Promise<void>
  onClose: () => void
}) {
  const curlExample = `curl -H "Authorization: Bearer ${issuedKey.key}" http://localhost:8080/api/v1/projects`
  const mcpExample = `APEXLOAD_URL=http://localhost:8080/api/v1\nAPEXLOAD_API_KEY=${issuedKey.key}\npython3 mcp/server.py`

  return (
    <div className="access-modal-overlay" role="presentation">
      <div className="access-issued-modal" role="dialog" aria-modal="true" aria-label="Issued API key">
        <div className="access-modal-header">
          <div>
            <h3>{issuedKey.mode === 'created' ? 'API Key Created' : 'API Key Rotated'}</h3>
            <p>Copy this key now. APEX Load stores only a hash after this dialog closes.</p>
          </div>
          <button className="btn btn-ghost btn-sm" type="button" onClick={onClose}>Close</button>
        </div>
        <div className="access-issued-key-box">
          <span>{issuedKey.accountName}</span>
          <code>{issuedKey.key}</code>
          <button className="btn btn-secondary btn-sm" type="button" onClick={onCopy}>{copyMessage || 'Copy Key'}</button>
        </div>
        <div className="access-issued-examples">
          <div>
            <h4>cURL</h4>
            <pre><code>{curlExample}</code></pre>
          </div>
          <div>
            <h4>MCP Server</h4>
            <pre><code>{mcpExample}</code></pre>
          </div>
        </div>
      </div>
    </div>
  )
}
