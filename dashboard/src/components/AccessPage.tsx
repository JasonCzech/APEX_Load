import { useCallback, useEffect, useMemo, useState } from 'react'
import type { Dispatch, SetStateAction } from 'react'
import { useApi } from '../hooks/useApi'
import {
  DashboardAuditEvent,
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
import { useHasTopbarContributionProvider, useTopbarContribution } from '../hooks/TopbarContributionProvider'

type AccessTab = 'users' | 'service_accounts' | 'activity'
type UserSubTab = 'existing' | 'onboard'
type ServiceAccountSubTab = 'existing' | 'generate'
type ActivitySubTab = 'events' | 'usage'
type SortDirection = 'asc' | 'desc'
type UserSortField = 'user' | 'status' | 'email' | 'roles' | 'access' | 'last_seen' | 'created' | 'updated' | 'usage'
type AccountSortField = 'account' | 'status' | 'roles' | 'access' | 'scopes' | 'last_used' | 'rotation_due' | 'expires' | 'updated'
type ActivitySortField = 'time' | 'actor' | 'action' | 'resource' | 'scope' | 'status' | 'details'
type UsageSortField = 'user' | 'runs' | 'total_vusers' | 'duration' | 'vuser_seconds'

type SortState<T extends string> = {
  field: T
  direction: SortDirection
}

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
  id: string
  bindingID?: string
  scopeType: DashboardUserRoleScopeType
  scopeID: string
  role: string
}

type UserFilterState = {
  query: string
  status: string
  role: string
  domainID: string
  projectID: string
}

