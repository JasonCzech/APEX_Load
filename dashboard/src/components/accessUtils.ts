// Shared constants, pure helpers, and SortHeader for the Access page workspaces
// (extracted from AccessPage.tsx, A16).
import {
  ApplicationRepository, Domain, Project,
  DashboardAuditEvent, DashboardAuditEventFilter, DashboardServiceAccount,
  DashboardUser, DashboardUserRoleBinding, DashboardUserRoleScopeType, UsageAccountingBucket,
} from '../types'
import {
  AccessTab, SortDirection, SortState, PurposePreset, ServiceAccountForm,
  UserBindingForm, ActivityFilterForm, UserSortField, AccountSortField,
  ActivitySortField, UsageSortField,
} from './accessTypes'

export const PURPOSE_PRESETS: PurposePreset[] = [
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

export const GLOBAL_ROLE_OPTIONS = [
  { id: 'viewer', label: 'Viewer' },
  { id: 'editor', label: 'Editor' },
  { id: 'project_admin', label: 'Project Admin' },
  { id: 'system_admin', label: 'System Admin' },
]

export const SCOPED_ROLE_OPTIONS = [
  { id: 'viewer', label: 'Viewer' },
  { id: 'editor', label: 'Editor' },
  { id: 'project_admin', label: 'Project Admin' },
]

export const SCOPE_OPTIONS = [
  { id: 'results:read', label: 'Results read' },
  { id: 'system:read', label: 'System read' },
  { id: 'tests:run', label: 'Tests run' },
  { id: 'scripts:write', label: 'Scripts write' },
  { id: 'settings:manage', label: 'Settings manage' },
  { id: 'service_accounts:manage', label: 'Service accounts manage' },
  { id: 'audit:read', label: 'Audit read' },
  { id: 'automation:*', label: 'Automation wildcard' },
]

export const ROLE_LABELS: Record<string, string> = {
  viewer: 'Viewer',
  editor: 'Editor',
  project_admin: 'Project Admin',
  system_admin: 'System Admin',
}

export const SCOPE_LABELS: Record<string, string> = {
  global: 'Global',
  domain: 'Domain',
  project: 'Project',
  application_repository: 'Repository',
}

export const userSortDefaults: Record<UserSortField, SortDirection> = {
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

export const accountSortDefaults: Record<AccountSortField, SortDirection> = {
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

export const activitySortDefaults: Record<ActivitySortField, SortDirection> = {
  time: 'desc',
  actor: 'asc',
  action: 'asc',
  resource: 'asc',
  scope: 'asc',
  status: 'asc',
  details: 'asc',
}

export const usageSortDefaults: Record<UsageSortField, SortDirection> = {
  user: 'asc',
  runs: 'desc',
  total_vusers: 'desc',
  duration: 'desc',
  vuser_seconds: 'desc',
}

export function preferredTab(canUseUsers: boolean, canManageServiceAccounts: boolean, canViewUserActivity: boolean): AccessTab {
  if (canUseUsers) return 'users'
  if (canManageServiceAccounts) return 'service_accounts'
  if (canViewUserActivity) return 'activity'
  return 'users'
}

export function presetByID(id: string): PurposePreset {
  return PURPOSE_PRESETS.find(preset => preset.id === id) || PURPOSE_PRESETS[1]
}

export function buildPresetForm(preset: PurposePreset, activeProjectID?: string): ServiceAccountForm {
  const projectRoles: Record<string, string> = {}
  if (activeProjectID && preset.projectRole) {
    projectRoles[activeProjectID] = preset.projectRole
  }
  return {
    purpose: preset.id,
    name: preset.defaultName,
    description: preset.description,
    owner: '',
    contact: '',
    globalRoles: preset.globalRoles,
    projectRoles,
    domainRoles: {},
    applicationRepositoryRoles: {},
    scopes: preset.scopes,
    expiresAt: '',
    rotationDays: '90',
  }
}

// formFromServiceAccount seeds the edit form from a stored account. Role maps are
// flattened from API shape (id -> roles[]) to the form's single-role-per-scope shape,
// keeping the first role for each scope.
export function formFromServiceAccount(account: DashboardServiceAccount): ServiceAccountForm {
  const firstRole = (roles?: Record<string, string[]>): Record<string, string> => {
    const out: Record<string, string> = {}
    Object.entries(roles || {}).forEach(([id, list]) => {
      if (id && list && list.length > 0) out[id] = list[0]
    })
    return out
  }
  const toLocalDateTime = (value?: string): string => {
    if (!value) return ''
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return ''
    // datetime-local needs YYYY-MM-DDTHH:mm in local time.
    const tzOffset = parsed.getTimezoneOffset() * 60000
    return new Date(parsed.getTime() - tzOffset).toISOString().slice(0, 16)
  }
  return {
    purpose: 'custom',
    name: account.name,
    description: account.description || '',
    owner: account.owner_id || '',
    contact: account.contact_email || '',
    globalRoles: account.global_roles || [],
    projectRoles: firstRole(account.project_roles),
    domainRoles: firstRole(account.domain_roles),
    applicationRepositoryRoles: firstRole(account.application_repository_roles),
    scopes: account.scopes || [],
    expiresAt: toLocalDateTime(account.expires_at),
    rotationDays: account.rotation_interval_days ? String(account.rotation_interval_days) : '90',
  }
}

export function buildActivityFilter(form: ActivityFilterForm): DashboardAuditEventFilter {
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

export function toggleValue(values: string[], value: string): string[] {
  return values.includes(value)
    ? values.filter(item => item !== value)
    : [...values, value]
}

export function toProjectRolesPayload(projectRoles: Record<string, string>): Record<string, string[]> {
  return Object.entries(projectRoles).reduce<Record<string, string[]>>((acc, [projectID, role]) => {
    if (projectID && role) {
      acc[projectID] = [role]
    }
    return acc
  }, {})
}

// mergeScopedRolesPayload builds the PATCH role map from the single-role-per-scope edit
// form WITHOUT silently dropping additional roles the form can't represent. The form only
// holds one role per scope (the first of the stored set); on save we keep the full original
// array whenever the chosen role is still part of it, and only collapse to [role] when the
// user picked a role outside the original set (an intentional change). Scopes the user
// unchecked are absent from formRoles and therefore correctly dropped.
export function mergeScopedRolesPayload(
  formRoles: Record<string, string>,
  originalRoles?: Record<string, string[]>,
): Record<string, string[]> {
  return Object.entries(formRoles).reduce<Record<string, string[]>>((acc, [id, role]) => {
    if (!id || !role) return acc
    const original = originalRoles?.[id]
    acc[id] = original && original.includes(role) ? original : [role]
    return acc
  }, {})
}

export function parseOptionalDateTime(value: string): string | undefined {
  const trimmed = value.trim()
  if (!trimmed) return undefined
  const parsed = new Date(trimmed)
  return Number.isNaN(parsed.getTime()) ? undefined : parsed.toISOString()
}

export function parseRotationDays(value: string): number {
  const parsed = Number(value)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : 90
}

export function timestampValue(value?: string): number {
  if (!value) return 0
  const parsed = Date.parse(value)
  return Number.isNaN(parsed) ? 0 : parsed
}

export function formatTimestamp(value?: string): string {
  if (!value) return 'Never'
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString()
}

export function formatDurationSeconds(value?: number): string {
  const total = Math.max(0, Math.round(value || 0))
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const seconds = total % 60
  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return `${minutes}m ${seconds}s`
  return `${seconds}s`
}

export function formatRoleList(roles?: string[]): string {
  if (!roles || roles.length === 0) return 'None'
  return roles.map(role => ROLE_LABELS[role] || role).join(', ')
}

export function roleOptionsForScope(scopeType: DashboardUserRoleScopeType) {
  return scopeType === 'global' ? GLOBAL_ROLE_OPTIONS : SCOPED_ROLE_OPTIONS
}

export function uniqueValues(values: string[]): string[] {
  return Array.from(new Set(values.filter(Boolean))).sort((a, b) => a.localeCompare(b))
}

export function normalize(value: unknown): string {
  return String(value ?? '').toLowerCase()
}

export function includesQuery(values: unknown[], query: string): boolean {
  const normalizedQuery = query.trim().toLowerCase()
  if (!normalizedQuery) return true
  return values.some(value => normalize(value).includes(normalizedQuery))
}

export function comparePrimitive(a: string | number, b: string | number, direction: SortDirection): number {
  const order = direction === 'asc' ? 1 : -1
  if (typeof a === 'number' && typeof b === 'number') {
    return (a - b) * order
  }
  return String(a).localeCompare(String(b), undefined, { numeric: true, sensitivity: 'base' }) * order
}

export function stableSort<T>(items: T[], compare: (a: T, b: T) => number): T[] {
  return items
    .map((item, index) => ({ item, index }))
    .sort((a, b) => compare(a.item, b.item) || a.index - b.index)
    .map(entry => entry.item)
}

export function nextSort<T extends string>(current: SortState<T>, field: T, defaults: Record<T, SortDirection>): SortState<T> {
  if (current.field === field) {
    return { field, direction: current.direction === 'asc' ? 'desc' : 'asc' }
  }
  return { field, direction: defaults[field] }
}

export function defaultScopeID(scopeType: DashboardUserRoleScopeType, projects: Project[], domains: Domain[], activeProject?: Project | null, activeDomain?: Domain | null, applicationRepository?: ApplicationRepository | null): string {
  if (scopeType === 'project') return activeProject?.id || projects[0]?.id || ''
  if (scopeType === 'domain') return activeDomain?.id || domains[0]?.id || activeProject?.domain_id || ''
  if (scopeType === 'application_repository') return applicationRepository?.id || ''
  return ''
}

export function createBindingDraftID(): string {
  return `binding-${Date.now()}-${Math.floor(Math.random() * 100000)}`
}

export function createUserBindingDraft(scopeType: DashboardUserRoleScopeType, projects: Project[], domains: Domain[], activeProject?: Project | null, activeDomain?: Domain | null, applicationRepository?: ApplicationRepository | null): UserBindingForm {
  return {
    id: createBindingDraftID(),
    scopeType,
    scopeID: defaultScopeID(scopeType, projects, domains, activeProject, activeDomain, applicationRepository),
    role: roleOptionsForScope(scopeType)[0]?.id || 'viewer',
  }
}

export function initialUserBindingDrafts(projects: Project[], domains: Domain[], activeProject?: Project | null, activeDomain?: Domain | null, applicationRepository?: ApplicationRepository | null): UserBindingForm[] {
  if (activeProject || projects.length > 0) {
    return [createUserBindingDraft('project', projects, domains, activeProject, activeDomain, applicationRepository)]
  }
  if (activeDomain || domains.length > 0) {
    return [createUserBindingDraft('domain', projects, domains, activeProject, activeDomain, applicationRepository)]
  }
  if (applicationRepository) {
    return [createUserBindingDraft('application_repository', projects, domains, activeProject, activeDomain, applicationRepository)]
  }
  return [createUserBindingDraft('global', projects, domains, activeProject, activeDomain, applicationRepository)]
}

export function userBindingDraftFromBinding(binding: DashboardUserRoleBinding): UserBindingForm {
  return {
    id: binding.id,
    bindingID: binding.id,
    scopeType: binding.scope_type,
    scopeID: binding.scope_id || '',
    role: binding.role,
  }
}

export function userBindingSignature(scopeType: DashboardUserRoleScopeType, scopeID: string | undefined, role: string): string {
  return `${scopeType}:${scopeType === 'global' ? '' : scopeID || ''}:${role}`
}

export function bindingSignature(binding: DashboardUserRoleBinding): string {
  return userBindingSignature(binding.scope_type, binding.scope_id, binding.role)
}

export function draftSignature(draft: UserBindingForm): string {
  return userBindingSignature(draft.scopeType, draft.scopeID, draft.role)
}

export function isValidUserBindingDraft(draft: UserBindingForm): boolean {
  if (!draft.role) return false
  if (draft.scopeType === 'global') return true
  return Boolean(draft.scopeID)
}

export function normalizedUserBindingDrafts(drafts: UserBindingForm[]): UserBindingForm[] {
  const seen = new Set<string>()
  return drafts.filter(draft => {
    if (!isValidUserBindingDraft(draft)) return false
    const signature = draftSignature(draft)
    if (seen.has(signature)) return false
    seen.add(signature)
    return true
  })
}

export function userStatusBadge(user: DashboardUser): { label: string; tone: string } {
  return user.active ? { label: 'Active', tone: 'healthy' } : { label: 'Disabled', tone: 'danger' }
}

export function accountBadges(account: DashboardServiceAccount): { label: string; tone: string }[] {
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

export function accountStatusKeys(account: DashboardServiceAccount): string[] {
  const now = Date.now()
  const expiresAt = account.expires_at ? Date.parse(account.expires_at) : Number.NaN
  const rotationDueAt = account.rotation_due_at ? Date.parse(account.rotation_due_at) : Number.NaN
  const keys = [account.active ? 'active' : 'disabled']
  if (!Number.isNaN(expiresAt) && expiresAt <= now) keys.push('expired')
  if (!Number.isNaN(rotationDueAt) && rotationDueAt <= now) keys.push('rotation_due')
  if (!account.last_used_at) keys.push('never_used')
  return keys
}

export function bindingLabel(binding: DashboardUserRoleBinding, projects: Project[], domains: Domain[], applicationRepository?: ApplicationRepository | null): string {
  if (binding.scope_type === 'global') return 'Global'
  if (binding.scope_type === 'project') {
    return projects.find(project => project.id === binding.scope_id)?.name || binding.scope_id || 'Project'
  }
  if (binding.scope_type === 'domain') {
    return domains.find(domain => domain.id === binding.scope_id)?.name || binding.scope_id || 'Domain'
  }
  if (binding.scope_type === 'application_repository') {
    return applicationRepository && applicationRepository.id === binding.scope_id ? applicationRepository.name : binding.scope_id || 'Repository'
  }
  return binding.scope_id || 'Repository'
}

export function userRoleList(user: DashboardUser): string[] {
  return uniqueValues((user.bindings || []).map(binding => binding.role))
}

export function userAccessLabels(user: DashboardUser, projects: Project[], domains: Domain[], applicationRepository?: ApplicationRepository | null): string[] {
  return (user.bindings || []).map(binding => {
    const scope = SCOPE_LABELS[binding.scope_type] || binding.scope_type
    const label = bindingLabel(binding, projects, domains, applicationRepository)
    return `${scope}: ${label} (${ROLE_LABELS[binding.role] || binding.role})`
  })
}

export function projectDomainID(projects: Project[], projectID?: string): string {
  return projects.find(project => project.id === projectID)?.domain_id || ''
}

export function userMatchesDomain(user: DashboardUser, domainID: string, projects: Project[]): boolean {
  if (!domainID) return true
  return (user.bindings || []).some(binding => {
    if (binding.scope_type === 'domain') return binding.scope_id === domainID
    if (binding.scope_type === 'project') return projectDomainID(projects, binding.scope_id) === domainID
    return false
  })
}

export function userMatchesProject(user: DashboardUser, projectID: string): boolean {
  if (!projectID) return true
  return (user.bindings || []).some(binding => binding.scope_type === 'project' && binding.scope_id === projectID)
}

export function accountRoleList(account: DashboardServiceAccount): string[] {
  const scopedRoles = [
    ...Object.values(account.project_roles || {}).flat(),
    ...Object.values(account.domain_roles || {}).flat(),
    ...Object.values(account.application_repository_roles || {}).flat(),
  ]
  return uniqueValues([...(account.global_roles || []), ...scopedRoles])
}

export function formatScopeRoleEntries(entries: Record<string, string[]> | undefined, labelForID: (id: string) => string, scopeLabel: string): string[] {
  return Object.entries(entries || {}).map(([id, roles]) => `${scopeLabel}: ${labelForID(id)} (${formatRoleList(roles)})`)
}

export function accountAccessLabels(account: DashboardServiceAccount, projects: Project[], domains: Domain[]): string[] {
  return [
    ...formatScopeRoleEntries(account.domain_roles, id => domains.find(domain => domain.id === id)?.name || id, 'Domain'),
    ...formatScopeRoleEntries(account.project_roles, id => projects.find(project => project.id === id)?.name || id, 'Project'),
    ...formatScopeRoleEntries(account.application_repository_roles, id => id, 'Repository'),
  ]
}

export function accountMatchesDomain(account: DashboardServiceAccount, domainID: string, projects: Project[]): boolean {
  if (!domainID) return true
  if (Object.keys(account.domain_roles || {}).includes(domainID)) return true
  return Object.keys(account.project_roles || {}).some(projectID => projectDomainID(projects, projectID) === domainID)
}

export function accountMatchesProject(account: DashboardServiceAccount, projectID: string): boolean {
  if (!projectID) return true
  return Object.keys(account.project_roles || {}).includes(projectID)
}

export function bucketLabel(bucket: UsageAccountingBucket): string {
  return bucket.key || 'unattributed'
}

export function formatNumber(value?: number): string {
  return (value || 0).toLocaleString()
}

export function usageSummary(bucket?: UsageAccountingBucket): string {
  if (!bucket) return 'Unavailable'
  return `${formatNumber(bucket.run_count)} runs · ${formatNumber(bucket.vuser_seconds)} VUser seconds`
}

export function usageForUser(user: DashboardUser, buckets: UsageAccountingBucket[]): UsageAccountingBucket | undefined {
  const keys = [user.subject, user.email, user.display_name, user.id].map(normalize).filter(Boolean)
  return buckets.find(bucket => keys.includes(normalize(bucket.key)))
}

export function eventScopeLabel(event: DashboardAuditEvent, projects: Project[], domains: Domain[]): string {
  const parts: string[] = []
  if (event.domain_id) {
    parts.push(domains.find(domain => domain.id === event.domain_id)?.name || event.domain_id)
  }
  if (event.project_id) {
    parts.push(projects.find(project => project.id === event.project_id)?.name || event.project_id)
  }
  return parts.join(' / ') || 'Global'
}

export function eventResourceLabel(event: DashboardAuditEvent): string {
  return `${event.resource_type}${event.resource_id ? `/${event.resource_id}` : ''}`
}

export function detailsSummary(details?: Record<string, unknown>): string {
  const entries = Object.entries(details || {})
  if (entries.length === 0) return 'None'
  return entries.slice(0, 3).map(([key, value]) => `${key}: ${String(value)}`).join(' · ')
}
