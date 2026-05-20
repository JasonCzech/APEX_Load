import { useCallback, useEffect, useMemo, useState } from 'react'
import { useApi } from '../hooks/useApi'
import {
  DashboardAuditEventFilter,
  DashboardServiceAccount,
  DashboardUser,
  DashboardUserRoleBinding,
  DashboardUserRoleScopeType,
  Domain,
  Project,
  UsageAccountingBucket,
} from '../types'
import { downloadBlob } from '../utils/fileUtils'
import { getErrorMessage } from '../utils/errorUtils'

type AccessTab = 'users' | 'service_accounts' | 'activity'

type AccessPageProps = {
  projects: Project[]
  domains?: Domain[]
  activeDomain?: Domain | null
  activeProject?: Project | null
  canManageUsers: boolean
  canViewUserActivity: boolean
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

type UserForm = {
  subject: string
  email: string
  displayName: string
}

type UserBindingForm = {
  scopeType: DashboardUserRoleScopeType
  scopeID: string
  role: string
}

type ActivityFilterForm = {
  actorSubject: string
  domainID: string
  projectID: string
  action: string
  status: string
  createdAfter: string
  createdBefore: string
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

const SCOPED_ROLE_OPTIONS = [
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

const SCOPE_LABELS: Record<string, string> = {
  global: 'Global',
  domain: 'Domain',
  project: 'Project',
  application_repository: 'Repository',
}

function preferredTab(canManageUsers: boolean, canManageServiceAccounts: boolean, canViewUserActivity: boolean): AccessTab {
  if (canManageUsers) return 'users'
  if (canManageServiceAccounts) return 'service_accounts'
  if (canViewUserActivity) return 'activity'
  return 'users'
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

function buildActivityFilter(form: ActivityFilterForm): DashboardAuditEventFilter {
  return {
    actor_subject: form.actorSubject.trim() || undefined,
    domain_id: form.domainID || undefined,
    project_id: form.projectID || undefined,
    action: form.action.trim() || undefined,
    status: form.status || undefined,
    created_after: parseOptionalDateTime(form.createdAfter),
    created_before: parseOptionalDateTime(form.createdBefore),
    limit: 50,
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

function userStatusBadge(user: DashboardUser): { label: string; tone: string } {
  return user.active ? { label: 'Active', tone: 'healthy' } : { label: 'Disabled', tone: 'danger' }
}

function bindingLabel(binding: DashboardUserRoleBinding, projects: Project[], domains: Domain[]): string {
  if (binding.scope_type === 'global') return 'Global'
  if (binding.scope_type === 'project') {
    return projects.find(project => project.id === binding.scope_id)?.name || binding.scope_id || 'Project'
  }
  if (binding.scope_type === 'domain') {
    return domains.find(domain => domain.id === binding.scope_id)?.name || binding.scope_id || 'Domain'
  }
  return binding.scope_id || 'Repository'
}

function bucketLabel(bucket: UsageAccountingBucket): string {
  return bucket.key || 'unattributed'
}

function formatNumber(value?: number): string {
  return (value || 0).toLocaleString()
}

function roleOptionsForScope(scopeType: DashboardUserRoleScopeType) {
  return scopeType === 'global' ? GLOBAL_ROLE_OPTIONS : SCOPED_ROLE_OPTIONS
}

function defaultScopeID(scopeType: DashboardUserRoleScopeType, projects: Project[], domains: Domain[], activeProject?: Project | null, activeDomain?: Domain | null): string {
  if (scopeType === 'project') return activeProject?.id || projects[0]?.id || ''
  if (scopeType === 'domain') return activeDomain?.id || domains[0]?.id || activeProject?.domain_id || ''
  return ''
}

export default function AccessPage({
  projects,
  domains = [],
  activeDomain,
  activeProject,
  canManageUsers,
  canViewUserActivity,
  canManageServiceAccounts,
}: AccessPageProps) {
  const api = useApi()
  const [activeTab, setActiveTab] = useState<AccessTab>(() => preferredTab(canManageUsers, canManageServiceAccounts, canViewUserActivity))
  const [accounts, setAccounts] = useState<DashboardServiceAccount[]>([])
  const [users, setUsers] = useState<DashboardUser[]>([])
  const [selectedUserID, setSelectedUserID] = useState('')
  const [userSearch, setUserSearch] = useState('')
  const [userForm, setUserForm] = useState<UserForm>({ subject: '', email: '', displayName: '' })
  const [editUserForm, setEditUserForm] = useState<UserForm>({ subject: '', email: '', displayName: '' })
  const [bindingForm, setBindingForm] = useState<UserBindingForm>({
    scopeType: 'project',
    scopeID: defaultScopeID('project', projects, domains, activeProject, activeDomain),
    role: 'viewer',
  })
  const [activityFilter, setActivityFilter] = useState<ActivityFilterForm>({
    actorSubject: '',
    domainID: activeDomain?.id || '',
    projectID: activeProject?.id || '',
    action: '',
    status: '',
    createdAfter: '',
    createdBefore: '',
  })
  const [activityEvents, setActivityEvents] = useState<Awaited<ReturnType<typeof api.getAuthUserActivity>>['events']>([])
  const [activityUsage, setActivityUsage] = useState<Awaited<ReturnType<typeof api.getAuthUserActivity>>['usage'] | null>(null)
  const [form, setForm] = useState<ServiceAccountForm>(() => buildPresetForm(presetByID('ci'), activeProject?.id))
  const [loading, setLoading] = useState(true)
  const [usersLoading, setUsersLoading] = useState(true)
  const [activityLoading, setActivityLoading] = useState(true)
  const [actionError, setActionError] = useState('')
  const [actionMessage, setActionMessage] = useState('')
  const [userActionError, setUserActionError] = useState('')
  const [userActionMessage, setUserActionMessage] = useState('')
  const [activityError, setActivityError] = useState('')
  const [activityMessage, setActivityMessage] = useState('')
  const [issuedKey, setIssuedKey] = useState<IssuedKeyState | null>(null)
  const [copyMessage, setCopyMessage] = useState('')

  const activePreset = useMemo(() => presetByID(form.purpose), [form.purpose])
  const selectedUser = useMemo(() => users.find(user => user.id === selectedUserID) || users[0] || null, [selectedUserID, users])
  const activeUsers = users.filter(user => user.active).length
  const userBuckets = activityUsage?.by_user || []

  const loadAccounts = useCallback(async () => {
    if (!canManageServiceAccounts) {
      setAccounts([])
      setLoading(false)
      return
    }
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
  }, [api, canManageServiceAccounts])

  const loadUsers = useCallback(async () => {
    if (!canManageUsers) {
      setUsers([])
      setUsersLoading(false)
      return
    }
    setUsersLoading(true)
    try {
      const response = await api.listAuthUsers({ q: userSearch.trim() || undefined, limit: 100 })
      setUsers(response.users || [])
      setUserActionError('')
    } catch (error: unknown) {
      setUserActionError(getErrorMessage(error, 'Unable to load users'))
      setUsers([])
    } finally {
      setUsersLoading(false)
    }
  }, [api, canManageUsers, userSearch])

  const loadActivity = useCallback(async () => {
    if (!canViewUserActivity) {
      setActivityEvents([])
      setActivityUsage(null)
      setActivityLoading(false)
      return
    }
    setActivityLoading(true)
    try {
      const response = await api.getAuthUserActivity(buildActivityFilter(activityFilter))
      setActivityEvents(response.events || [])
      setActivityUsage(response.usage)
      setActivityError('')
    } catch (error: unknown) {
      setActivityError(getErrorMessage(error, 'Unable to load activity'))
      setActivityEvents([])
      setActivityUsage(null)
    } finally {
      setActivityLoading(false)
    }
  }, [activityFilter, api, canViewUserActivity])

  useEffect(() => {
    setActiveTab(current => {
      if (current === 'users' && canManageUsers) return current
      if (current === 'service_accounts' && canManageServiceAccounts) return current
      if (current === 'activity' && canViewUserActivity) return current
      return preferredTab(canManageUsers, canManageServiceAccounts, canViewUserActivity)
    })
  }, [canManageServiceAccounts, canManageUsers, canViewUserActivity])

  useEffect(() => {
    loadAccounts()
  }, [loadAccounts])

  useEffect(() => {
    loadUsers()
  }, [loadUsers])

  useEffect(() => {
    loadActivity()
  }, [loadActivity])

  useEffect(() => {
    if (!selectedUser) {
      setSelectedUserID('')
      return
    }
    setSelectedUserID(selectedUser.id)
    setEditUserForm({
      subject: selectedUser.subject || '',
      email: selectedUser.email || '',
      displayName: selectedUser.display_name || '',
    })
  }, [selectedUser])

  useEffect(() => {
    setBindingForm(prev => ({
      ...prev,
      scopeID: prev.scopeType === 'global' ? '' : prev.scopeID || defaultScopeID(prev.scopeType, projects, domains, activeProject, activeDomain),
    }))
  }, [activeDomain, activeProject, domains, projects])

  const applyPreset = (preset: PurposePreset) => {
    setForm(buildPresetForm(preset, activeProject?.id))
    setActionError('')
    setActionMessage('')
  }

  const createUser = async () => {
    if (!canManageUsers) {
      setUserActionError('Current session cannot manage users.')
      setUserActionMessage('')
      return
    }
    if (!userForm.subject.trim()) {
      setUserActionError('Subject is required.')
      setUserActionMessage('')
      return
    }
    try {
      const user = await api.createAuthUser({
        subject: userForm.subject.trim(),
        email: userForm.email.trim() || undefined,
        display_name: userForm.displayName.trim() || undefined,
      })
      setSelectedUserID(user.id)
      setUserForm({ subject: '', email: '', displayName: '' })
      setUserActionMessage(`Onboarded ${user.display_name || user.email || user.subject}.`)
      setUserActionError('')
      await loadUsers()
    } catch (error: unknown) {
      setUserActionError(getErrorMessage(error, 'Unable to create user'))
      setUserActionMessage('')
    }
  }

  const updateSelectedUser = async () => {
    if (!selectedUser || !canManageUsers) return
    if (!editUserForm.subject.trim()) {
      setUserActionError('Subject is required.')
      setUserActionMessage('')
      return
    }
    try {
      const user = await api.updateAuthUser(selectedUser.id, {
        subject: editUserForm.subject.trim(),
        email: editUserForm.email.trim() || undefined,
        display_name: editUserForm.displayName.trim() || undefined,
      })
      setSelectedUserID(user.id)
      setUserActionMessage(`Updated ${user.display_name || user.email || user.subject}.`)
      setUserActionError('')
      await loadUsers()
    } catch (error: unknown) {
      setUserActionError(getErrorMessage(error, 'Unable to update user'))
      setUserActionMessage('')
    }
  }

  const setUserActive = async (user: DashboardUser, active: boolean) => {
    if (!canManageUsers) return
    const action = active ? 'enable' : 'disable'
    if (typeof window !== 'undefined' && !window.confirm(`Really ${action} ${user.display_name || user.subject}?`)) {
      return
    }
    try {
      await (active ? api.enableAuthUser(user.id) : api.disableAuthUser(user.id))
      setUserActionMessage(`${active ? 'Enabled' : 'Disabled'} ${user.display_name || user.subject}.`)
      setUserActionError('')
      await loadUsers()
    } catch (error: unknown) {
      setUserActionError(getErrorMessage(error, `Unable to ${action} user`))
      setUserActionMessage('')
    }
  }

  const createBinding = async () => {
    if (!selectedUser || !canManageUsers) return
    if (bindingForm.scopeType !== 'global' && !bindingForm.scopeID) {
      setUserActionError('Choose a scope.')
      setUserActionMessage('')
      return
    }
    try {
      await api.createAuthUserBinding(selectedUser.id, {
        scope_type: bindingForm.scopeType,
        scope_id: bindingForm.scopeType === 'global' ? undefined : bindingForm.scopeID,
        role: bindingForm.role,
      })
      setUserActionMessage(`Added ${ROLE_LABELS[bindingForm.role] || bindingForm.role} binding.`)
      setUserActionError('')
      await loadUsers()
    } catch (error: unknown) {
      setUserActionError(getErrorMessage(error, 'Unable to add role binding'))
      setUserActionMessage('')
    }
  }

  const deleteBinding = async (binding: DashboardUserRoleBinding) => {
    if (!selectedUser || !canManageUsers) return
    if (typeof window !== 'undefined' && !window.confirm(`Remove ${ROLE_LABELS[binding.role] || binding.role} from ${bindingLabel(binding, projects, domains)}?`)) {
      return
    }
    try {
      await api.deleteAuthUserBinding(selectedUser.id, binding.id)
      setUserActionMessage('Removed role binding.')
      setUserActionError('')
      await loadUsers()
    } catch (error: unknown) {
      setUserActionError(getErrorMessage(error, 'Unable to remove role binding'))
      setUserActionMessage('')
    }
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

  const exportActivity = async () => {
    if (!canViewUserActivity) return
    try {
      const blob = await api.downloadAuthUserActivity(buildActivityFilter(activityFilter))
      const date = new Date().toISOString().slice(0, 10)
      downloadBlob(blob, `apexload-user-activity-${date}.csv`)
      setActivityMessage('Activity export downloaded.')
      setActivityError('')
    } catch (error: unknown) {
      setActivityError(getErrorMessage(error, 'Unable to export activity'))
      setActivityMessage('')
    }
  }

  const setBindingScopeType = (scopeType: DashboardUserRoleScopeType) => {
    setBindingForm({
      scopeType,
      scopeID: defaultScopeID(scopeType, projects, domains, activeProject, activeDomain),
      role: roleOptionsForScope(scopeType)[0]?.id || 'viewer',
    })
  }

  return (
    <div className="page access-page">
      <div className="page-header">
        <div>
          <h2>Access</h2>
          <p className="page-subtitle">Users, automation keys, and activity across domains and projects.</p>
        </div>
        <div className="access-header-actions">
          <span className={`access-permission-badge ${(canManageUsers || canManageServiceAccounts || canViewUserActivity) ? 'healthy' : 'danger'}`}>
            {(canManageUsers || canManageServiceAccounts || canViewUserActivity) ? 'Access workspace available' : 'Read-only session'}
          </span>
        </div>
      </div>

      <div className="access-tab-row" role="tablist" aria-label="Access workspace sections">
        {canManageUsers && <button type="button" className={`access-tab ${activeTab === 'users' ? 'active' : ''}`} onClick={() => setActiveTab('users')}>Users</button>}
        {canManageServiceAccounts && <button type="button" className={`access-tab ${activeTab === 'service_accounts' ? 'active' : ''}`} onClick={() => setActiveTab('service_accounts')}>Service Accounts</button>}
        {canViewUserActivity && <button type="button" className={`access-tab ${activeTab === 'activity' ? 'active' : ''}`} onClick={() => setActiveTab('activity')}>Activity</button>}
      </div>

      <section className="access-summary-grid">
        <div>
          <span>Users</span>
          <strong>{users.length}</strong>
        </div>
        <div>
          <span>Active Users</span>
          <strong>{activeUsers}</strong>
        </div>
        <div>
          <span>Service Accounts</span>
          <strong>{accounts.length}</strong>
        </div>
        <div>
          <span>Activity Events</span>
          <strong>{activityEvents.length}</strong>
        </div>
      </section>

      {activeTab === 'users' && (
        <section className="access-layout">
          <article className="access-panel">
            <div className="access-panel-header">
              <div>
                <h3>Onboard User</h3>
                <p>Create a local APEX Load user record for an upstream OIDC or trusted-header subject.</p>
              </div>
            </div>
            <div className="access-form-grid">
              <label>
                <span>Subject</span>
                <input value={userForm.subject} onChange={event => setUserForm(prev => ({ ...prev, subject: event.target.value }))} placeholder="oidc-subject-or-upstream-user" />
              </label>
              <label>
                <span>Email</span>
                <input value={userForm.email} onChange={event => setUserForm(prev => ({ ...prev, email: event.target.value }))} placeholder="person@example.com" />
              </label>
              <label>
                <span>Display Name</span>
                <input value={userForm.displayName} onChange={event => setUserForm(prev => ({ ...prev, displayName: event.target.value }))} placeholder="Jordan Lee" />
              </label>
            </div>
            <div className="access-form-actions">
              <button className="btn btn-primary" type="button" onClick={createUser} disabled={!canManageUsers || !userForm.subject.trim()}>
                Create User
              </button>
            </div>

            {selectedUser && (
              <>
                <div className="access-divider" />
                <div className="access-panel-header">
                  <div>
                    <h3>Selected User</h3>
                    <p>{selectedUser.display_name || selectedUser.email || selectedUser.subject}</p>
                  </div>
                  <span className={`access-status-badge ${userStatusBadge(selectedUser).tone}`}>{userStatusBadge(selectedUser).label}</span>
                </div>
                <div className="access-form-grid">
                  <label>
                    <span>Subject</span>
                    <input value={editUserForm.subject} onChange={event => setEditUserForm(prev => ({ ...prev, subject: event.target.value }))} />
                  </label>
                  <label>
                    <span>Email</span>
                    <input value={editUserForm.email} onChange={event => setEditUserForm(prev => ({ ...prev, email: event.target.value }))} />
                  </label>
                  <label>
                    <span>Display Name</span>
                    <input value={editUserForm.displayName} onChange={event => setEditUserForm(prev => ({ ...prev, displayName: event.target.value }))} />
                  </label>
                </div>
                <div className="access-account-actions">
                  <button className="btn btn-secondary btn-sm" type="button" onClick={updateSelectedUser} disabled={!canManageUsers}>Update User</button>
                  <button className="btn btn-secondary btn-sm" type="button" onClick={() => setUserActive(selectedUser, !selectedUser.active)} disabled={!canManageUsers}>
                    {selectedUser.active ? 'Disable User' : 'Enable User'}
                  </button>
                </div>

                <div className="access-control-section">
                  <div>
                    <h4>Role Bindings</h4>
                    <p>Assign roles at global, domain, or project scope.</p>
                  </div>
                  <div className="access-form-grid">
                    <label>
                      <span>Scope</span>
                      <select value={bindingForm.scopeType} onChange={event => setBindingScopeType(event.target.value as DashboardUserRoleScopeType)}>
                        <option value="global">Global</option>
                        <option value="domain">Domain</option>
                        <option value="project">Project</option>
                      </select>
                    </label>
                    {bindingForm.scopeType === 'domain' && (
                      <label>
                        <span>Domain</span>
                        <select value={bindingForm.scopeID} onChange={event => setBindingForm(prev => ({ ...prev, scopeID: event.target.value }))}>
                          {domains.map(domain => <option key={domain.id} value={domain.id}>{domain.name}</option>)}
                          {domains.length === 0 && activeDomain && <option value={activeDomain.id}>{activeDomain.name}</option>}
                        </select>
                      </label>
                    )}
                    {bindingForm.scopeType === 'project' && (
                      <label>
                        <span>Project</span>
                        <select value={bindingForm.scopeID} onChange={event => setBindingForm(prev => ({ ...prev, scopeID: event.target.value }))}>
                          {projects.map(project => <option key={project.id} value={project.id}>{project.name}</option>)}
                        </select>
                      </label>
                    )}
                    <label>
                      <span>Role</span>
                      <select value={bindingForm.role} onChange={event => setBindingForm(prev => ({ ...prev, role: event.target.value }))}>
                        {roleOptionsForScope(bindingForm.scopeType).map(option => <option key={option.id} value={option.id}>{option.label}</option>)}
                      </select>
                    </label>
                  </div>
                  <div className="access-form-actions">
                    <button className="btn btn-primary" type="button" onClick={createBinding} disabled={!canManageUsers || !selectedUser}>
                      Add Role
                    </button>
                  </div>
                  <div className="access-binding-list">
                    {(selectedUser.bindings || []).length > 0 ? selectedUser.bindings?.map(binding => (
                      <div className="access-binding-row" key={binding.id}>
                        <div>
                          <strong>{ROLE_LABELS[binding.role] || binding.role}</strong>
                          <span>{SCOPE_LABELS[binding.scope_type] || binding.scope_type} · {bindingLabel(binding, projects, domains)}</span>
                        </div>
                        <button className="btn btn-ghost btn-sm access-danger-action" type="button" onClick={() => deleteBinding(binding)} disabled={!canManageUsers}>Remove</button>
                      </div>
                    )) : (
                      <p className="access-muted">No role bindings assigned.</p>
                    )}
                  </div>
                </div>
              </>
            )}

            {(userActionError || userActionMessage) && (
              <div className={`access-alert ${userActionError ? 'error' : 'success'}`}>
                {userActionError || userActionMessage}
              </div>
            )}
          </article>

          <aside className="access-side-stack">
            <section className="access-panel">
              <div className="access-panel-header">
                <div>
                  <h3>Users</h3>
                  <p>{usersLoading ? 'Loading users...' : `${users.length} local user${users.length === 1 ? '' : 's'}`}</p>
                </div>
              </div>
              <div className="access-form-grid access-search-grid">
                <label>
                  <span>Search</span>
                  <input value={userSearch} onChange={event => setUserSearch(event.target.value)} placeholder="subject, email, or name" />
                </label>
              </div>
              <div className="access-account-list">
                {users.length > 0 ? users.map(user => (
                  <button
                    type="button"
                    className={`access-user-card ${selectedUser?.id === user.id ? 'active' : ''}`}
                    key={user.id}
                    onClick={() => setSelectedUserID(user.id)}
                  >
                    <span>
                      <strong>{user.display_name || user.email || user.subject}</strong>
                      <small>{user.subject}</small>
                    </span>
                    <span className={`access-status-badge ${userStatusBadge(user).tone}`}>{userStatusBadge(user).label}</span>
                  </button>
                )) : (
                  <div className="access-empty-state">
                    <strong>No users found</strong>
                    <span>Adjust search or create a user.</span>
                  </div>
                )}
              </div>
            </section>
          </aside>
        </section>
      )}

      {activeTab === 'service_accounts' && (
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
                        {SCOPED_ROLE_OPTIONS.map(option => (
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
      )}

      {activeTab === 'activity' && (
        <section className="access-layout">
          <article className="access-panel">
            <div className="access-panel-header">
              <div>
                <h3>Activity</h3>
                <p>{activityLoading ? 'Loading activity...' : `${activityEvents.length} audit event${activityEvents.length === 1 ? '' : 's'}`}</p>
              </div>
              <button className="btn btn-secondary btn-sm" type="button" onClick={exportActivity} disabled={!canViewUserActivity}>Export CSV</button>
            </div>
            <div className="access-form-grid">
              <label>
                <span>User</span>
                <input value={activityFilter.actorSubject} onChange={event => setActivityFilter(prev => ({ ...prev, actorSubject: event.target.value }))} placeholder="actor subject" />
              </label>
              <label>
                <span>Domain</span>
                <select value={activityFilter.domainID} onChange={event => setActivityFilter(prev => ({ ...prev, domainID: event.target.value }))}>
                  <option value="">All domains</option>
                  {domains.map(domain => <option key={domain.id} value={domain.id}>{domain.name}</option>)}
                </select>
              </label>
              <label>
                <span>Project</span>
                <select value={activityFilter.projectID} onChange={event => setActivityFilter(prev => ({ ...prev, projectID: event.target.value }))}>
                  <option value="">All projects</option>
                  {projects.map(project => <option key={project.id} value={project.id}>{project.name}</option>)}
                </select>
              </label>
              <label>
                <span>Action</span>
                <input value={activityFilter.action} onChange={event => setActivityFilter(prev => ({ ...prev, action: event.target.value }))} placeholder="auth.user.create" />
              </label>
              <label>
                <span>Status</span>
                <select value={activityFilter.status} onChange={event => setActivityFilter(prev => ({ ...prev, status: event.target.value }))}>
                  <option value="">Any</option>
                  <option value="success">Success</option>
                  <option value="failure">Failure</option>
                </select>
              </label>
              <label>
                <span>Created After</span>
                <input type="datetime-local" value={activityFilter.createdAfter} onChange={event => setActivityFilter(prev => ({ ...prev, createdAfter: event.target.value }))} />
              </label>
              <label>
                <span>Created Before</span>
                <input type="datetime-local" value={activityFilter.createdBefore} onChange={event => setActivityFilter(prev => ({ ...prev, createdBefore: event.target.value }))} />
              </label>
            </div>
            <div className="access-form-actions">
              <button className="btn btn-primary" type="button" onClick={loadActivity} disabled={!canViewUserActivity}>Apply Filters</button>
            </div>

            {(activityError || activityMessage) && (
              <div className={`access-alert ${activityError ? 'error' : 'success'}`}>
                {activityError || activityMessage}
              </div>
            )}

            <div className="access-activity-list">
              {activityEvents.length > 0 ? activityEvents.map(event => (
                <article className="access-activity-row" key={event.id}>
                  <div>
                    <strong>{event.action}</strong>
                    <span>{event.actor_subject || event.actor_type} · {event.resource_type}{event.resource_id ? `/${event.resource_id}` : ''}</span>
                  </div>
                  <div>
                    <span className={`access-status-badge ${event.status === 'success' ? 'healthy' : 'warning'}`}>{event.status}</span>
                    <small>{formatTimestamp(event.created_at)}</small>
                  </div>
                </article>
              )) : (
                <div className="access-empty-state">
                  <strong>No activity found</strong>
                  <span>Adjust filters or select a broader timeframe.</span>
                </div>
              )}
            </div>
          </article>

          <aside className="access-side-stack">
            <section className="access-panel">
              <div className="access-panel-header">
                <div>
                  <h3>User Usage</h3>
                  <p>{userBuckets.length} attributed bucket{userBuckets.length === 1 ? '' : 's'}</p>
                </div>
              </div>
              <div className="access-usage-list">
                {userBuckets.length > 0 ? userBuckets.map(bucket => (
                  <div className="access-usage-row" key={bucketLabel(bucket)}>
                    <div>
                      <strong>{bucketLabel(bucket)}</strong>
                      <span>{formatNumber(bucket.run_count)} runs · {formatNumber(bucket.vuser_seconds)} VUser seconds</span>
                    </div>
                    <small>{formatNumber(bucket.total_vusers)} VUsers</small>
                  </div>
                )) : (
                  <div className="access-empty-state">
                    <strong>No attributed usage</strong>
                    <span>New runs will appear after user attribution is recorded.</span>
                  </div>
                )}
              </div>
            </section>
          </aside>
        </section>
      )}

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