type AccountFilterState = {
  query: string
  status: string
  role: string
  scope: string
  domainID: string
  projectID: string
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

const userSortDefaults: Record<UserSortField, SortDirection> = {
  user: 'asc',
  status: 'asc',
  email: 'asc',
  roles: 'asc',
  access: 'asc',
  last_seen: 'desc',
  created: 'desc',
  updated: 'desc',
  usage: 'desc',
}

const accountSortDefaults: Record<AccountSortField, SortDirection> = {
  account: 'asc',
  status: 'asc',
  roles: 'asc',
  access: 'asc',
  scopes: 'asc',
  last_used: 'desc',
  rotation_due: 'asc',
  expires: 'asc',
  updated: 'desc',
}

const activitySortDefaults: Record<ActivitySortField, SortDirection> = {
  time: 'desc',
  actor: 'asc',
  action: 'asc',
  resource: 'asc',
  scope: 'asc',
  status: 'asc',
  details: 'asc',
}

const usageSortDefaults: Record<UsageSortField, SortDirection> = {
  user: 'asc',
  runs: 'desc',
  total_vusers: 'desc',
  duration: 'desc',
  vuser_seconds: 'desc',
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

function timestampValue(value?: string): number {
  if (!value) return 0
  const parsed = Date.parse(value)
  return Number.isNaN(parsed) ? 0 : parsed
}

function formatTimestamp(value?: string): string {
  if (!value) return 'Never'
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString()
}

function formatDurationSeconds(value?: number): string {
  const total = Math.max(0, Math.round(value || 0))
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const seconds = total % 60
  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return `${minutes}m ${seconds}s`
  return `${seconds}s`
}

function formatRoleList(roles?: string[]): string {
  if (!roles || roles.length === 0) return 'None'
  return roles.map(role => ROLE_LABELS[role] || role).join(', ')
}

function roleOptionsForScope(scopeType: DashboardUserRoleScopeType) {
  return scopeType === 'global' ? GLOBAL_ROLE_OPTIONS : SCOPED_ROLE_OPTIONS
}

function uniqueValues(values: string[]): string[] {
  return Array.from(new Set(values.filter(Boolean))).sort((a, b) => a.localeCompare(b))
}

function normalize(value: unknown): string {
  return String(value ?? '').toLowerCase()
}

function includesQuery(values: unknown[], query: string): boolean {
  const normalizedQuery = query.trim().toLowerCase()
  if (!normalizedQuery) return true
  return values.some(value => normalize(value).includes(normalizedQuery))
}

function comparePrimitive(a: string | number, b: string | number, direction: SortDirection): number {
  const order = direction === 'asc' ? 1 : -1
  if (typeof a === 'number' && typeof b === 'number') {
    return (a - b) * order
  }
  return String(a).localeCompare(String(b), undefined, { numeric: true, sensitivity: 'base' }) * order
}

function stableSort<T>(items: T[], compare: (a: T, b: T) => number): T[] {
  return items
    .map((item, index) => ({ item, index }))
    .sort((a, b) => compare(a.item, b.item) || a.index - b.index)
    .map(entry => entry.item)
}

function nextSort<T extends string>(current: SortState<T>, field: T, defaults: Record<T, SortDirection>): SortState<T> {
  if (current.field === field) {
    return { field, direction: current.direction === 'asc' ? 'desc' : 'asc' }
  }
  return { field, direction: defaults[field] }
}

function defaultScopeID(scopeType: DashboardUserRoleScopeType, projects: Project[], domains: Domain[], activeProject?: Project | null, activeDomain?: Domain | null): string {
  if (scopeType === 'project') return activeProject?.id || projects[0]?.id || ''
  if (scopeType === 'domain') return activeDomain?.id || domains[0]?.id || activeProject?.domain_id || ''
  return ''
}

function createBindingDraftID(): string {
  return `binding-${Date.now()}-${Math.floor(Math.random() * 100000)}`
}

function createUserBindingDraft(scopeType: DashboardUserRoleScopeType, projects: Project[], domains: Domain[], activeProject?: Project | null, activeDomain?: Domain | null): UserBindingForm {
  return {
    id: createBindingDraftID(),
    scopeType,
    scopeID: defaultScopeID(scopeType, projects, domains, activeProject, activeDomain),
    role: roleOptionsForScope(scopeType)[0]?.id || 'viewer',
  }
}

function initialUserBindingDrafts(projects: Project[], domains: Domain[], activeProject?: Project | null, activeDomain?: Domain | null): UserBindingForm[] {
  if (activeProject || projects.length > 0) {
    return [createUserBindingDraft('project', projects, domains, activeProject, activeDomain)]
  }
  if (activeDomain || domains.length > 0) {
    return [createUserBindingDraft('domain', projects, domains, activeProject, activeDomain)]
  }
  return [createUserBindingDraft('global', projects, domains, activeProject, activeDomain)]
}

function userBindingDraftFromBinding(binding: DashboardUserRoleBinding): UserBindingForm {
  return {
    id: binding.id,
    bindingID: binding.id,
    scopeType: binding.scope_type,
    scopeID: binding.scope_id || '',
    role: binding.role,
  }
}

function userBindingSignature(scopeType: DashboardUserRoleScopeType, scopeID: string | undefined, role: string): string {
  return `${scopeType}:${scopeType === 'global' ? '' : scopeID || ''}:${role}`
}

function bindingSignature(binding: DashboardUserRoleBinding): string {
  return userBindingSignature(binding.scope_type, binding.scope_id, binding.role)
}

function draftSignature(draft: UserBindingForm): string {
  return userBindingSignature(draft.scopeType, draft.scopeID, draft.role)
}

function isValidUserBindingDraft(draft: UserBindingForm): boolean {
  if (!draft.role) return false
  if (draft.scopeType === 'global') return true
  return Boolean(draft.scopeID)
}

function normalizedUserBindingDrafts(drafts: UserBindingForm[]): UserBindingForm[] {
  const seen = new Set<string>()
  return drafts.filter(draft => {
    if (!isValidUserBindingDraft(draft)) return false
    const signature = draftSignature(draft)
    if (seen.has(signature)) return false
    seen.add(signature)
    return true
  })
}

function userStatusBadge(user: DashboardUser): { label: string; tone: string } {
  return user.active ? { label: 'Active', tone: 'healthy' } : { label: 'Disabled', tone: 'danger' }
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

function accountStatusKeys(account: DashboardServiceAccount): string[] {
  const now = Date.now()
  const expiresAt = account.expires_at ? Date.parse(account.expires_at) : Number.NaN
  const rotationDueAt = account.rotation_due_at ? Date.parse(account.rotation_due_at) : Number.NaN
  const keys = [account.active ? 'active' : 'disabled']
  if (!Number.isNaN(expiresAt) && expiresAt <= now) keys.push('expired')
  if (!Number.isNaN(rotationDueAt) && rotationDueAt <= now) keys.push('rotation_due')
  if (!account.last_used_at) keys.push('never_used')
  return keys
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

function userRoleList(user: DashboardUser): string[] {
  return uniqueValues((user.bindings || []).map(binding => binding.role))
}

function userAccessLabels(user: DashboardUser, projects: Project[], domains: Domain[]): string[] {
  return (user.bindings || []).map(binding => {
    const scope = SCOPE_LABELS[binding.scope_type] || binding.scope_type
    const label = bindingLabel(binding, projects, domains)
    return `${scope}: ${label} (${ROLE_LABELS[binding.role] || binding.role})`
  })
}

function projectDomainID(projects: Project[], projectID?: string): string {
  return projects.find(project => project.id === projectID)?.domain_id || ''
}

function userMatchesDomain(user: DashboardUser, domainID: string, projects: Project[]): boolean {
  if (!domainID) return true
  return (user.bindings || []).some(binding => {
    if (binding.scope_type === 'domain') return binding.scope_id === domainID
    if (binding.scope_type === 'project') return projectDomainID(projects, binding.scope_id) === domainID
    return false
  })
}

function userMatchesProject(user: DashboardUser, projectID: string): boolean {
  if (!projectID) return true
  return (user.bindings || []).some(binding => binding.scope_type === 'project' && binding.scope_id === projectID)
}

function accountRoleList(account: DashboardServiceAccount): string[] {
  const scopedRoles = [
    ...Object.values(account.project_roles || {}).flat(),
    ...Object.values(account.domain_roles || {}).flat(),
    ...Object.values(account.application_repository_roles || {}).flat(),
  ]
  return uniqueValues([...(account.global_roles || []), ...scopedRoles])
}

function formatScopeRoleEntries(entries: Record<string, string[]> | undefined, labelForID: (id: string) => string, scopeLabel: string): string[] {
  return Object.entries(entries || {}).map(([id, roles]) => `${scopeLabel}: ${labelForID(id)} (${formatRoleList(roles)})`)
}

function accountAccessLabels(account: DashboardServiceAccount, projects: Project[], domains: Domain[]): string[] {
  return [
    ...formatScopeRoleEntries(account.domain_roles, id => domains.find(domain => domain.id === id)?.name || id, 'Domain'),
    ...formatScopeRoleEntries(account.project_roles, id => projects.find(project => project.id === id)?.name || id, 'Project'),
    ...formatScopeRoleEntries(account.application_repository_roles, id => id, 'Repository'),
  ]
}

function accountMatchesDomain(account: DashboardServiceAccount, domainID: string, projects: Project[]): boolean {
  if (!domainID) return true
  if (Object.keys(account.domain_roles || {}).includes(domainID)) return true
  return Object.keys(account.project_roles || {}).some(projectID => projectDomainID(projects, projectID) === domainID)
}

function accountMatchesProject(account: DashboardServiceAccount, projectID: string): boolean {
  if (!projectID) return true
  return Object.keys(account.project_roles || {}).includes(projectID)
}

function bucketLabel(bucket: UsageAccountingBucket): string {
  return bucket.key || 'unattributed'
}

function formatNumber(value?: number): string {
  return (value || 0).toLocaleString()
}

function usageSummary(bucket?: UsageAccountingBucket): string {
  if (!bucket) return 'Unavailable'
  return `${formatNumber(bucket.run_count)} runs · ${formatNumber(bucket.vuser_seconds)} VUser seconds`
}

function usageForUser(user: DashboardUser, buckets: UsageAccountingBucket[]): UsageAccountingBucket | undefined {
  const keys = [user.subject, user.email, user.display_name, user.id].map(normalize).filter(Boolean)
  return buckets.find(bucket => keys.includes(normalize(bucket.key)))
}

function eventScopeLabel(event: DashboardAuditEvent, projects: Project[], domains: Domain[]): string {
  const parts: string[] = []
  if (event.domain_id) {
    parts.push(domains.find(domain => domain.id === event.domain_id)?.name || event.domain_id)
  }
  if (event.project_id) {
    parts.push(projects.find(project => project.id === event.project_id)?.name || event.project_id)
  }
  return parts.join(' / ') || 'Global'
}

function eventResourceLabel(event: DashboardAuditEvent): string {
  return `${event.resource_type}${event.resource_id ? `/${event.resource_id}` : ''}`
}

function detailsSummary(details?: Record<string, unknown>): string {
  const entries = Object.entries(details || {})
  if (entries.length === 0) return 'None'
  return entries.slice(0, 3).map(([key, value]) => `${key}: ${String(value)}`).join(' · ')
}

function SortIcon({ active, direction }: { active: boolean; direction: SortDirection }) {
  if (!active) return <span className="sort-icon">↕</span>
  return <span className="sort-icon active">{direction === 'asc' ? '↑' : '↓'}</span>
}

function SortHeader<T extends string>({
  label,
  field,
  sort,
  onSort,
  numeric = false,
}: {
  label: string
  field: T
  sort: SortState<T>
  onSort: (field: T) => void
  numeric?: boolean
}) {
  return (
    <th className={`sortable ${numeric ? 'num' : ''}`.trim()}>
      <button type="button" onClick={() => onSort(field)}>
        {label}
        <SortIcon active={sort.field === field} direction={sort.direction} />
      </button>
    </th>
  )
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
  const [userSubTab, setUserSubTab] = useState<UserSubTab>('existing')
  const [serviceAccountSubTab, setServiceAccountSubTab] = useState<ServiceAccountSubTab>('existing')
  const [activitySubTab, setActivitySubTab] = useState<ActivitySubTab>('events')
  const [accounts, setAccounts] = useState<DashboardServiceAccount[]>([])
  const [users, setUsers] = useState<DashboardUser[]>([])
  const [selectedUserID, setSelectedUserID] = useState('')
  const [selectedAccountID, setSelectedAccountID] = useState('')
  const [selectedActivityID, setSelectedActivityID] = useState('')
  const [userInspectorCollapsed, setUserInspectorCollapsed] = useState(false)
  const [accountInspectorCollapsed, setAccountInspectorCollapsed] = useState(false)
  const [activityInspectorCollapsed, setActivityInspectorCollapsed] = useState(false)
  const [userFilters, setUserFilters] = useState<UserFilterState>({ query: '', status: '', role: '', domainID: '', projectID: '' })
  const [accountFilters, setAccountFilters] = useState<AccountFilterState>({ query: '', status: '', role: '', scope: '', domainID: '', projectID: '' })
  const [activitySearch, setActivitySearch] = useState('')
  const [usageSearch, setUsageSearch] = useState('')
  const [userSort, setUserSort] = useState<SortState<UserSortField>>({ field: 'user', direction: 'asc' })
  const [accountSort, setAccountSort] = useState<SortState<AccountSortField>>({ field: 'updated', direction: 'desc' })
  const [activitySort, setActivitySort] = useState<SortState<ActivitySortField>>({ field: 'time', direction: 'desc' })
  const [usageSort, setUsageSort] = useState<SortState<UsageSortField>>({ field: 'vuser_seconds', direction: 'desc' })
  const [userForm, setUserForm] = useState<UserForm>({ subject: '', email: '', displayName: '' })
  const [editUserForm, setEditUserForm] = useState<UserForm>({ subject: '', email: '', displayName: '' })
  const [onboardBindingDrafts, setOnboardBindingDrafts] = useState<UserBindingForm[]>(() => initialUserBindingDrafts(projects, domains, activeProject, activeDomain))
  const [editBindingDrafts, setEditBindingDrafts] = useState<UserBindingForm[]>([])
  const [selectedUserEditMode, setSelectedUserEditMode] = useState(false)
  const [activityFilter, setActivityFilter] = useState<ActivityFilterForm>({
    actorSubject: '',
    domainID: activeDomain?.id || '',
    projectID: activeProject?.id || '',
    action: '',
    status: '',
    createdAfter: '',
    createdBefore: '',
  })
  const [activityEvents, setActivityEvents] = useState<DashboardAuditEvent[]>([])
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
  const hasTopbarContributionProvider = useHasTopbarContributionProvider()

  const activePreset = useMemo(() => presetByID(form.purpose), [form.purpose])
  const selectedUser = useMemo(() => users.find(user => user.id === selectedUserID) || null, [selectedUserID, users])
  const selectedAccount = useMemo(() => accounts.find(account => account.id === selectedAccountID) || null, [accounts, selectedAccountID])
  const selectedActivity = useMemo(() => activityEvents.find(event => event.id === selectedActivityID) || null, [activityEvents, selectedActivityID])
  const userBuckets = useMemo(() => activityUsage?.by_user || [], [activityUsage?.by_user])
  const accessWorkspaceAvailable = canManageUsers || canManageServiceAccounts || canViewUserActivity

  const accessTabControls = useMemo(() => (
    accessWorkspaceAvailable ? (
      <div className="access-tab-row topbar-page-section-toggle" role="tablist" aria-label="Access workspace sections">
        {canManageUsers && (
          <button type="button" role="tab" aria-selected={activeTab === 'users'} className={`access-tab ${activeTab === 'users' ? 'active' : ''}`} onClick={() => setActiveTab('users')}>
            Users <strong>{users.length}</strong>
          </button>
        )}
        {canManageServiceAccounts && (
          <button type="button" role="tab" aria-selected={activeTab === 'service_accounts'} className={`access-tab ${activeTab === 'service_accounts' ? 'active' : ''}`} onClick={() => setActiveTab('service_accounts')}>
            Service Accounts <strong>{accounts.length}</strong>
          </button>
        )}
        {canViewUserActivity && (
          <button type="button" role="tab" aria-selected={activeTab === 'activity'} className={`access-tab ${activeTab === 'activity' ? 'active' : ''}`} onClick={() => setActiveTab('activity')}>
            Activity <strong>{activityEvents.length}</strong>
          </button>
        )}
      </div>
    ) : null
  ), [accessWorkspaceAvailable, accounts.length, activeTab, activityEvents.length, canManageServiceAccounts, canManageUsers, canViewUserActivity, users.length])

  const accessTopbarContribution = useMemo(() => ({
    id: 'access-page',
    title: 'Access',
    controls: accessTabControls,
    priority: 10,
  }), [accessTabControls])

  useTopbarContribution(accessTopbarContribution)

  const roleFilterOptions = useMemo(() => uniqueValues([
    ...GLOBAL_ROLE_OPTIONS.map(option => option.id),
    ...users.flatMap(user => userRoleList(user)),
    ...accounts.flatMap(account => accountRoleList(account)),
  ]), [accounts, users])

  const scopeFilterOptions = useMemo(() => uniqueValues([
    ...SCOPE_OPTIONS.map(option => option.id),
    ...accounts.flatMap(account => account.scopes || []),
  ]), [accounts])

  const filteredUsers = useMemo(() => {
    const filtered = users.filter(user => {
      const roles = userRoleList(user)
      const accessLabels = userAccessLabels(user, projects, domains)
      const usage = usageForUser(user, userBuckets)
      if (userFilters.status === 'active' && !user.active) return false
      if (userFilters.status === 'disabled' && user.active) return false
      if (userFilters.role && !roles.includes(userFilters.role)) return false
      if (!userMatchesDomain(user, userFilters.domainID, projects)) return false
      if (!userMatchesProject(user, userFilters.projectID)) return false
      return includesQuery([
        user.subject,
        user.email,
        user.display_name,
        userStatusBadge(user).label,
        ...roles.map(role => ROLE_LABELS[role] || role),
        ...accessLabels,
        usageSummary(usage),
      ], userFilters.query)
    })

    return stableSort(filtered, (a, b) => {
      const usageA = usageForUser(a, userBuckets)
      const usageB = usageForUser(b, userBuckets)
      const valueFor = (user: DashboardUser, usage?: UsageAccountingBucket): string | number => {
        switch (userSort.field) {
          case 'status': return user.active ? 'Active' : 'Disabled'
          case 'email': return user.email || ''
          case 'roles': return formatRoleList(userRoleList(user))
          case 'access': return userAccessLabels(user, projects, domains).join(' ')
          case 'last_seen': return timestampValue(user.last_seen_at)
          case 'created': return timestampValue(user.created_at)
          case 'updated': return timestampValue(user.updated_at)
          case 'usage': return usage?.vuser_seconds || 0
          case 'user':
          default:
            return user.display_name || user.email || user.subject
        }
      }
      return comparePrimitive(valueFor(a, usageA), valueFor(b, usageB), userSort.direction)
    })
  }, [domains, projects, userBuckets, userFilters, userSort, users])

  const filteredAccounts = useMemo(() => {
    const filtered = accounts.filter(account => {
      const roles = accountRoleList(account)
      const accessLabels = accountAccessLabels(account, projects, domains)
      const statusKeys = accountStatusKeys(account)
      if (accountFilters.status && !statusKeys.includes(accountFilters.status)) return false
      if (accountFilters.role && !roles.includes(accountFilters.role)) return false
      if (accountFilters.scope && !(account.scopes || []).includes(accountFilters.scope)) return false
      if (!accountMatchesDomain(account, accountFilters.domainID, projects)) return false
      if (!accountMatchesProject(account, accountFilters.projectID)) return false
      return includesQuery([
        account.name,
        account.description,
        ...roles.map(role => ROLE_LABELS[role] || role),
        ...accessLabels,
        ...(account.scopes || []),
        ...accountBadges(account).map(badge => badge.label),
      ], accountFilters.query)
    })

    return stableSort(filtered, (a, b) => {
      const valueFor = (account: DashboardServiceAccount): string | number => {
        switch (accountSort.field) {
          case 'status': return accountBadges(account)[0]?.label || ''
          case 'roles': return formatRoleList(accountRoleList(account))
          case 'access': return accountAccessLabels(account, projects, domains).join(' ')
          case 'scopes': return (account.scopes || ['automation:*']).join(' ')
          case 'last_used': return timestampValue(account.last_used_at)
          case 'rotation_due': return timestampValue(account.rotation_due_at)
          case 'expires': return timestampValue(account.expires_at)
          case 'updated': return timestampValue(account.updated_at)
          case 'account':
          default:
            return account.name
        }
      }
      return comparePrimitive(valueFor(a), valueFor(b), accountSort.direction)
    })
  }, [accountFilters, accountSort, accounts, domains, projects])

  const filteredActivityEvents = useMemo(() => {
    const filtered = activityEvents.filter(event => includesQuery([
      event.actor_subject,
      event.actor_display_name,
      event.actor_type,
      event.action,
      eventResourceLabel(event),
      eventScopeLabel(event, projects, domains),
      event.status,
      detailsSummary(event.details),
    ], activitySearch))

    return stableSort(filtered, (a, b) => {
      const valueFor = (event: DashboardAuditEvent): string | number => {
        switch (activitySort.field) {
          case 'actor': return event.actor_display_name || event.actor_subject || event.actor_type
          case 'action': return event.action
          case 'resource': return eventResourceLabel(event)
          case 'scope': return eventScopeLabel(event, projects, domains)
          case 'status': return event.status
          case 'details': return detailsSummary(event.details)
          case 'time':
          default:
            return timestampValue(event.created_at)
        }
      }
      return comparePrimitive(valueFor(a), valueFor(b), activitySort.direction)
    })
  }, [activityEvents, activitySearch, activitySort, domains, projects])

  const filteredUsageBuckets = useMemo(() => {
    const filtered = userBuckets.filter(bucket => includesQuery([
      bucketLabel(bucket),
      bucket.run_count,
      bucket.total_vusers,
      bucket.vuser_seconds,
    ], usageSearch))

    return stableSort(filtered, (a, b) => {
      const valueFor = (bucket: UsageAccountingBucket): string | number => {
        switch (usageSort.field) {
          case 'runs': return bucket.run_count
          case 'total_vusers': return bucket.total_vusers
          case 'duration': return bucket.duration_seconds
          case 'vuser_seconds': return bucket.vuser_seconds
          case 'user':
          default:
            return bucketLabel(bucket)
        }
      }
      return comparePrimitive(valueFor(a), valueFor(b), usageSort.direction)
    })
  }, [usageSearch, usageSort, userBuckets])

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
      const response = await api.listAuthUsers({ limit: 100 })
      setUsers(response.users || [])
      setUserActionError('')
    } catch (error: unknown) {
      setUserActionError(getErrorMessage(error, 'Unable to load users'))
      setUsers([])
    } finally {
      setUsersLoading(false)
    }
  }, [api, canManageUsers])

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
    if (users.length === 0) {
      setSelectedUserID('')
      return
    }
    if (!selectedUserID || !users.some(user => user.id === selectedUserID)) {
      setSelectedUserID(users[0].id)
    }
  }, [selectedUserID, users])

  useEffect(() => {
    if (accounts.length === 0) {
      setSelectedAccountID('')
      return
    }
    if (!selectedAccountID || !accounts.some(account => account.id === selectedAccountID)) {
      setSelectedAccountID(accounts[0].id)
    }
  }, [accounts, selectedAccountID])

  useEffect(() => {
    if (activityEvents.length === 0) {
      setSelectedActivityID('')
      return
    }
    if (!selectedActivityID || !activityEvents.some(event => event.id === selectedActivityID)) {
      setSelectedActivityID(activityEvents[0].id)
    }
  }, [activityEvents, selectedActivityID])

  useEffect(() => {
    if (!selectedUser) {
      setEditUserForm({ subject: '', email: '', displayName: '' })
      setEditBindingDrafts([])
      return
    }
    setEditUserForm({
      subject: selectedUser.subject || '',
      email: selectedUser.email || '',
      displayName: selectedUser.display_name || '',
    })
    setEditBindingDrafts((selectedUser.bindings || []).map(userBindingDraftFromBinding))
  }, [selectedUser])

  useEffect(() => {
    setOnboardBindingDrafts(prev => prev.length > 0 ? prev : initialUserBindingDrafts(projects, domains, activeProject, activeDomain))
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
    const initialBindings = normalizedUserBindingDrafts(onboardBindingDrafts)
    if (initialBindings.length === 0) {
      setUserActionError('Choose at least one initial role binding.')
      setUserActionMessage('')
      return
    }
    try {
      const user = await api.createAuthUser({
        subject: userForm.subject.trim(),
        email: userForm.email.trim() || undefined,
        display_name: userForm.displayName.trim() || undefined,
      })
      for (const binding of initialBindings) {
        await api.createAuthUserBinding(user.id, {
          scope_type: binding.scopeType,
          scope_id: binding.scopeType === 'global' ? undefined : binding.scopeID,
          role: binding.role,
        })
      }
      setSelectedUserID(user.id)
      setUserInspectorCollapsed(false)
      setUserSubTab('existing')
      setUserForm({ subject: '', email: '', displayName: '' })
      setOnboardBindingDrafts(initialUserBindingDrafts(projects, domains, activeProject, activeDomain))
      setSelectedUserEditMode(false)
      setUserActionMessage(`Onboarded ${user.display_name || user.email || user.subject} with ${initialBindings.length} role ${initialBindings.length === 1 ? 'binding' : 'bindings'}.`)
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
    const desiredBindings = normalizedUserBindingDrafts(editBindingDrafts)
    try {
      const user = await api.updateAuthUser(selectedUser.id, {
        subject: editUserForm.subject.trim(),
        email: editUserForm.email.trim() || undefined,
        display_name: editUserForm.displayName.trim() || undefined,
      })
      const desiredSignatures = new Set(desiredBindings.map(draftSignature))
      const existingBindings = selectedUser.bindings || []
      const existingSignatures = new Set(existingBindings.map(bindingSignature))
      for (const binding of existingBindings) {
        if (!desiredSignatures.has(bindingSignature(binding))) {
          await api.deleteAuthUserBinding(selectedUser.id, binding.id)
        }
      }
      for (const binding of desiredBindings) {
        if (!existingSignatures.has(draftSignature(binding))) {
          await api.createAuthUserBinding(selectedUser.id, {
            scope_type: binding.scopeType,
            scope_id: binding.scopeType === 'global' ? undefined : binding.scopeID,
            role: binding.role,
          })
        }
      }
      setSelectedUserID(user.id)
      setSelectedUserEditMode(false)
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
    const action = active ? 'restore access for' : 'remove access from'
    if (typeof window !== 'undefined' && !window.confirm(`Really ${action} ${user.display_name || user.subject}?`)) {
      return
    }
    try {
      await (active ? api.enableAuthUser(user.id) : api.disableAuthUser(user.id))
      setUserActionMessage(`${active ? 'Restored access for' : 'Removed access from'} ${user.display_name || user.subject}.`)
      setUserActionError('')
      await loadUsers()
    } catch (error: unknown) {
      setUserActionError(getErrorMessage(error, `Unable to ${active ? 'restore' : 'remove'} user access`))
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
      setSelectedAccountID(response.service_account.id)
      setAccountInspectorCollapsed(false)
      setServiceAccountSubTab('existing')
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
      setSelectedAccountID(response.service_account.id)
      setAccountInspectorCollapsed(false)
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

  const addBindingDraft = (setter: Dispatch<SetStateAction<UserBindingForm[]>>, scopeType: DashboardUserRoleScopeType) => {
    setter(prev => [...prev, createUserBindingDraft(scopeType, projects, domains, activeProject, activeDomain)])
  }

  const updateBindingDraft = (setter: Dispatch<SetStateAction<UserBindingForm[]>>, draftID: string, updates: Partial<UserBindingForm>) => {
    setter(prev => prev.map(draft => {
      if (draft.id !== draftID) return draft
      if (updates.scopeType && updates.scopeType !== draft.scopeType) {
        return {
          ...draft,
          ...updates,
          scopeID: defaultScopeID(updates.scopeType, projects, domains, activeProject, activeDomain),
          role: roleOptionsForScope(updates.scopeType)[0]?.id || draft.role,
        }
      }
      return { ...draft, ...updates }
    }))
  }

  const removeBindingDraft = (setter: Dispatch<SetStateAction<UserBindingForm[]>>, draftID: string) => {
    setter(prev => prev.filter(draft => draft.id !== draftID))
  }

  const resetSelectedUserEditDrafts = () => {
    if (!selectedUser) return
    setEditUserForm({
      subject: selectedUser.subject || '',
      email: selectedUser.email || '',
      displayName: selectedUser.display_name || '',
    })
    setEditBindingDrafts((selectedUser.bindings || []).map(userBindingDraftFromBinding))
  }

  const selectUser = (user: DashboardUser) => {
    setSelectedUserID(user.id)
    setUserInspectorCollapsed(false)
    setSelectedUserEditMode(false)
  }

  const editUser = (user: DashboardUser) => {
    setSelectedUserID(user.id)
    setUserInspectorCollapsed(false)
    setSelectedUserEditMode(true)
  }

  const selectAccount = (account: DashboardServiceAccount) => {
    setSelectedAccountID(account.id)
    setAccountInspectorCollapsed(false)
  }

  const selectActivity = (event: DashboardAuditEvent) => {
    setSelectedActivityID(event.id)
    setActivityInspectorCollapsed(false)
  }

  const renderUserBindingDraftEditor = (drafts: UserBindingForm[], setter: Dispatch<SetStateAction<UserBindingForm[]>>) => (
    <div className="access-binding-editor">
      <div className="access-binding-draft-list">
        {drafts.length > 0 ? drafts.map((draft, index) => (
          <div className="access-binding-draft-row" key={draft.id}>
            <div className="access-binding-draft-heading">
              <strong>Binding {index + 1}</strong>
              <button className="btn btn-ghost btn-sm access-danger-action" type="button" onClick={() => removeBindingDraft(setter, draft.id)}>
                Remove
              </button>
            </div>
            <div className="access-binding-draft-grid">
              <label>
                <span>Scope</span>
                <select value={draft.scopeType} onChange={event => updateBindingDraft(setter, draft.id, { scopeType: event.target.value as DashboardUserRoleScopeType })}>
                  <option value="global">Global</option>
                  <option value="domain">Domain</option>
                  <option value="project">Project</option>
                </select>
              </label>
              {draft.scopeType === 'domain' && (
                <label>
                  <span>Domain</span>
                  <select value={draft.scopeID} onChange={event => updateBindingDraft(setter, draft.id, { scopeID: event.target.value })}>
                    {domains.map(domain => <option key={domain.id} value={domain.id}>{domain.name}</option>)}
                    {domains.length === 0 && activeDomain && <option value={activeDomain.id}>{activeDomain.name}</option>}
                  </select>
                </label>
              )}
              {draft.scopeType === 'project' && (
                <label>
                  <span>Project</span>
                  <select value={draft.scopeID} onChange={event => updateBindingDraft(setter, draft.id, { scopeID: event.target.value })}>
                    {projects.map(project => <option key={project.id} value={project.id}>{project.name}</option>)}
                  </select>
                </label>
              )}
              <label>
                <span>Role</span>
                <select value={draft.role} onChange={event => updateBindingDraft(setter, draft.id, { role: event.target.value })}>
                  {roleOptionsForScope(draft.scopeType).map(option => <option key={option.id} value={option.id}>{option.label}</option>)}
                </select>
              </label>
            </div>
          </div>
        )) : (
          <div className="access-empty-state compact">
            <strong>No role bindings selected</strong>
            <span>Add a global, domain, or project binding before saving.</span>
          </div>
        )}
      </div>
      <div className="access-form-actions access-binding-add-actions">
        <button className="btn btn-secondary btn-sm" type="button" onClick={() => addBindingDraft(setter, 'global')}>Add Global Role</button>
        <button className="btn btn-secondary btn-sm" type="button" onClick={() => addBindingDraft(setter, 'domain')} disabled={domains.length === 0 && !activeDomain}>Add Domain</button>
        <button className="btn btn-secondary btn-sm" type="button" onClick={() => addBindingDraft(setter, 'project')} disabled={projects.length === 0}>Add Project</button>
      </div>
    </div>
  )

  const renderUserManagementPanel = () => {
    if (!selectedUser) return null
    if (!selectedUserEditMode) {
      return (
        <div className="access-user-management readonly">
          <div className="access-control-section">
            <div className="access-section-heading">
              <div>
                <h4>User Access</h4>
                <p>Review assigned roles, domains, and projects. Click Edit to change access.</p>
              </div>
              <button className="btn btn-secondary btn-sm" type="button" onClick={() => setSelectedUserEditMode(true)} disabled={!canManageUsers}>
                Edit
              </button>
            </div>
            <div className="access-binding-list readonly">
              {(selectedUser.bindings || []).length > 0 ? selectedUser.bindings?.map(binding => (
                <div className="access-binding-row" key={binding.id}>
                  <div>
                    <strong>{ROLE_LABELS[binding.role] || binding.role}</strong>
                    <span>{SCOPE_LABELS[binding.scope_type] || binding.scope_type} · {bindingLabel(binding, projects, domains)}</span>
                  </div>
                </div>
              )) : (
                <p className="access-muted">No role bindings assigned.</p>
              )}
            </div>
          </div>
        </div>
      )
    }
    return (
      <div className="access-user-management">
        <div className="access-control-section">
          <div className="access-section-heading">
            <div>
              <h4>Edit User</h4>
              <p>Profile updates and access changes apply to {selectedUser.display_name || selectedUser.email || selectedUser.subject}.</p>
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
            <button className="btn btn-primary btn-sm" type="button" onClick={updateSelectedUser} disabled={!canManageUsers}>Save Changes</button>
            <button className="btn btn-secondary btn-sm" type="button" onClick={() => { resetSelectedUserEditDrafts(); setSelectedUserEditMode(false) }}>Cancel</button>
            <button className="btn btn-secondary btn-sm" type="button" onClick={() => setUserActive(selectedUser, !selectedUser.active)} disabled={!canManageUsers}>
              {selectedUser.active ? 'Remove Access' : 'Restore Access'}
            </button>
          </div>
        </div>

        <div className="access-control-section">
          <h4>Roles, Domains, and Projects</h4>
          <p>Assign as many global, domain, and project bindings as this user needs.</p>
          {renderUserBindingDraftEditor(editBindingDrafts, setEditBindingDrafts)}
        </div>
      </div>
    )
  }

  const renderUserInspector = () => {
    if (!selectedUser || userInspectorCollapsed) return null
    const selectedUsage = usageForUser(selectedUser, userBuckets)
    return (
      <aside className="access-inspector access-panel" aria-label="Selected user details">
        <div className="access-panel-header">
          <div>
            <h3>User Details</h3>
            <p>{selectedUser.display_name || selectedUser.email || selectedUser.subject}</p>
          </div>
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => setUserInspectorCollapsed(true)}>Hide Details</button>
        </div>
        <div className="access-inspector-title">
          <span className={`access-status-badge ${userStatusBadge(selectedUser).tone}`}>{userStatusBadge(selectedUser).label}</span>
          <strong>{selectedUser.subject}</strong>
        </div>
        <dl className="access-detail-grid">
          <div><dt>Email</dt><dd>{selectedUser.email || 'None'}</dd></div>
          <div><dt>Last Seen</dt><dd>{formatTimestamp(selectedUser.last_seen_at)}</dd></div>
          <div><dt>Created</dt><dd>{formatTimestamp(selectedUser.created_at)}</dd></div>
          <div><dt>Updated</dt><dd>{formatTimestamp(selectedUser.updated_at)}</dd></div>
          <div><dt>Usage</dt><dd>{canViewUserActivity ? usageSummary(selectedUsage) : 'Unavailable'}</dd></div>
        </dl>

        <div className="access-control-section access-readonly-section">
          <h4>Role Bindings</h4>
          <div className="access-binding-list readonly">
            {(selectedUser.bindings || []).length > 0 ? selectedUser.bindings?.map(binding => (
              <div className="access-binding-row" key={binding.id}>
                <div>
                  <strong>{ROLE_LABELS[binding.role] || binding.role}</strong>
                  <span>{SCOPE_LABELS[binding.scope_type] || binding.scope_type} · {bindingLabel(binding, projects, domains)}</span>
                </div>
              </div>
            )) : (
              <p className="access-muted">No role bindings assigned.</p>
            )}
          </div>
        </div>
      </aside>
    )
  }

  const renderAccountInspector = () => {
    if (!selectedAccount || accountInspectorCollapsed) return null
    const roles = accountRoleList(selectedAccount)
    const accessLabels = accountAccessLabels(selectedAccount, projects, domains)
    return (
      <aside className="access-inspector access-panel" aria-label="Selected service account details">
        <div className="access-panel-header">
          <div>
            <h3>Account Details</h3>
            <p>{selectedAccount.name}</p>
          </div>
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => setAccountInspectorCollapsed(true)}>Hide Details</button>
        </div>
        <div className="access-badge-row">
          {accountBadges(selectedAccount).map(badge => (
            <span className={`access-status-badge ${badge.tone}`} key={badge.label}>{badge.label}</span>
          ))}
        </div>
        <dl className="access-detail-grid">
          <div><dt>Description</dt><dd>{selectedAccount.description || 'None'}</dd></div>
          <div><dt>Roles</dt><dd>{formatRoleList(roles)}</dd></div>
          <div><dt>Scopes</dt><dd>{(selectedAccount.scopes || ['automation:*']).join(', ')}</dd></div>
          <div><dt>Access</dt><dd>{accessLabels.join(' | ') || 'Global only'}</dd></div>
          <div><dt>Last Used</dt><dd>{formatTimestamp(selectedAccount.last_used_at)}</dd></div>
          <div><dt>Rotated</dt><dd>{formatTimestamp(selectedAccount.key_rotated_at)}</dd></div>
          <div><dt>Rotation Due</dt><dd>{formatTimestamp(selectedAccount.rotation_due_at)}</dd></div>
          <div><dt>Expires</dt><dd>{selectedAccount.expires_at ? formatTimestamp(selectedAccount.expires_at) : 'No expiration'}</dd></div>
          <div><dt>Created</dt><dd>{formatTimestamp(selectedAccount.created_at)}</dd></div>
          <div><dt>Updated</dt><dd>{formatTimestamp(selectedAccount.updated_at)}</dd></div>
        </dl>
        <div className="access-account-actions">
          <button className="btn btn-secondary btn-sm" type="button" onClick={() => rotateAccount(selectedAccount)} disabled={!canManageServiceAccounts}>Rotate Key</button>
          <button className="btn btn-secondary btn-sm" type="button" onClick={() => setAccountActive(selectedAccount, !selectedAccount.active)} disabled={!canManageServiceAccounts}>
            {selectedAccount.active ? 'Disable' : 'Enable'}
          </button>
          <button className="btn btn-ghost btn-sm access-danger-action" type="button" onClick={() => deleteAccount(selectedAccount)} disabled={!canManageServiceAccounts}>Delete</button>
        </div>
      </aside>
    )
  }

  const renderActivityInspector = () => {
    if (!selectedActivity || activityInspectorCollapsed) return null
    return (
      <aside className="access-inspector access-panel" aria-label="Selected activity event details">
        <div className="access-panel-header">
          <div>
            <h3>Event Details</h3>
            <p>{selectedActivity.action}</p>
          </div>
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => setActivityInspectorCollapsed(true)}>Hide Details</button>
        </div>
        <span className={`access-status-badge ${selectedActivity.status === 'success' ? 'healthy' : 'warning'}`}>{selectedActivity.status}</span>
        <dl className="access-detail-grid">
          <div><dt>Time</dt><dd>{formatTimestamp(selectedActivity.created_at)}</dd></div>
          <div><dt>Actor</dt><dd>{selectedActivity.actor_display_name || selectedActivity.actor_subject || selectedActivity.actor_type}</dd></div>
          <div><dt>Actor Type</dt><dd>{selectedActivity.actor_type}</dd></div>
          <div><dt>Resource</dt><dd>{eventResourceLabel(selectedActivity)}</dd></div>
          <div><dt>Scope</dt><dd>{eventScopeLabel(selectedActivity, projects, domains)}</dd></div>
          <div><dt>Details</dt><dd>{detailsSummary(selectedActivity.details)}</dd></div>
        </dl>
      </aside>
    )
  }

  return (
    <div className="page access-page">
      <div className="page-header">
        <div>
          <h2>Access</h2>
          <p className="page-subtitle">Users, automation keys, and activity across domains and projects.</p>
        </div>
        {!hasTopbarContributionProvider && (
          <div className="access-header-actions">
            <span className={`access-permission-badge ${(canManageUsers || canManageServiceAccounts || canViewUserActivity) ? 'healthy' : 'danger'}`}>
              {(canManageUsers || canManageServiceAccounts || canViewUserActivity) ? 'Access workspace available' : 'Read-only session'}
            </span>
          </div>
        )}
      </div>

      {!hasTopbarContributionProvider && accessTabControls}

      {activeTab === 'users' && (
        <section className="access-workspace" role="tabpanel" aria-label="Users access workspace">
          <article className="access-panel access-table-panel">
            <div className="access-panel-header">
              <div>
                <h3>Users</h3>
                <p>{usersLoading ? 'Loading users...' : `${filteredUsers.length} of ${users.length} local users`}</p>
              </div>
              {selectedUser && userInspectorCollapsed && (
                <button className="btn btn-secondary btn-sm" type="button" onClick={() => setUserInspectorCollapsed(false)}>Show Details</button>
              )}
            </div>
            <div className="access-subtab-row" role="tablist" aria-label="User workspace views">
              <button type="button" role="tab" aria-selected={userSubTab === 'existing'} className={`access-subtab ${userSubTab === 'existing' ? 'active' : ''}`} onClick={() => setUserSubTab('existing')}>Existing Users</button>
              <button type="button" role="tab" aria-selected={userSubTab === 'onboard'} className={`access-subtab ${userSubTab === 'onboard' ? 'active' : ''}`} onClick={() => setUserSubTab('onboard')}>Onboard Users</button>
            </div>

            {userSubTab === 'existing' && (
              <>
                <div className="access-table-toolbar">
                  <label>
                    <span>Search</span>
                    <input value={userFilters.query} onChange={event => setUserFilters(prev => ({ ...prev, query: event.target.value }))} placeholder="name, email, role, scope" />
                  </label>
                  <label>
                    <span>Status</span>
                    <select value={userFilters.status} onChange={event => setUserFilters(prev => ({ ...prev, status: event.target.value }))}>
                      <option value="">All statuses</option>
                      <option value="active">Active</option>
                      <option value="disabled">Disabled</option>
                    </select>
                  </label>
                  <label>
                    <span>Role</span>
                    <select value={userFilters.role} onChange={event => setUserFilters(prev => ({ ...prev, role: event.target.value }))}>
                      <option value="">All roles</option>
                      {roleFilterOptions.map(role => <option key={role} value={role}>{ROLE_LABELS[role] || role}</option>)}
                    </select>
                  </label>
                  <label>
                    <span>Domain</span>
                    <select value={userFilters.domainID} onChange={event => setUserFilters(prev => ({ ...prev, domainID: event.target.value }))}>
                      <option value="">All domains</option>
                      {domains.map(domain => <option key={domain.id} value={domain.id}>{domain.name}</option>)}
                    </select>
                  </label>
                  <label>
                    <span>Project</span>
                    <select value={userFilters.projectID} onChange={event => setUserFilters(prev => ({ ...prev, projectID: event.target.value }))}>
                      <option value="">All projects</option>
                      {projects.map(project => <option key={project.id} value={project.id}>{project.name}</option>)}
                    </select>
                  </label>
                </div>

                {(userActionError || userActionMessage) && (
                  <div className={`access-alert ${userActionError ? 'error' : 'success'}`}>
                    {userActionError || userActionMessage}
                  </div>
                )}

                <div className="access-table-wrap">
                  <table className="access-data-table">
                    <thead>
                      <tr>
                        <SortHeader label="User" field="user" sort={userSort} onSort={field => setUserSort(prev => nextSort(prev, field, userSortDefaults))} />
                        <SortHeader label="Status" field="status" sort={userSort} onSort={field => setUserSort(prev => nextSort(prev, field, userSortDefaults))} />
                        <SortHeader label="Email" field="email" sort={userSort} onSort={field => setUserSort(prev => nextSort(prev, field, userSortDefaults))} />
                        <SortHeader label="Roles" field="roles" sort={userSort} onSort={field => setUserSort(prev => nextSort(prev, field, userSortDefaults))} />
                        <SortHeader label="Domains / Projects" field="access" sort={userSort} onSort={field => setUserSort(prev => nextSort(prev, field, userSortDefaults))} />
                        <SortHeader label="Last Seen" field="last_seen" sort={userSort} onSort={field => setUserSort(prev => nextSort(prev, field, userSortDefaults))} />
                        <SortHeader label="Created" field="created" sort={userSort} onSort={field => setUserSort(prev => nextSort(prev, field, userSortDefaults))} />
                        <SortHeader label="Updated" field="updated" sort={userSort} onSort={field => setUserSort(prev => nextSort(prev, field, userSortDefaults))} />
                        <SortHeader label="Usage" field="usage" sort={userSort} onSort={field => setUserSort(prev => nextSort(prev, field, userSortDefaults))} numeric />
                        <th>Actions</th>
                      </tr>
                    </thead>
                    <tbody>
                      {filteredUsers.map(user => {
                        const roles = userRoleList(user)
                        const accessLabels = userAccessLabels(user, projects, domains)
                        const usage = usageForUser(user, userBuckets)
                        const selected = selectedUserID === user.id && !userInspectorCollapsed
                        return (
                          <tr key={user.id} className={selected ? 'selected' : ''} onClick={() => selectUser(user)}>
                            <td>
                              <button
                                type="button"
                                className="access-row-primary"
                                onClick={event => {
                                  event.stopPropagation()
                                  selectUser(user)
                                }}
                              >
                                {user.display_name || user.email || user.subject}
                              </button>
                              <span className="access-row-meta">{user.subject}</span>
                            </td>
                            <td><span className={`access-status-badge ${userStatusBadge(user).tone}`}>{userStatusBadge(user).label}</span></td>
                            <td>{user.email || 'None'}</td>
                            <td>{roles.length > 0 ? roles.map(role => <span className="access-table-chip" key={role}>{ROLE_LABELS[role] || role}</span>) : 'None'}</td>
                            <td><span className="access-table-stack">{accessLabels.join(' | ') || 'No scoped access'}</span></td>
                            <td>{formatTimestamp(user.last_seen_at)}</td>
                            <td>{formatTimestamp(user.created_at)}</td>
                            <td>{formatTimestamp(user.updated_at)}</td>
                            <td className="num">{canViewUserActivity ? usageSummary(usage) : 'Unavailable'}</td>
                            <td>
                              <div className="access-table-actions">
                                <button className="btn btn-secondary btn-sm" type="button" onClick={event => { event.stopPropagation(); selectUser(user) }}>Inspect</button>
                                <button className="btn btn-secondary btn-sm" type="button" onClick={event => { event.stopPropagation(); editUser(user) }}>Edit</button>
                                <button className="btn btn-ghost btn-sm" type="button" onClick={event => { event.stopPropagation(); setUserActive(user, !user.active) }}>
                                  {user.active ? 'Remove Access' : 'Restore Access'}
                                </button>
                              </div>
                            </td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                  {filteredUsers.length === 0 && (
                    <div className="access-empty-state">
                      <strong>No users found</strong>
                      <span>Adjust search or filters.</span>
                    </div>
                  )}
                </div>
                {renderUserManagementPanel()}
              </>
            )}

            {userSubTab === 'onboard' && (
              <>
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
                <div className="access-control-section">
                  <h4>Initial Permissions</h4>
                  <p>Assign the user's starting global, domain, and project roles before the account is created.</p>
                  {renderUserBindingDraftEditor(onboardBindingDrafts, setOnboardBindingDrafts)}
                </div>
                {(userActionError || userActionMessage) && (
                  <div className={`access-alert ${userActionError ? 'error' : 'success'}`}>
                    {userActionError || userActionMessage}
                  </div>
                )}
                <div className="access-form-actions">
                  <button className="btn btn-primary" type="button" onClick={createUser} disabled={!canManageUsers || !userForm.subject.trim()}>
                    Create User
                  </button>
                </div>
              </>
            )}
          </article>
          {renderUserInspector()}
        </section>
      )}

      {activeTab === 'service_accounts' && (
        <section className="access-workspace" role="tabpanel" aria-label="Service accounts access workspace">
          <article className="access-panel access-table-panel">
            <div className="access-panel-header">
              <div>
                <h3>Service Accounts</h3>
                <p>{loading ? 'Loading service accounts...' : `${filteredAccounts.length} of ${accounts.length} automation principals`}</p>
              </div>
              {selectedAccount && accountInspectorCollapsed && (
                <button className="btn btn-secondary btn-sm" type="button" onClick={() => setAccountInspectorCollapsed(false)}>Show Details</button>
              )}
            </div>
            <div className="access-subtab-row" role="tablist" aria-label="Service account workspace views">
              <button type="button" role="tab" aria-selected={serviceAccountSubTab === 'existing'} className={`access-subtab ${serviceAccountSubTab === 'existing' ? 'active' : ''}`} onClick={() => setServiceAccountSubTab('existing')}>Existing Accounts</button>
              <button type="button" role="tab" aria-selected={serviceAccountSubTab === 'generate'} className={`access-subtab ${serviceAccountSubTab === 'generate' ? 'active' : ''}`} onClick={() => setServiceAccountSubTab('generate')}>Generate Key</button>
            </div>

            {serviceAccountSubTab === 'existing' && (
              <>
                <div className="access-table-toolbar">
                  <label>
                    <span>Search</span>
                    <input value={accountFilters.query} onChange={event => setAccountFilters(prev => ({ ...prev, query: event.target.value }))} placeholder="account, scope, role" />
                  </label>
                  <label>
                    <span>Status</span>
                    <select value={accountFilters.status} onChange={event => setAccountFilters(prev => ({ ...prev, status: event.target.value }))}>
                      <option value="">All statuses</option>
                      <option value="active">Active</option>
                      <option value="disabled">Disabled</option>
                      <option value="expired">Expired</option>
                      <option value="rotation_due">Rotation due</option>
                      <option value="never_used">Never used</option>
                    </select>
                  </label>
                  <label>
                    <span>Role</span>
                    <select value={accountFilters.role} onChange={event => setAccountFilters(prev => ({ ...prev, role: event.target.value }))}>
                      <option value="">All roles</option>
                      {roleFilterOptions.map(role => <option key={role} value={role}>{ROLE_LABELS[role] || role}</option>)}
                    </select>
                  </label>
                  <label>
                    <span>Scope</span>
                    <select value={accountFilters.scope} onChange={event => setAccountFilters(prev => ({ ...prev, scope: event.target.value }))}>
                      <option value="">All scopes</option>
                      {scopeFilterOptions.map(scope => <option key={scope} value={scope}>{SCOPE_OPTIONS.find(option => option.id === scope)?.label || scope}</option>)}
                    </select>
                  </label>
                  <label>
                    <span>Domain</span>
                    <select value={accountFilters.domainID} onChange={event => setAccountFilters(prev => ({ ...prev, domainID: event.target.value }))}>
                      <option value="">All domains</option>
                      {domains.map(domain => <option key={domain.id} value={domain.id}>{domain.name}</option>)}
                    </select>
                  </label>
                  <label>
                    <span>Project</span>
                    <select value={accountFilters.projectID} onChange={event => setAccountFilters(prev => ({ ...prev, projectID: event.target.value }))}>
                      <option value="">All projects</option>
                      {projects.map(project => <option key={project.id} value={project.id}>{project.name}</option>)}
                    </select>
                  </label>
                </div>

                {(actionError || actionMessage) && (
                  <div className={`access-alert ${actionError ? 'error' : 'success'}`}>
                    {actionError || actionMessage}
                  </div>
                )}

                <div className="access-table-wrap">
                  <table className="access-data-table">
                    <thead>
                      <tr>
                        <SortHeader label="Account" field="account" sort={accountSort} onSort={field => setAccountSort(prev => nextSort(prev, field, accountSortDefaults))} />
                        <SortHeader label="Status" field="status" sort={accountSort} onSort={field => setAccountSort(prev => nextSort(prev, field, accountSortDefaults))} />
                        <SortHeader label="Roles" field="roles" sort={accountSort} onSort={field => setAccountSort(prev => nextSort(prev, field, accountSortDefaults))} />
                        <SortHeader label="Domains / Projects" field="access" sort={accountSort} onSort={field => setAccountSort(prev => nextSort(prev, field, accountSortDefaults))} />
                        <SortHeader label="Scopes" field="scopes" sort={accountSort} onSort={field => setAccountSort(prev => nextSort(prev, field, accountSortDefaults))} />
                        <SortHeader label="Last Used" field="last_used" sort={accountSort} onSort={field => setAccountSort(prev => nextSort(prev, field, accountSortDefaults))} />
                        <SortHeader label="Rotation Due" field="rotation_due" sort={accountSort} onSort={field => setAccountSort(prev => nextSort(prev, field, accountSortDefaults))} />
                        <SortHeader label="Expires" field="expires" sort={accountSort} onSort={field => setAccountSort(prev => nextSort(prev, field, accountSortDefaults))} />
                        <SortHeader label="Updated" field="updated" sort={accountSort} onSort={field => setAccountSort(prev => nextSort(prev, field, accountSortDefaults))} />
                        <th>Actions</th>
                      </tr>
                    </thead>
                    <tbody>
                      {filteredAccounts.map(account => {
                        const selected = selectedAccountID === account.id && !accountInspectorCollapsed
                        const roles = accountRoleList(account)
                        const accessLabels = accountAccessLabels(account, projects, domains)
                        return (
                          <tr key={account.id} className={selected ? 'selected' : ''} onClick={() => selectAccount(account)}>
                            <td>
                              <button type="button" className="access-row-primary" onClick={event => { event.stopPropagation(); selectAccount(account) }}>
                                {account.name}
                              </button>
                              <span className="access-row-meta">{account.description || 'No description'}</span>
                            </td>
                            <td>
                              <div className="access-badge-row">
                                {accountBadges(account).map(badge => (
                                  <span className={`access-status-badge ${badge.tone}`} key={badge.label}>{badge.label}</span>
                                ))}
                              </div>
                            </td>
                            <td>{roles.length > 0 ? roles.map(role => <span className="access-table-chip" key={role}>{ROLE_LABELS[role] || role}</span>) : 'None'}</td>
                            <td><span className="access-table-stack">{accessLabels.join(' | ') || 'Global only'}</span></td>
                            <td><span className="access-table-stack">{(account.scopes || ['automation:*']).join(', ')}</span></td>
                            <td>{formatTimestamp(account.last_used_at)}</td>
                            <td>{formatTimestamp(account.rotation_due_at)}</td>
                            <td>{account.expires_at ? formatTimestamp(account.expires_at) : 'No expiration'}</td>
                            <td>{formatTimestamp(account.updated_at)}</td>
                            <td>
                              <div className="access-table-actions">
                                <button className="btn btn-secondary btn-sm" type="button" onClick={event => { event.stopPropagation(); selectAccount(account) }}>Inspect</button>
                                <button className="btn btn-secondary btn-sm" type="button" onClick={event => { event.stopPropagation(); rotateAccount(account) }}>Rotate Key</button>
                                <button className="btn btn-ghost btn-sm" type="button" onClick={event => { event.stopPropagation(); setAccountActive(account, !account.active) }}>
                                  {account.active ? 'Disable' : 'Enable'}
                                </button>
                                <button className="btn btn-ghost btn-sm access-danger-action" type="button" onClick={event => { event.stopPropagation(); deleteAccount(account) }}>Delete</button>
                              </div>
                            </td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                  {filteredAccounts.length === 0 && (
                    <div className="access-empty-state">
                      <strong>No service accounts found</strong>
                      <span>Adjust search or filters.</span>
                    </div>
                  )}
                </div>
              </>
            )}

            {serviceAccountSubTab === 'generate' && (
              <>
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
              </>
            )}
          </article>
          {renderAccountInspector()}
        </section>
      )}

      {activeTab === 'activity' && (
        <section className="access-workspace" role="tabpanel" aria-label="Activity access workspace">
          <article className="access-panel access-table-panel">
            <div className="access-panel-header">
              <div>
                <h3>Activity</h3>
                <p>{activityLoading ? 'Loading activity...' : `${activityEvents.length} audit events`}</p>
              </div>
              <div className="access-account-actions">
                {selectedActivity && activityInspectorCollapsed && (
                  <button className="btn btn-secondary btn-sm" type="button" onClick={() => setActivityInspectorCollapsed(false)}>Show Details</button>
                )}
                <button className="btn btn-secondary btn-sm" type="button" onClick={exportActivity} disabled={!canViewUserActivity}>Export CSV</button>
              </div>
            </div>
            <div className="access-subtab-row" role="tablist" aria-label="Activity workspace views">
              <button type="button" role="tab" aria-selected={activitySubTab === 'events'} className={`access-subtab ${activitySubTab === 'events' ? 'active' : ''}`} onClick={() => setActivitySubTab('events')}>Events</button>
              <button type="button" role="tab" aria-selected={activitySubTab === 'usage'} className={`access-subtab ${activitySubTab === 'usage' ? 'active' : ''}`} onClick={() => setActivitySubTab('usage')}>Usage By User</button>
            </div>

            {activitySubTab === 'events' && (
              <>
                <div className="access-table-toolbar access-activity-filter-toolbar">
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
                  <button className="btn btn-primary btn-sm" type="button" onClick={loadActivity} disabled={!canViewUserActivity}>Apply Filters</button>
                </div>

                <div className="access-table-toolbar compact">
                  <label>
                    <span>Table Search</span>
                    <input value={activitySearch} onChange={event => setActivitySearch(event.target.value)} placeholder="actor, action, resource, details" />
                  </label>
                </div>

                {(activityError || activityMessage) && (
                  <div className={`access-alert ${activityError ? 'error' : 'success'}`}>
                    {activityError || activityMessage}
                  </div>
                )}

                <div className="access-table-wrap">
                  <table className="access-data-table">
                    <thead>
                      <tr>
                        <SortHeader label="Time" field="time" sort={activitySort} onSort={field => setActivitySort(prev => nextSort(prev, field, activitySortDefaults))} />
                        <SortHeader label="Actor" field="actor" sort={activitySort} onSort={field => setActivitySort(prev => nextSort(prev, field, activitySortDefaults))} />
                        <SortHeader label="Action" field="action" sort={activitySort} onSort={field => setActivitySort(prev => nextSort(prev, field, activitySortDefaults))} />
                        <SortHeader label="Resource" field="resource" sort={activitySort} onSort={field => setActivitySort(prev => nextSort(prev, field, activitySortDefaults))} />
                        <SortHeader label="Scope" field="scope" sort={activitySort} onSort={field => setActivitySort(prev => nextSort(prev, field, activitySortDefaults))} />
                        <SortHeader label="Status" field="status" sort={activitySort} onSort={field => setActivitySort(prev => nextSort(prev, field, activitySortDefaults))} />
                        <SortHeader label="Details" field="details" sort={activitySort} onSort={field => setActivitySort(prev => nextSort(prev, field, activitySortDefaults))} />
                      </tr>
                    </thead>
                    <tbody>
                      {filteredActivityEvents.map(event => {
                        const selected = selectedActivityID === event.id && !activityInspectorCollapsed
                        return (
                          <tr key={event.id} className={selected ? 'selected' : ''} onClick={() => selectActivity(event)}>
                            <td>{formatTimestamp(event.created_at)}</td>
                            <td>
                              <button type="button" className="access-row-primary" onClick={clickEvent => { clickEvent.stopPropagation(); selectActivity(event) }}>
                                {event.actor_display_name || event.actor_subject || event.actor_type}
                              </button>
                              <span className="access-row-meta">{event.actor_type}</span>
                            </td>
                            <td>{event.action}</td>
                            <td>{eventResourceLabel(event)}</td>
                            <td>{eventScopeLabel(event, projects, domains)}</td>
                            <td><span className={`access-status-badge ${event.status === 'success' ? 'healthy' : 'warning'}`}>{event.status}</span></td>
                            <td><span className="access-table-stack">{detailsSummary(event.details)}</span></td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                  {filteredActivityEvents.length === 0 && (
                    <div className="access-empty-state">
                      <strong>No activity found</strong>
                      <span>Adjust filters or select a broader timeframe.</span>
                    </div>
                  )}
                </div>
              </>
            )}

            {activitySubTab === 'usage' && (
              <>
                <div className="access-table-toolbar compact">
                  <label>
                    <span>Search</span>
                    <input value={usageSearch} onChange={event => setUsageSearch(event.target.value)} placeholder="user or attribution key" />
                  </label>
                </div>
                <div className="access-table-wrap">
                  <table className="access-data-table access-usage-table">
                    <thead>
                      <tr>
                        <SortHeader label="User" field="user" sort={usageSort} onSort={field => setUsageSort(prev => nextSort(prev, field, usageSortDefaults))} />
                        <SortHeader label="Runs" field="runs" sort={usageSort} onSort={field => setUsageSort(prev => nextSort(prev, field, usageSortDefaults))} numeric />
                        <SortHeader label="Total VUsers" field="total_vusers" sort={usageSort} onSort={field => setUsageSort(prev => nextSort(prev, field, usageSortDefaults))} numeric />
                        <SortHeader label="Duration" field="duration" sort={usageSort} onSort={field => setUsageSort(prev => nextSort(prev, field, usageSortDefaults))} numeric />
                        <SortHeader label="VUser Seconds" field="vuser_seconds" sort={usageSort} onSort={field => setUsageSort(prev => nextSort(prev, field, usageSortDefaults))} numeric />
                      </tr>
                    </thead>
                    <tbody>
                      {filteredUsageBuckets.map(bucket => (
                        <tr key={bucketLabel(bucket)}>
                          <td><strong>{bucketLabel(bucket)}</strong></td>
                          <td className="num">{formatNumber(bucket.run_count)}</td>
                          <td className="num">{formatNumber(bucket.total_vusers)}</td>
                          <td className="num">{formatDurationSeconds(bucket.duration_seconds)}</td>
                          <td className="num">{formatNumber(bucket.vuser_seconds)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                  {filteredUsageBuckets.length === 0 && (
                    <div className="access-empty-state">
                      <strong>No attributed usage</strong>
                      <span>New runs will appear after user attribution is recorded.</span>
                    </div>
                  )}
                </div>
              </>
            )}
          </article>
          {activitySubTab === 'events' && renderActivityInspector()}
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
