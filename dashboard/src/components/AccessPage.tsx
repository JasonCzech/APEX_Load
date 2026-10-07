import './Access.css'
import { settingsHref } from './settingsNavigation'
import { useAccessWorkspace } from './useAccessWorkspace'
import AccessNavigation from './AccessNavigation'
import { csvBlob, reviewPriority, activityDateError, activitySnapshotBefore } from './accessReporting'
import AccessUsagePanel from './AccessUsagePanel'
import FormField from './ux/FormField'
import TabList from './ux/TabList'
import { confirmAction } from './ux/confirmAction'
import PanelToggle from "./ux/PanelToggle"
import { handleTabKey } from './tabKeyboard'
import { AuditEventTable, LoadOlderEvents } from './AuditEventTable'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { Dispatch, SetStateAction } from 'react'
import { useApi } from '../hooks/useApi'
import {
  DashboardAccessReviewResponse,
  DashboardAuthEffectiveAccess,
  DashboardAuditEvent,
  DashboardServiceAccount,
  DashboardUser,
  DashboardUserRoleBinding,
  DashboardUserRoleScopeType,
  UsageAccountingBucket,
} from '../types'
import { downloadBlob } from '../utils/fileUtils'
import { getErrorMessage } from '../utils/errorUtils'
import { useHasTopbarContributionProvider, useTopbarContribution } from '../hooks/TopbarContributionProvider'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { AccessIssuedKeyModal } from './AccessIssuedKeyModal'
import { SortHeader } from './SortHeader'
import type {
  UserSortField, AccountSortField,
  ActivitySortField, SortState, AccessPageProps, PurposePreset, ServiceAccountForm,
  UserForm, UserBindingForm, UserFilterState, AccountFilterState, ActivityFilterForm, IssuedKeyState,
} from './accessTypes'
import {
  PURPOSE_PRESETS, GLOBAL_ROLE_OPTIONS, SCOPED_ROLE_OPTIONS, SCOPE_OPTIONS, ROLE_LABELS, SCOPE_LABELS,
  userSortDefaults, accountSortDefaults, activitySortDefaults, preferredTab, presetByID,
  buildPresetForm, formFromServiceAccount, buildActivityFilter, toggleValue, toProjectRolesPayload,
  mergeScopedRolesPayload, parseOptionalDateTime, parseRotationDays, timestampValue, formatTimestamp,
  formatRoleList, roleOptionsForScope, uniqueValues, includesQuery, comparePrimitive,
  stableSort, nextSort, defaultScopeID, createUserBindingDraft, initialUserBindingDrafts,
  userBindingDraftFromBinding, bindingSignature, draftSignature, normalizedUserBindingDrafts, userStatusBadge,
  accountBadges, accountStatusKeys, bindingLabel, userRoleList, userAccessLabels, userMatchesDomain,
  userMatchesProject, accountRoleList, accountAccessLabels, accountMatchesDomain, accountMatchesProject,
  usageSummary, usageForUser, eventScopeLabel, eventResourceLabel, detailsSummary, appendNewEvents,
} from './accessUtils'

const USER_PAGE_SIZE_OPTIONS = [25, 50, 100]

type AuthoritativeEntity<T> = {
  entity: T
  listScope?: string | null
  isSatisfiedBy?: (serverEntity: T, authoritativeEntity: T) => boolean
}

function canonicalize(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value
      .map(canonicalize)
      .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)))
  }
  if (value && typeof value === 'object') {
    return Object.keys(value as Record<string, unknown>)
      .sort()
      .reduce<Record<string, unknown>>((result, key) => {
        result[key] = canonicalize((value as Record<string, unknown>)[key])
        return result
      }, {})
  }
  return value
}

function dataSignature(value: unknown): string {
  return JSON.stringify(canonicalize(value))
}

function serverHasAuthoritativeEntity<T>(serverEntity: T, authoritative: AuthoritativeEntity<T>): boolean {
  const authoritativeEntity = authoritative.entity
  if (dataSignature(serverEntity) === dataSignature(authoritativeEntity)) return true
  return authoritative.isSatisfiedBy?.(serverEntity, authoritativeEntity) || false
}

function reconcileAuthoritativeEntities<T extends { id: string; updated_at: string }>(
  serverEntities: T[],
  authoritativeEntities: Map<string, AuthoritativeEntity<T>>,
  deletedEntityIDs: Set<string>,
  listScope?: string,
): { entities: T[]; deletedFromResponse: number; insertedAuthoritative: number } {
  const seen = new Set<string>()
  let deletedFromResponse = 0
  const entities = serverEntities.flatMap(serverEntity => {
    if (deletedEntityIDs.has(serverEntity.id)) {
      deletedFromResponse += 1
      return []
    }
    seen.add(serverEntity.id)
    const authoritative = authoritativeEntities.get(serverEntity.id)
    if (!authoritative) return [serverEntity]
    if (serverHasAuthoritativeEntity(serverEntity, authoritative)) {
      authoritativeEntities.delete(serverEntity.id)
      return [serverEntity]
    }
    return [authoritative.entity]
  })

  let insertedAuthoritative = 0
  authoritativeEntities.forEach(authoritative => {
    if (deletedEntityIDs.has(authoritative.entity.id) || seen.has(authoritative.entity.id)) return
    if (authoritative.listScope === null) return
    if (authoritative.listScope !== undefined && authoritative.listScope !== listScope) return
    entities.unshift(authoritative.entity)
    seen.add(authoritative.entity.id)
    insertedAuthoritative += 1
  })

  return { entities, deletedFromResponse, insertedAuthoritative }
}

function userDraftSignature(form: UserForm, bindings: UserBindingForm[]): string {
  return dataSignature({
    form,
    bindings: normalizedUserBindingDrafts(bindings).map(draftSignature),
  })
}

function serviceAccountDraftSignature(form: ServiceAccountForm): string {
  return dataSignature(form)
}

function userMutationStateSignature(user: DashboardUser): string {
  return dataSignature({
    subject: user.subject,
    email: user.email || '',
    displayName: user.display_name || '',
    active: user.active,
    bindings: (user.bindings || []).map(binding => bindingSignature(binding)),
  })
}

function accountMutationStateSignature(account: DashboardServiceAccount): string {
  return dataSignature({
    name: account.name,
    description: account.description || '',
    ownerID: account.owner_id || '',
    contactEmail: account.contact_email || '',
    active: account.active,
    globalRoles: account.global_roles || [],
    projectRoles: account.project_roles || {},
    domainRoles: account.domain_roles || {},
    applicationRepositoryRoles: account.application_repository_roles || {},
    scopes: account.scopes || [],
    expiresAt: account.expires_at || '',
    keyRotatedAt: account.key_rotated_at || '',
    rotationIntervalDays: account.rotation_interval_days || 0,
    rotationDueAt: account.rotation_due_at || '',
  })
}

function isValidOptionalEmail(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed) return true
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(trimmed)
}

async function confirmBroadAutomationScope(scopes: string[], subject: string): Promise<boolean> {
  if (!scopes.includes('automation:*')) return true
  if (typeof window === 'undefined') return true
  return (await confirmAction(`${subject} includes automation:*, which allows any service-account token scope after RBAC also permits the route. Continue?`))
}

function additionalScopedRoleEntries(
  account: DashboardServiceAccount,
  projects: { id: string; name: string }[],
  domains: { id: string; name: string }[],
  applicationRepository?: { id: string; name: string } | null,
): Array<{ key: string; label: string; roles: string[] }> {
  const entries: Array<{ key: string; label: string; roles: string[] }> = []
  const collect = (scopeLabel: string, rolesByID: Record<string, string[]> | undefined, labelForID: (id: string) => string) => {
    Object.entries(rolesByID || {}).forEach(([id, roles]) => {
      const additionalRoles = (roles || []).slice(1)
      if (additionalRoles.length > 0) {
        entries.push({ key: `${scopeLabel}:${id}`, label: `${scopeLabel}: ${labelForID(id)}`, roles: additionalRoles })
      }
    })
  }
  collect('Domain', account.domain_roles, id => domains.find(domain => domain.id === id)?.name || id)
  collect('Project', account.project_roles, id => projects.find(project => project.id === id)?.name || id)
  collect('Repository', account.application_repository_roles, id => (applicationRepository?.id === id ? applicationRepository.name : id))
  return entries
}

export default function AccessPage({
  accessView,
  onViewChange,
  authMode,
  projects,
  domains = [],
  applicationRepository,
  activeDomain,
  activeProject,
  canManageUserLifecycle,
  canManageUserBindings,
  canViewUserActivity,
  canManageServiceAccounts,
  accessReviewRefreshSignal = 0,
}: AccessPageProps) {
  const api = useApi()
  const settingsHrefForAuth = settingsHref('authentication')
  const { runGuarded } = useNavigationGuardRunner()
  const canUseUsers = canManageUserLifecycle || canManageUserBindings
  const { view: currentAccessView, setView: setAccessView, activeTab, userSubTab, serviceAccountSubTab, activitySubTab, setActiveTab, setUserSubTab, setServiceAccountSubTab, setActivitySubTab } = useAccessWorkspace(preferredTab(canUseUsers, canManageServiceAccounts, canViewUserActivity), accessView, onViewChange)
  const [accounts, setAccounts] = useState<DashboardServiceAccount[]>([])
  const [users, setUsers] = useState<DashboardUser[]>([])
  const [usersTotal, setUsersTotal] = useState(0)
  const [userPage, setUserPage] = useState(0)
  const [userPageSize, setUserPageSize] = useState(50)
  const [selectedUserID, setSelectedUserID] = useState('')
  const [selectedAccountID, setSelectedAccountID] = useState('')
  const [selectedActivityID, setSelectedActivityID] = useState('')
  const [userInspectorCollapsed, setUserInspectorCollapsed] = useState(false)
  const userInspectorHeadingRef = useRef<HTMLHeadingElement>(null)
  const [userInspectorFocusRequest, setUserInspectorFocusRequest] = useState(0)
  useEffect(() => {
    if (userInspectorFocusRequest && !userInspectorCollapsed) {
      userInspectorHeadingRef.current?.focus({ preventScroll: true })
      userInspectorHeadingRef.current?.scrollIntoView?.({ block: 'nearest' })
    }
  }, [userInspectorFocusRequest, userInspectorCollapsed])

  const [accountInspectorCollapsed, setAccountInspectorCollapsed] = useState(false)
  const [activityInspectorCollapsed, setActivityInspectorCollapsed] = useState(false)
  const [userFilters, setUserFilters] = useState<UserFilterState>({ query: '', status: '', role: '', domainID: '', projectID: '' })
  const [accountFilters, setAccountFilters] = useState<AccountFilterState>({ query: '', status: '', role: '', scope: '', domainID: '', projectID: '' })
  const [showUserMetadata, setShowUserMetadata] = useState(false)
  const [showAccountMetadata, setShowAccountMetadata] = useState(false)
  const [activitySearch, setActivitySearch] = useState('')

  const [userSort, setUserSort] = useState<SortState<UserSortField>>({ field: 'user', direction: 'asc' })
  const [accountSort, setAccountSort] = useState<SortState<AccountSortField>>({ field: 'updated', direction: 'desc' })
  const [activitySort, setActivitySort] = useState<SortState<ActivitySortField>>({ field: 'time', direction: 'desc' })

  const [userForm, setUserForm] = useState<UserForm>({ subject: '', email: '', displayName: '' })
  const [editUserForm, setEditUserForm] = useState<UserForm>({ subject: '', email: '', displayName: '' })
  const [onboardBindingDrafts, setOnboardBindingDrafts] = useState<UserBindingForm[]>(() => initialUserBindingDrafts(projects, domains, activeProject, activeDomain, applicationRepository))
  // Compare against the preset actually shown, not a new preset derived from later catalog hydration.
  const onboardInitialDraftSignature = useRef(userDraftSignature(userForm, onboardBindingDrafts))
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
  const [appliedActivityFilter, setAppliedActivityFilter] = useState(activityFilter)
  const [activityHasMore, setActivityHasMore] = useState(false)
  const [activityOlderLoading, setActivityOlderLoading] = useState(false)
  const [activityOlderError, setActivityOlderError] = useState('')
  const [activitySnapshot, setActivitySnapshot] = useState(() => new Date().toISOString())
  const activityValidation = activityDateError(activityFilter.createdAfter, activityFilter.createdBefore)
  const activityWorkspaceRef = useRef(`${activeDomain?.id || ''}:${activeProject?.id || ''}`)
  useEffect(() => {
    const workspace = `${activeDomain?.id || ''}:${activeProject?.id || ''}`
    if (activityWorkspaceRef.current === workspace) return
    activityWorkspaceRef.current = workspace
    const next = { actorSubject: '', domainID: activeDomain?.id || '', projectID: activeProject?.id || '', action: '', status: '', createdAfter: '', createdBefore: '' }
    setActivityFilter(next); setAppliedActivityFilter(next); setActivitySnapshot(new Date().toISOString())
  }, [activeDomain?.id, activeProject?.id])
  const [activityEvents, setActivityEvents] = useState<DashboardAuditEvent[]>([])
  const [activityUsage, setActivityUsage] = useState<Awaited<ReturnType<typeof api.getAuthUserActivity>>['usage'] | null>(null)
  const [accessReview, setAccessReview] = useState<DashboardAccessReviewResponse | null>(null)
  const [accessReviewLoading, setAccessReviewLoading] = useState(false)
  const [accessReviewError, setAccessReviewError] = useState('')
  const [form, setForm] = useState<ServiceAccountForm>(() => buildPresetForm(presetByID('ci'), activeProject?.id))
  const [loading, setLoading] = useState(true)
  const [usersLoading, setUsersLoading] = useState(true)
  const [activityLoading, setActivityLoading] = useState(true)
  const [accountsLoadFailed, setAccountsLoadFailed] = useState(false)
  const [usersLoadFailed, setUsersLoadFailed] = useState(false)
  const [activityLoadFailed, setActivityLoadFailed] = useState(false)
  const [activityExporting, setActivityExporting] = useState(false)
  const [actionError, setActionError] = useState('')
  const [actionMessage, setActionMessage] = useState('')
  const [userActionError, setUserActionError] = useState('')
  const [userActionMessage, setUserActionMessage] = useState('')
  const [activityError, setActivityError] = useState('')
  const [activityMessage, setActivityMessage] = useState('')
  const [issuedKey, setIssuedKey] = useState<IssuedKeyState | null>(null)
  const [copyMessage, setCopyMessage] = useState('')
  const [accountEditMode, setAccountEditMode] = useState(false)
  const [editAccountForm, setEditAccountForm] = useState<ServiceAccountForm>(() => buildPresetForm(presetByID('ci')))
  const [simulatorPrincipal, setSimulatorPrincipal] = useState<{ type: 'user' | 'service_account'; id: string }>({ type: 'user', id: '' })
  const [simulatorUsers, setSimulatorUsers] = useState<DashboardUser[]>([])
  const [simulatorQuery, setSimulatorQuery] = useState('')
  const [simulatorPage, setSimulatorPage] = useState(0)
  const [simulatorHasMore, setSimulatorHasMore] = useState(false)
  const [simulatorDirectoryLoading, setSimulatorDirectoryLoading] = useState(false)
  const [simulatorDirectoryError, setSimulatorDirectoryError] = useState('')
  const [simulatorDirectoryRevision, setSimulatorDirectoryRevision] = useState(0)
  const [simulatorResult, setSimulatorResult] = useState<DashboardAuthEffectiveAccess | null>(null)
  const [simulatorError, setSimulatorError] = useState('')
  const [simulatorLoading, setSimulatorLoading] = useState(false)
  const [accessMutationBusy, setAccessMutationBusy] = useState(false)
  const accountsLoadGenerationRef = useRef(0)
  const usersLoadGenerationRef = useRef(0)
  const activityLoadGenerationRef = useRef(0)
  const accessReviewLoadGenerationRef = useRef(0)
  const simulatorGenerationRef = useRef(0)
  const accessMutationInFlightRef = useRef(false)
  const authoritativeAccountsRef = useRef(new Map<string, AuthoritativeEntity<DashboardServiceAccount>>())
  const deletedAccountIDsRef = useRef(new Set<string>())
  const authoritativeUsersRef = useRef(new Map<string, AuthoritativeEntity<DashboardUser>>())
  const deletedUserIDsRef = useRef(new Set<string>())
  const editingUserIDRef = useRef('')
  const editingUserSnapshotRef = useRef<DashboardUser | null>(null)
  const hasTopbarContributionProvider = useHasTopbarContributionProvider()

  const selectedUser = useMemo(() => users.find(user => user.id === selectedUserID) || null, [selectedUserID, users])
  const selectedAccount = useMemo(() => accounts.find(account => account.id === selectedAccountID) || null, [accounts, selectedAccountID])
  const selectedActivity = useMemo(() => activityEvents.find(event => event.id === selectedActivityID) || null, [activityEvents, selectedActivityID])
  const userBuckets = useMemo(() => activityUsage?.by_user || [], [activityUsage?.by_user])
  const accessWorkspaceAvailable = canUseUsers || canManageServiceAccounts || canViewUserActivity
  const userPageCount = Math.max(1, Math.ceil(usersTotal / userPageSize))
  const userRangeStart = usersTotal === 0 ? 0 : userPage * userPageSize + 1
  const userRangeEnd = Math.min(userPage * userPageSize + users.length, usersTotal)
  const onboardEmailInvalid = !isValidOptionalEmail(userForm.email)
  const editUserEmailInvalid = !isValidOptionalEmail(editUserForm.email)
  const accountContactInvalid = !isValidOptionalEmail(form.contact)
  const editAccountContactInvalid = !isValidOptionalEmail(editAccountForm.contact)
  const simulatorScopeKey = [
    simulatorPrincipal.type,
    simulatorPrincipal.id,
    activeProject?.id || '',
    activeProject?.domain_id || activeDomain?.id || '',
    applicationRepository?.id || '',
  ].join('\u0000')
  const simulatorScopeKeyRef = useRef(simulatorScopeKey)
  simulatorScopeKeyRef.current = simulatorScopeKey
  const userListScopeKey = dataSignature({
    page: userPage,
    pageSize: userPageSize,
    domainID: userFilters.domainID,
    projectID: userFilters.projectID,
    query: userFilters.query.trim(),
    role: userFilters.role,
    status: userFilters.status,
  })
  const currentUserListScopeRef = useRef(userListScopeKey)
  currentUserListScopeRef.current = userListScopeKey
  const currentUserFilterContextRef = useRef({
    applicationRepository,
    domains,
    filters: userFilters,
    projects,
  })
  currentUserFilterContextRef.current = {
    applicationRepository,
    domains,
    filters: userFilters,
    projects,
  }
  // Attention summary for the access-review badge: flagged principals + recent denials.
  const accessReviewAttention = useMemo(() => {
    if (!accessReview) return null
    const accountsFlagged = accessReview.service_accounts.length
    const usersFlagged = accessReview.users.length
    const denials = accessReview.recent_denials || 0
    const total = accountsFlagged + usersFlagged + denials
    return total > 0 ? { accountsFlagged, usersFlagged, denials, total } : null
  }, [accessReview])
  const simulatorPrincipalOptions = useMemo(() =>
    simulatorPrincipal.type === 'user'
      ? simulatorUsers.map(user => ({ id: user.id, label: user.display_name || user.subject }))
      : accounts.map(account => ({ id: account.id, label: account.name })),
  [simulatorPrincipal.type, simulatorUsers, accounts])

  useEffect(() => {
    if (activeTab !== 'activity' || activitySubTab !== 'review' || simulatorPrincipal.type !== 'user' || !canManageUserLifecycle) return
    let cancelled = false
    setSimulatorDirectoryLoading(true); setSimulatorDirectoryError(''); setSimulatorUsers([])
    void api.listAuthUsers({ q: simulatorQuery.trim() || undefined, limit: 50, offset: simulatorPage * 50 }).then(response => {
      if (cancelled) return
      setSimulatorUsers(response.users || [])
      setSimulatorHasMore(response.has_more ?? (response.users || []).length === 50)
    }).catch(error => { if (!cancelled) setSimulatorDirectoryError(getErrorMessage(error, 'Unable to search users')) }).finally(() => { if (!cancelled) setSimulatorDirectoryLoading(false) })
    return () => { cancelled = true }
  }, [activeTab, activitySubTab, simulatorPrincipal.type, canManageUserLifecycle, api, simulatorQuery, simulatorPage, simulatorDirectoryRevision])

  const accessTopbarContribution = useMemo(() => ({
    id: 'access-page',
    title: 'Access',
    priority: 10,
  }), [])

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
      const accessLabels = userAccessLabels(user, projects, domains, applicationRepository)
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
          case 'access': return userAccessLabels(user, projects, domains, applicationRepository).join(' ')
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
  }, [applicationRepository, domains, projects, userBuckets, userFilters, userSort, users])

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


  const loadAccounts = useCallback(async () => {
    const requestGeneration = ++accountsLoadGenerationRef.current
    const isCurrentRequest = () => accountsLoadGenerationRef.current === requestGeneration
    if (!canManageServiceAccounts) {
      if (isCurrentRequest()) {
        setAccounts([])
        setAccountsLoadFailed(false)
        setLoading(false)
      }
      return
    }
    setLoading(true)
    try {
      const response = await api.listAuthServiceAccounts()
      if (!isCurrentRequest()) return
      const reconciled = reconcileAuthoritativeEntities(
        response.service_accounts || [],
        authoritativeAccountsRef.current,
        deletedAccountIDsRef.current,
      )
      setAccounts(reconciled.entities)
      setActionError('')
      setAccountsLoadFailed(false)
    } catch (error: unknown) {
      if (!isCurrentRequest()) return
      setActionError(getErrorMessage(error, 'Unable to load service accounts'))
      setAccountsLoadFailed(true)
    } finally {
      if (isCurrentRequest()) setLoading(false)
    }
  }, [api, canManageServiceAccounts])

  const loadUsers = useCallback(async () => {
    const requestGeneration = ++usersLoadGenerationRef.current
    const isCurrentRequest = () => usersLoadGenerationRef.current === requestGeneration
    if (!canUseUsers) {
      if (isCurrentRequest()) {
        setUsers([])
        setUsersTotal(0)
        setUsersLoadFailed(false)
        setUsersLoading(false)
      }
      return
    }
    setUsersLoading(true)
    try {
      const offset = userPage * userPageSize
      const requestFilter: { q?: string; active?: string; role?: string; domain_id?: string; project_id?: string; limit: number; offset: number } = { limit: userPageSize, offset }
      const query = userFilters.query.trim()
      if (query) requestFilter.q = query
      if (userFilters.status === 'active') requestFilter.active = 'true'
      if (userFilters.status === 'disabled') requestFilter.active = 'false'
      if (userFilters.role) requestFilter.role = userFilters.role
      if (userFilters.domainID) requestFilter.domain_id = userFilters.domainID
      if (userFilters.projectID) requestFilter.project_id = userFilters.projectID
      const response = await api.listAuthUsers(requestFilter)
      if (!isCurrentRequest()) return
      const reconciled = reconcileAuthoritativeEntities(
        response.users || [],
        authoritativeUsersRef.current,
        deletedUserIDsRef.current,
        userListScopeKey,
      )
      const usersForDisplay = [...reconciled.entities]
      const editingUser = editingUserSnapshotRef.current
      if (editingUser) {
        const loadedEditingUser = usersForDisplay.find(user => user.id === editingUser.id)
        if (loadedEditingUser) {
          editingUserSnapshotRef.current = loadedEditingUser
        } else if (!deletedUserIDsRef.current.has(editingUser.id)) {
          usersForDisplay.push(editingUser)
        }
      }
      setUsers(usersForDisplay)
      const serverTotal = typeof response.total === 'number' ? response.total : (response.users || []).length
      const visibleRangeFloor = reconciled.entities.length > 0 ? offset + reconciled.entities.length : 0
      const total = Math.max(
        0,
        serverTotal - reconciled.deletedFromResponse,
        visibleRangeFloor,
      )
      setUsersTotal(total)
      if (offset >= total && total > 0) {
        setUserPage(Math.max(0, Math.ceil(total / userPageSize) - 1))
      }
      setUserActionError('')
      setUsersLoadFailed(false)
    } catch (error: unknown) {
      if (!isCurrentRequest()) return
      setUserActionError(getErrorMessage(error, 'Unable to load users'))
      setUsersLoadFailed(true)
    } finally {
      if (isCurrentRequest()) setUsersLoading(false)
    }
  }, [api, canUseUsers, userFilters.domainID, userFilters.projectID, userFilters.query, userFilters.role, userFilters.status, userListScopeKey, userPage, userPageSize])
  const currentLoadUsersRef = useRef(loadUsers)
  currentLoadUsersRef.current = loadUsers
  const loadCurrentUsers = useCallback(() => currentLoadUsersRef.current(), [])

  useEffect(() => {
    setUserPage(0)
  }, [userFilters.domainID, userFilters.projectID, userFilters.query, userFilters.role, userFilters.status])

  const loadActivity = useCallback(async () => {
    const requestGeneration = ++activityLoadGenerationRef.current
    const isCurrentRequest = () => activityLoadGenerationRef.current === requestGeneration
    if (!canViewUserActivity) {
      if (isCurrentRequest()) {
        setActivityEvents([])
        setActivityUsage(null)
        setActivityLoadFailed(false)
        setActivityLoading(false)
      }
      return
    }
    setActivityLoading(true)
    // A new first batch supersedes any older-events load still in flight.
    setActivityOlderLoading(false)
    setActivityOlderError('')
    try {
      const response = await api.getAuthUserActivity({ ...buildActivityFilter(appliedActivityFilter), created_before: activitySnapshotBefore(appliedActivityFilter.createdBefore, activitySnapshot), offset: 0 })
      if (!isCurrentRequest()) return
      setActivityEvents(response.events || [])
      setActivityHasMore(response.has_more ?? (response.events || []).length === 50)
      setActivityUsage(response.usage)
      setActivityError('')
      setActivityLoadFailed(false)
    } catch (error: unknown) {
      if (!isCurrentRequest()) return
      setActivityError(getErrorMessage(error, 'Unable to load activity'))
      setActivityLoadFailed(true)
    } finally {
      if (isCurrentRequest()) setActivityLoading(false)
    }
  }, [appliedActivityFilter, activitySnapshot, api, canViewUserActivity])

  // Older events append to the loaded rows. The next offset is the number of
  // rows already loaded, so a failed load retries the same batch.
  const loadOlderActivity = useCallback(async () => {
    const requestGeneration = activityLoadGenerationRef.current
    const isCurrentRequest = () => activityLoadGenerationRef.current === requestGeneration
    setActivityOlderLoading(true)
    setActivityOlderError('')
    try {
      const response = await api.getAuthUserActivity({ ...buildActivityFilter(appliedActivityFilter), created_before: activitySnapshotBefore(appliedActivityFilter.createdBefore, activitySnapshot), offset: activityEvents.length })
      if (!isCurrentRequest()) return
      const older = response.events || []
      setActivityEvents(current => appendNewEvents(current, older))
      setActivityHasMore(response.has_more ?? older.length === 50)
    } catch (error: unknown) {
      if (isCurrentRequest()) setActivityOlderError(getErrorMessage(error, 'Unable to load older events'))
    } finally {
      if (isCurrentRequest()) setActivityOlderLoading(false)
    }
  }, [appliedActivityFilter, activityEvents.length, activitySnapshot, api])

  const loadAccessReview = useCallback(async () => {
    const requestGeneration = ++accessReviewLoadGenerationRef.current
    const isCurrentRequest = () => accessReviewLoadGenerationRef.current === requestGeneration
    // The report endpoint is gated by user-lifecycle admin.
    if (!canManageUserLifecycle) {
      if (isCurrentRequest()) {
        setAccessReview(null)
        setAccessReviewError('')
        setAccessReviewLoading(false)
      }
      return
    }
    setAccessReviewLoading(true)
    try {
      const response = await api.getAccessReview()
      if (!isCurrentRequest()) return
      setAccessReview(response)
      setAccessReviewError('')
    } catch (error: unknown) {
      if (!isCurrentRequest()) return
      setAccessReviewError(getErrorMessage(error, 'Unable to load access review'))
      setAccessReview(null)
    } finally {
      if (isCurrentRequest()) setAccessReviewLoading(false)
    }
  }, [api, canManageUserLifecycle])

  useEffect(() => {
    if (canManageUserLifecycle) return
    accessReviewLoadGenerationRef.current += 1
    setAccessReview(null)
    setAccessReviewError('')
    setAccessReviewLoading(false)
  }, [canManageUserLifecycle])

  useEffect(() => () => {
    accessReviewLoadGenerationRef.current += 1
  }, [])

  useEffect(() => {
    if (activeTab === 'activity' && activitySubTab === 'review' && canManageUserLifecycle && !accessReview && !accessReviewError && !accessReviewLoading) {
      void loadAccessReview()
    }
  }, [activeTab, activitySubTab, accessReview, accessReviewError, accessReviewLoading, canManageUserLifecycle, loadAccessReview])

  useEffect(() => {
    if (!canManageUserLifecycle && activitySubTab === 'review') {
      setActivitySubTab('events')
    }
  }, [activitySubTab, canManageUserLifecycle, setActivitySubTab])

  // Admins get the access-review posture eagerly (for the attention badge) and refetched on
  // each `access_review_changed` ping, regardless of which tab is open.
  useEffect(() => {
    if (canManageUserLifecycle) {
      void loadAccessReview()
    }
  }, [canManageUserLifecycle, accessReviewRefreshSignal, loadAccessReview])

  useEffect(() => {
    setActiveTab(current => {
      if (current === 'users' && canUseUsers) return current
      if (current === 'service_accounts' && canManageServiceAccounts) return current
      if (current === 'activity' && canViewUserActivity) return current
      return preferredTab(canUseUsers, canManageServiceAccounts, canViewUserActivity)
    })
  }, [canManageServiceAccounts, canUseUsers, canViewUserActivity, setActiveTab])

  useEffect(() => {
    if (!canManageUserLifecycle && userSubTab === 'onboard') {
      setUserSubTab('existing')
    }
  }, [canManageUserLifecycle, userSubTab, setUserSubTab])

  useEffect(() => {
    void loadAccounts()
    return () => {
      accountsLoadGenerationRef.current += 1
    }
  }, [loadAccounts])

  useEffect(() => {
    void loadUsers()
    return () => {
      usersLoadGenerationRef.current += 1
    }
  }, [loadUsers])

  useEffect(() => {
    void loadActivity()
    return () => {
      activityLoadGenerationRef.current += 1
    }
  }, [loadActivity])

  useEffect(() => {
    simulatorGenerationRef.current += 1
    setSimulatorResult(null)
    setSimulatorError('')
    setSimulatorLoading(false)
  }, [simulatorScopeKey])

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
    if (!selectedUserEditMode || !editingUserIDRef.current || editingUserIDRef.current === selectedUserID) return
    editingUserIDRef.current = ''
    editingUserSnapshotRef.current = null
    setSelectedUserEditMode(false)
  }, [selectedUserEditMode, selectedUserID])

  // Leave edit mode whenever the selected account changes, so a stale edit draft can't
  // be saved against a different account.
  useEffect(() => {
    setAccountEditMode(false)
  }, [selectedAccountID])

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
    if (selectedUserEditMode) return
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
  }, [selectedUser, selectedUserEditMode])

  useEffect(() => {
    setOnboardBindingDrafts(prev => prev.length > 0 ? prev : initialUserBindingDrafts(projects, domains, activeProject, activeDomain, applicationRepository))
  }, [activeDomain, activeProject, applicationRepository, domains, projects])

  const userMatchesCurrentServerFilters = (user: DashboardUser) => {
    const {
      applicationRepository: currentApplicationRepository,
      domains: currentDomains,
      filters,
      projects: currentProjects,
    } = currentUserFilterContextRef.current
    if (filters.status === 'active' && !user.active) return false
    if (filters.status === 'disabled' && user.active) return false
    const roles = userRoleList(user)
    if (filters.role && !roles.includes(filters.role)) return false
    if (!userMatchesDomain(user, filters.domainID, currentProjects)) return false
    if (!userMatchesProject(user, filters.projectID)) return false
    return includesQuery([
      user.subject,
      user.email,
      user.display_name,
      ...roles,
      ...userAccessLabels(user, currentProjects, currentDomains, currentApplicationRepository),
    ], filters.query)
  }

  const publishAuthoritativeUser = (user: DashboardUser, listScope = currentUserListScopeRef.current) => {
    if (!user?.id) return
    usersLoadGenerationRef.current += 1
    deletedUserIDsRef.current.delete(user.id)
    authoritativeUsersRef.current.set(user.id, {
      entity: user,
      listScope: userMatchesCurrentServerFilters(user) ? listScope : null,
      isSatisfiedBy: (serverUser, authoritativeUser) => userMutationStateSignature(serverUser) === userMutationStateSignature(authoritativeUser),
    })
    setUsers(prev => prev.some(existing => existing.id === user.id)
      ? prev.map(existing => existing.id === user.id ? user : existing)
      : [user, ...prev])
  }

  const publishDeletedUser = (userID: string) => {
    usersLoadGenerationRef.current += 1
    authoritativeUsersRef.current.delete(userID)
    deletedUserIDsRef.current.add(userID)
    setUsers(prev => prev.filter(user => user.id !== userID))
  }

  const publishAuthoritativeAccount = (account: DashboardServiceAccount) => {
    if (!account?.id) return
    accountsLoadGenerationRef.current += 1
    deletedAccountIDsRef.current.delete(account.id)
    authoritativeAccountsRef.current.set(account.id, {
      entity: account,
      isSatisfiedBy: (serverAccount, authoritativeAccount) => accountMutationStateSignature(serverAccount) === accountMutationStateSignature(authoritativeAccount),
    })
    setAccounts(prev => prev.some(existing => existing.id === account.id)
      ? prev.map(existing => existing.id === account.id ? account : existing)
      : [account, ...prev])
  }

  const publishDeletedAccount = (accountID: string) => {
    accountsLoadGenerationRef.current += 1
    authoritativeAccountsRef.current.delete(accountID)
    deletedAccountIDsRef.current.add(accountID)
    setAccounts(prev => prev.filter(account => account.id !== accountID))
  }

  const runAccessMutation = async (operation: () => Promise<void>): Promise<boolean> => {
    if (accessMutationInFlightRef.current) return false
    accessMutationInFlightRef.current = true
    setAccessMutationBusy(true)
    try {
      await operation()
      return true
    } finally {
      accessMutationInFlightRef.current = false
      setAccessMutationBusy(false)
    }
  }

  const applyPreset = (preset: PurposePreset) => {
    setForm(buildPresetForm(preset, activeProject?.id))
    setActionError('')
    setActionMessage('')
  }

  const createUser = async (): Promise<boolean> => {
    if (accessMutationInFlightRef.current) return false
    if (!canManageUserLifecycle) {
      setUserActionError('Current session cannot manage users.')
      setUserActionMessage('')
      return false
    }
    if (!userForm.subject.trim()) {
      setUserActionError('Subject is required.')
      setUserActionMessage('')
      return false
    }
    if (onboardEmailInvalid) {
      setUserActionError('Enter a valid email address before creating the user.')
      setUserActionMessage('')
      return false
    }
    const initialBindings = normalizedUserBindingDrafts(onboardBindingDrafts)
    if (initialBindings.length === 0) {
      setUserActionError('Choose at least one initial role binding.')
      setUserActionMessage('')
      return false
    }
    let succeeded = false
    await runAccessMutation(async () => {
      try {
        const user = await api.onboardAuthUser({
          subject: userForm.subject.trim(),
          email: userForm.email.trim() || undefined,
          display_name: userForm.displayName.trim() || undefined,
          bindings: initialBindings.map(binding => ({
            scope_type: binding.scopeType,
            scope_id: binding.scopeType === 'global' ? undefined : binding.scopeID,
            role: binding.role,
          })),
        })
        publishAuthoritativeUser(user)
        if (userMatchesCurrentServerFilters(user)) setUsersTotal(total => total + 1)
        setSelectedUserID(user.id)
        setUserInspectorCollapsed(false)
        setUserSubTab('existing')
        resetOnboardDrafts()
        setSelectedUserEditMode(false)
        setUserActionMessage(`Onboarded ${user.display_name || user.email || user.subject} with ${initialBindings.length} role ${initialBindings.length === 1 ? 'binding' : 'bindings'}.`)
        setUserActionError('')
        succeeded = true
        await loadCurrentUsers()
      } catch (error: unknown) {
        setUserActionError(getErrorMessage(error, 'Unable to create user'))
        setUserActionMessage('')
      }
    })
    return succeeded
  }

  const updateSelectedUser = async (): Promise<boolean> => {
    if (accessMutationInFlightRef.current) return false
    if (!selectedUser || !canManageUserBindings) return false
    if (canManageUserLifecycle && !editUserForm.subject.trim()) {
      setUserActionError('Subject is required.')
      setUserActionMessage('')
      return false
    }
    if (canManageUserLifecycle && editUserEmailInvalid) {
      setUserActionError('Enter a valid email address before saving the user.')
      setUserActionMessage('')
      return false
    }
    const desiredBindings = normalizedUserBindingDrafts(editBindingDrafts)
    if (desiredBindings.length === 0) {
      setUserActionError('Choose at least one role binding before saving access changes.')
      setUserActionMessage('')
      return false
    }
    let succeeded = false
    await runAccessMutation(async () => {
      let reconciledUser = selectedUser
      let reconciledBindings = [...(selectedUser.bindings || [])]
      let committedAnyChange = false
      const publishReconciledUser = () => {
        const snapshot: DashboardUser = { ...selectedUser, ...reconciledUser, bindings: reconciledBindings }
        publishAuthoritativeUser(snapshot)
        return snapshot
      }
      try {
        const desiredSignatures = new Set(desiredBindings.map(draftSignature))
        const existingSignatures = new Set(reconciledBindings.map(bindingSignature))
        // Add replacement grants before removing old ones. A failed grant must
        // never strand the user with zero access or a partially deleted role.
        for (const binding of desiredBindings) {
          const signature = draftSignature(binding)
          if (!existingSignatures.has(signature)) {
            const request = {
              scope_type: binding.scopeType,
              scope_id: binding.scopeType === 'global' ? undefined : binding.scopeID,
              role: binding.role,
            }
            const response = await api.createAuthUserBinding(selectedUser.id, request) as DashboardUserRoleBinding
            // Preserve the requested identity even when an older controller
            // returns an acknowledgement-only response.
            const created: DashboardUserRoleBinding = {
              ...response,
              user_id: response.user_id || selectedUser.id,
              scope_type: response.scope_type || binding.scopeType,
              scope_id: response.scope_id ?? (binding.scopeType === 'global' ? '' : binding.scopeID),
              role: response.role || binding.role,
            }
            reconciledBindings = [...reconciledBindings, created]
            existingSignatures.add(signature)
            committedAnyChange = true
          }
        }
        for (const binding of [...reconciledBindings]) {
          if (!desiredSignatures.has(bindingSignature(binding))) {
            await api.deleteAuthUserBinding(selectedUser.id, binding.id)
            reconciledBindings = reconciledBindings.filter(candidate => candidate.id !== binding.id)
            existingSignatures.delete(bindingSignature(binding))
            committedAnyChange = true
          }
        }
        // Commit profile edits only after access reconciliation succeeds, so a
        // binding failure cannot also leave an unrelated partial profile edit.
        if (canManageUserLifecycle) {
          reconciledUser = await api.updateAuthUser(selectedUser.id, {
            subject: editUserForm.subject.trim(),
            email: editUserForm.email.trim() || undefined,
            display_name: editUserForm.displayName.trim() || undefined,
          })
          committedAnyChange = true
        }
        const user = publishReconciledUser()
        setSelectedUserID(user.id)
        editingUserIDRef.current = ''
        editingUserSnapshotRef.current = null
        setSelectedUserEditMode(false)
        setUserActionMessage(`${canManageUserLifecycle ? 'Updated' : 'Updated access for'} ${user.display_name || user.email || user.subject}.`)
        setUserActionError('')
        succeeded = true
        await loadCurrentUsers()
      } catch (error: unknown) {
        const message = getErrorMessage(error, 'Unable to update user')
        if (committedAnyChange) {
          publishReconciledUser()
          editingUserIDRef.current = ''
          editingUserSnapshotRef.current = null
          setSelectedUserEditMode(false)
          await loadCurrentUsers()
          setUserActionError(`${message} Some changes were already applied; current access was reconciled before retrying.`)
        } else {
          setUserActionError(message)
        }
        setUserActionMessage('')
      }
    })
    return succeeded
  }

  const setUserActive = async (user: DashboardUser, active: boolean) => {
    if (!canManageUserLifecycle || accessMutationInFlightRef.current) return
    const action = active ? 'restore access for' : 'remove access from'
    if (typeof window !== 'undefined' && !(await confirmAction(`Really ${action} ${user.display_name || user.subject}?`))) {
      return
    }
    await runAccessMutation(async () => {
      try {
        const updated = await (active ? api.enableAuthUser(user.id) : api.disableAuthUser(user.id))
        publishAuthoritativeUser(updated)
        setUserActionMessage(`${active ? 'Restored access for' : 'Removed access from'} ${user.display_name || user.subject}.`)
        setUserActionError('')
        await loadCurrentUsers()
      } catch (error: unknown) {
        setUserActionError(getErrorMessage(error, `Unable to ${active ? 'restore' : 'remove'} user access`))
        setUserActionMessage('')
      }
    })
  }

  const revokeSessions = async (user: DashboardUser) => {
    if (!canManageUserLifecycle || accessMutationInFlightRef.current) return
    if (typeof window !== 'undefined' && !(await confirmAction(`Sign out all active sessions for ${user.display_name || user.subject}? Their account stays active but they must sign in again.`))) {
      return
    }
    await runAccessMutation(async () => {
      try {
        const updated = await api.revokeAuthUserSessions(user.id)
        publishAuthoritativeUser(updated)
        setUserActionMessage(`Revoked active sessions for ${user.display_name || user.subject}.`)
        setUserActionError('')
        await loadCurrentUsers()
      } catch (error: unknown) {
        setUserActionError(getErrorMessage(error, 'Unable to revoke user sessions'))
        setUserActionMessage('')
      }
    })
  }

  const deleteUser = async (user: DashboardUser) => {
    if (!canManageUserLifecycle || accessMutationInFlightRef.current) return
    if (typeof window !== 'undefined' && !(await confirmAction(`Permanently delete ${user.display_name || user.subject} and all of their role bindings? This cannot be undone. To preserve the audit trail, use Remove Access instead.`))) {
      return
    }
    await runAccessMutation(async () => {
      try {
        await api.deleteAuthUser(user.id)
        publishDeletedUser(user.id)
        if (userMatchesCurrentServerFilters(user)) setUsersTotal(total => Math.max(0, total - 1))
        setUserActionMessage(`Deleted ${user.display_name || user.subject}.`)
        setUserActionError('')
        setSelectedUserID('')
        await loadCurrentUsers()
      } catch (error: unknown) {
        setUserActionError(getErrorMessage(error, 'Unable to delete user'))
        setUserActionMessage('')
      }
    })
  }

  const createAccount = async (): Promise<boolean> => {
    if (accessMutationInFlightRef.current) return false
    if (!canManageServiceAccounts) {
      setActionError('Current session cannot manage service-account API keys.')
      setActionMessage('')
      return false
    }
    if (!form.name.trim()) {
      setActionError('Service account name is required.')
      setActionMessage('')
      return false
    }
    if (form.globalRoles.length === 0) {
      setActionError('Choose at least one global role.')
      setActionMessage('')
      return false
    }
    if (form.scopes.length === 0) {
      setActionError('Choose at least one token scope.')
      setActionMessage('')
      return false
    }
    if (accountContactInvalid) {
      setActionError('Enter a valid contact email before creating the service account.')
      setActionMessage('')
      return false
    }
    if (!(await confirmBroadAutomationScope(form.scopes, 'This service account'))) {
      return false
    }

    let succeeded = false
    await runAccessMutation(async () => {
      try {
        const response = await api.createAuthServiceAccount({
          name: form.name.trim(),
          description: form.description.trim() || undefined,
          owner_id: form.owner.trim() || undefined,
          contact_email: form.contact.trim() || undefined,
          global_roles: form.globalRoles,
          project_roles: toProjectRolesPayload(form.projectRoles),
          domain_roles: toProjectRolesPayload(form.domainRoles),
          application_repository_roles: toProjectRolesPayload(form.applicationRepositoryRoles),
          scopes: form.scopes,
          expires_at: parseOptionalDateTime(form.expiresAt),
          rotation_interval_days: parseRotationDays(form.rotationDays),
        })
        publishAuthoritativeAccount(response.service_account)
        setIssuedKey({ key: response.api_key, accountName: response.service_account.name, mode: 'created' })
        setSelectedAccountID(response.service_account.id)
        setAccountInspectorCollapsed(false)
        setServiceAccountSubTab('existing')
        setForm(buildPresetForm(presetByID('ci'), activeProject?.id))
        setActionMessage(`Created ${response.service_account.name}. Copy the issued key before closing the dialog.`)
        setActionError('')
        succeeded = true
        await loadAccounts()
      } catch (error: unknown) {
        setActionError(getErrorMessage(error, 'Unable to create service account'))
        setActionMessage('')
      }
    })
    return succeeded
  }

  const rotateAccount = async (account: DashboardServiceAccount) => {
    if (!canManageServiceAccounts || accessMutationInFlightRef.current) return
    if (typeof window !== 'undefined' && !(await confirmAction(`Rotate API key for ${account.name}? Existing automation must be updated with the new key.`))) {
      return
    }
    await runAccessMutation(async () => {
      try {
        const response = await api.rotateAuthServiceAccount(account.id)
        publishAuthoritativeAccount(response.service_account)
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
    })
  }

  const setAccountActive = async (account: DashboardServiceAccount, active: boolean) => {
    if (!canManageServiceAccounts || accessMutationInFlightRef.current) return
    const action = active ? 'enable' : 'disable'
    if (typeof window !== 'undefined' && !(await confirmAction(`Really ${action} ${account.name}?`))) {
      return
    }
    await runAccessMutation(async () => {
      try {
        const updated = await api.updateAuthServiceAccount(account.id, { active }) as DashboardServiceAccount
        publishAuthoritativeAccount(updated)
        setActionMessage(`${active ? 'Enabled' : 'Disabled'} ${account.name}.`)
        setActionError('')
        await loadAccounts()
      } catch (error: unknown) {
        setActionError(getErrorMessage(error, `Unable to ${action} service account`))
        setActionMessage('')
      }
    })
  }

  const deleteAccount = async (account: DashboardServiceAccount) => {
    if (!canManageServiceAccounts || accessMutationInFlightRef.current) return
    if (typeof window !== 'undefined' && !(await confirmAction(`Delete ${account.name}? This cannot be undone.`))) {
      return
    }
    await runAccessMutation(async () => {
      try {
        await api.deleteAuthServiceAccount(account.id)
        publishDeletedAccount(account.id)
        setActionMessage(`Deleted ${account.name}.`)
        setActionError('')
        await loadAccounts()
      } catch (error: unknown) {
        setActionError(getErrorMessage(error, 'Unable to delete service account'))
        setActionMessage('')
      }
    })
  }

  const beginEditAccount = (account: DashboardServiceAccount) => {
    setEditAccountForm(formFromServiceAccount(account))
    setAccountEditMode(true)
    setActionError('')
    setActionMessage('')
  }

  const cancelEditAccount = () => {
    setAccountEditMode(false)
    setActionError('')
  }

  const updateAccount = async (account: DashboardServiceAccount): Promise<boolean> => {
    if (!canManageServiceAccounts || accessMutationInFlightRef.current) return false
    if (!editAccountForm.name.trim()) {
      setActionError('Service account name is required.')
      return false
    }
    // The backend rejects empty role/scope sets on a PATCH (disable a key with the
    // Disable action instead of stripping its permissions).
    if (editAccountForm.globalRoles.length === 0) {
      setActionError('Keep at least one global role (use Disable to deactivate the key).')
      return false
    }
    if (editAccountForm.scopes.length === 0) {
      setActionError('Keep at least one token scope (use Disable to deactivate the key).')
      return false
    }
    if (editAccountContactInvalid) {
      setActionError('Enter a valid contact email before saving the service account.')
      return false
    }
    if (!(await confirmBroadAutomationScope(editAccountForm.scopes, account.name))) {
      return false
    }
    let succeeded = false
    await runAccessMutation(async () => {
      try {
        const updated = await api.updateAuthServiceAccount(account.id, {
          name: editAccountForm.name.trim(),
          description: editAccountForm.description.trim(),
          owner_id: editAccountForm.owner.trim(),
          contact_email: editAccountForm.contact.trim(),
          global_roles: editAccountForm.globalRoles,
          project_roles: mergeScopedRolesPayload(editAccountForm.projectRoles, account.project_roles),
          domain_roles: mergeScopedRolesPayload(editAccountForm.domainRoles, account.domain_roles),
          application_repository_roles: mergeScopedRolesPayload(editAccountForm.applicationRepositoryRoles, account.application_repository_roles),
          scopes: editAccountForm.scopes,
          expires_at: parseOptionalDateTime(editAccountForm.expiresAt) ?? '',
          rotation_interval_days: parseRotationDays(editAccountForm.rotationDays),
        }) as DashboardServiceAccount
        publishAuthoritativeAccount(updated)
        setAccountEditMode(false)
        setActionMessage(`Updated ${editAccountForm.name.trim()}.`)
        setActionError('')
        succeeded = true
        await loadAccounts()
      } catch (error: unknown) {
        setActionError(getErrorMessage(error, 'Unable to update service account'))
        setActionMessage('')
      }
    })
    return succeeded
  }

  const runSimulator = async () => {
    const principalID = simulatorPrincipal.id.trim()
    if (!principalID) {
      setSimulatorError('Choose a principal to simulate.')
      setSimulatorResult(null)
      return
    }
    const requestGeneration = ++simulatorGenerationRef.current
    const requestedScopeKey = simulatorScopeKey
    const isCurrentRequest = () => (
      simulatorGenerationRef.current === requestGeneration
      && simulatorScopeKeyRef.current === requestedScopeKey
    )
    setSimulatorLoading(true)
    setSimulatorError('')
    try {
      const result = await api.getAuthEffectiveAccessForPrincipal({
        principal_type: simulatorPrincipal.type,
        principal_id: principalID,
        project_id: activeProject?.id,
        domain_id: activeProject?.domain_id || activeDomain?.id,
        application_repository_id: applicationRepository?.id,
      })
      if (isCurrentRequest()) setSimulatorResult(result)
    } catch (error: unknown) {
      if (!isCurrentRequest()) return
      setSimulatorError(getErrorMessage(error, 'Unable to simulate effective access'))
      setSimulatorResult(null)
    } finally {
      if (isCurrentRequest()) setSimulatorLoading(false)
    }
  }

  const exportActivity = async () => {
    if (!canViewUserActivity) return
    setActivityExporting(true)
    setActivityMessage('')
    setActivityError('')
    try {
      const blob = await api.downloadAuthUserActivity({ ...buildActivityFilter(appliedActivityFilter), created_before: activitySnapshotBefore(appliedActivityFilter.createdBefore, activitySnapshot), all: 'true' })
      const date = new Date().toISOString().slice(0, 10)
      const outcome = await downloadBlob(blob, `apexload-user-activity-${date}.csv`)
      if (outcome === 'cancelled') return
      setActivityMessage('Activity export downloaded.')
      setActivityError('')
    } catch (error: unknown) {
      setActivityError(getErrorMessage(error, 'Unable to export activity'))
      setActivityMessage('')
    } finally {
      setActivityExporting(false)
    }
  }

  const addBindingDraft = (setter: Dispatch<SetStateAction<UserBindingForm[]>>, scopeType: DashboardUserRoleScopeType) => {
    setter(prev => [...prev, createUserBindingDraft(scopeType, projects, domains, activeProject, activeDomain, applicationRepository)])
  }

  const updateBindingDraft = (setter: Dispatch<SetStateAction<UserBindingForm[]>>, draftID: string, updates: Partial<UserBindingForm>) => {
    setter(prev => prev.map(draft => {
      if (draft.id !== draftID) return draft
      if (updates.scopeType && updates.scopeType !== draft.scopeType) {
        return {
          ...draft,
          ...updates,
          scopeID: defaultScopeID(updates.scopeType, projects, domains, activeProject, activeDomain, applicationRepository),
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

  const cancelSelectedUserEdit = () => {
    resetSelectedUserEditDrafts()
    editingUserIDRef.current = ''
    editingUserSnapshotRef.current = null
    setSelectedUserEditMode(false)
  }

  const resetOnboardDrafts = () => {
    const emptyForm = { subject: '', email: '', displayName: '' }
    const bindings = initialUserBindingDrafts(projects, domains, activeProject, activeDomain, applicationRepository)
    onboardInitialDraftSignature.current = userDraftSignature(emptyForm, bindings)
    setUserForm(emptyForm)
    setOnboardBindingDrafts(bindings)
    setUserActionError('')
  }

  const resetAccountCreationDraft = () => {
    setForm(buildPresetForm(presetByID('ci'), activeProject?.id))
    setActionError('')
  }

  const selectUser = (user: DashboardUser) => {
    runGuarded(() => {
      setUserInspectorFocusRequest(value => value + 1)
      editingUserIDRef.current = ''
      editingUserSnapshotRef.current = null
      setSelectedUserID(user.id)
      setUserInspectorCollapsed(false)
      setSelectedUserEditMode(false)
    })
  }

  const editUser = (user: DashboardUser) => {
    if (!canManageUserBindings) return
    if (selectedUserEditMode && selectedUserID === user.id) {
      setUserInspectorCollapsed(false)
      return
    }
    runGuarded(() => {
      setUserInspectorFocusRequest(value => value + 1)
      editingUserIDRef.current = user.id
      editingUserSnapshotRef.current = user
      setSelectedUserID(user.id)
      setUserInspectorCollapsed(false)
      setSelectedUserEditMode(true)
    })
  }

  const selectAccount = (account: DashboardServiceAccount) => {
    if (selectedAccountID === account.id) {
      setAccountInspectorCollapsed(false)
      return
    }
    runGuarded(() => {
      setSelectedAccountID(account.id)
      setAccountInspectorCollapsed(false)
    })
  }

  const selectActivity = (event: DashboardAuditEvent) => {
    setSelectedActivityID(event.id)
    setActivityInspectorCollapsed(false)
  }

  const onboardDraftDirty = userSubTab === 'onboard'
    && userDraftSignature(userForm, onboardBindingDrafts) !== onboardInitialDraftSignature.current
  const selectedUserDraftDirty = Boolean(
    selectedUserEditMode
    && selectedUser
    && userDraftSignature(editUserForm, editBindingDrafts) !== userDraftSignature(
      {
        subject: selectedUser.subject || '',
        email: selectedUser.email || '',
        displayName: selectedUser.display_name || '',
      },
      (selectedUser.bindings || []).map(userBindingDraftFromBinding),
    ),
  )
  const accountCreationDraftDirty = serviceAccountSubTab === 'generate'
    && serviceAccountDraftSignature(form) !== serviceAccountDraftSignature(buildPresetForm(presetByID('ci'), activeProject?.id))
  const selectedAccountDraftDirty = Boolean(
    accountEditMode
    && selectedAccount
    && serviceAccountDraftSignature(editAccountForm) !== serviceAccountDraftSignature(formFromServiceAccount(selectedAccount)),
  )

  const onboardDraftDirtyRef = useRef(onboardDraftDirty)
  onboardDraftDirtyRef.current = onboardDraftDirty
  const createUserRef = useRef(createUser)
  createUserRef.current = createUser
  const resetOnboardDraftsRef = useRef(resetOnboardDrafts)
  resetOnboardDraftsRef.current = resetOnboardDrafts
  const onboardNavigationGuard = useMemo<NavigationGuard>(() => ({
    id: 'access-onboard-user-draft',
    label: 'User onboarding form',
    isDirty: () => onboardDraftDirtyRef.current,
    save: () => createUserRef.current(),
    discard: () => resetOnboardDraftsRef.current(),
  }), [])
  useNavigationGuard(canManageUserLifecycle ? onboardNavigationGuard : null, onboardDraftDirty)

  const selectedUserDraftDirtyRef = useRef(selectedUserDraftDirty)
  selectedUserDraftDirtyRef.current = selectedUserDraftDirty
  const updateSelectedUserRef = useRef(updateSelectedUser)
  updateSelectedUserRef.current = updateSelectedUser
  const discardSelectedUserDraftRef = useRef<() => void>(() => undefined)
  discardSelectedUserDraftRef.current = cancelSelectedUserEdit
  const selectedUserNavigationGuard = useMemo<NavigationGuard>(() => ({
    id: 'access-selected-user-edit-draft',
    label: 'User editor',
    isDirty: () => selectedUserDraftDirtyRef.current,
    save: () => updateSelectedUserRef.current(),
    discard: () => discardSelectedUserDraftRef.current(),
  }), [])
  useNavigationGuard(selectedUser ? selectedUserNavigationGuard : null, selectedUserDraftDirty)

  const accountCreationDraftDirtyRef = useRef(accountCreationDraftDirty)
  accountCreationDraftDirtyRef.current = accountCreationDraftDirty
  const createAccountRef = useRef(createAccount)
  createAccountRef.current = createAccount
  const resetAccountCreationDraftRef = useRef(resetAccountCreationDraft)
  resetAccountCreationDraftRef.current = resetAccountCreationDraft
  const accountCreationNavigationGuard = useMemo<NavigationGuard>(() => ({
    id: 'access-service-account-create-draft',
    label: 'Service-account creation form',
    isDirty: () => accountCreationDraftDirtyRef.current,
    save: () => createAccountRef.current(),
    discard: () => resetAccountCreationDraftRef.current(),
  }), [])
  useNavigationGuard(canManageServiceAccounts ? accountCreationNavigationGuard : null, accountCreationDraftDirty)

  const selectedAccountDraftDirtyRef = useRef(selectedAccountDraftDirty)
  selectedAccountDraftDirtyRef.current = selectedAccountDraftDirty
  const selectedAccountRef = useRef(selectedAccount)
  selectedAccountRef.current = selectedAccount
  const updateAccountRef = useRef(updateAccount)
  updateAccountRef.current = updateAccount
  const discardSelectedAccountDraftRef = useRef<() => void>(() => undefined)
  discardSelectedAccountDraftRef.current = () => cancelEditAccount()
  const selectedAccountNavigationGuard = useMemo<NavigationGuard>(() => ({
    id: 'access-selected-service-account-edit-draft',
    label: 'Service-account editor',
    isDirty: () => selectedAccountDraftDirtyRef.current,
    save: () => {
      const account = selectedAccountRef.current
      return account ? updateAccountRef.current(account) : false
    },
    discard: () => discardSelectedAccountDraftRef.current(),
  }), [])
  useNavigationGuard(selectedAccount ? selectedAccountNavigationGuard : null, selectedAccountDraftDirty)

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
                  {canManageUserLifecycle && <option value="global">Global</option>}
                  <option value="domain">Domain</option>
                  <option value="project">Project</option>
                  {applicationRepository && <option value="application_repository">Repository</option>}
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
              {draft.scopeType === 'application_repository' && applicationRepository && (
                <label>
                  <span>Repository</span>
                  <select value={draft.scopeID} onChange={event => updateBindingDraft(setter, draft.id, { scopeID: event.target.value })}>
                    <option value={applicationRepository.id}>{applicationRepository.name}</option>
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
        {canManageUserLifecycle && (
          <button className="btn btn-secondary btn-sm" type="button" onClick={() => addBindingDraft(setter, 'global')}>Add Global Role</button>
        )}
        <button className="btn btn-secondary btn-sm" type="button" onClick={() => addBindingDraft(setter, 'domain')} disabled={domains.length === 0 && !activeDomain}>Add Domain</button>
        <button className="btn btn-secondary btn-sm" type="button" onClick={() => addBindingDraft(setter, 'project')} disabled={projects.length === 0}>Add Project</button>
        <button className="btn btn-secondary btn-sm" type="button" onClick={() => addBindingDraft(setter, 'application_repository')} disabled={!applicationRepository}>Add Repository</button>
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
              <button className="btn btn-secondary btn-sm" type="button" onClick={() => editUser(selectedUser)} disabled={!canManageUserBindings || accessMutationBusy}>
                Edit
              </button>
            </div>
            <div className="access-binding-list readonly">
              {(selectedUser.bindings || []).length > 0 ? selectedUser.bindings?.map(binding => (
                <div className="access-binding-row" key={binding.id}>
                  <div>
                    <strong>{ROLE_LABELS[binding.role] || binding.role}</strong>
                    <span>{SCOPE_LABELS[binding.scope_type] || binding.scope_type} · {bindingLabel(binding, projects, domains, applicationRepository)}</span>
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
              <input value={editUserForm.subject} onChange={event => setEditUserForm(prev => ({ ...prev, subject: event.target.value }))} disabled={!canManageUserLifecycle} />
            </label>
            <FormField label="Email" htmlFor="edit-user-email" error={editUserEmailInvalid ? 'Enter a valid email address.' : undefined}><input value={editUserForm.email} onChange={event => setEditUserForm(prev => ({ ...prev, email: event.target.value }))} disabled={!canManageUserLifecycle} aria-invalid={editUserEmailInvalid || undefined} /></FormField>
            <FormField label="Display Name" htmlFor="edit-user-displayName"><input value={editUserForm.displayName} onChange={event => setEditUserForm(prev => ({ ...prev, displayName: event.target.value }))} disabled={!canManageUserLifecycle} /></FormField>
          </div>
          <div className="access-account-actions">
            <button className="btn btn-primary btn-sm" type="button" onClick={updateSelectedUser} disabled={accessMutationBusy || !canManageUserBindings || (canManageUserLifecycle && editUserEmailInvalid)}>Save Changes</button>
            <button className="btn btn-secondary btn-sm" type="button" onClick={cancelSelectedUserEdit} disabled={accessMutationBusy}>Cancel</button>
            <button className="btn btn-secondary btn-sm" type="button" onClick={() => setUserActive(selectedUser, !selectedUser.active)} disabled={accessMutationBusy || !canManageUserLifecycle}>
              {selectedUser.active ? 'Remove Access' : 'Restore Access'}
            </button>
            <button className="btn btn-secondary btn-sm" type="button" onClick={() => revokeSessions(selectedUser)} disabled={accessMutationBusy || !canManageUserLifecycle} title="Sign the user out of all active sessions without disabling the account">
              Revoke Sessions
            </button>
            <button className="btn btn-danger btn-sm" type="button" onClick={() => deleteUser(selectedUser)} disabled={accessMutationBusy || !canManageUserLifecycle} title="Permanently delete the user and their role bindings (prefer Remove Access to keep the audit trail)">
              Delete User
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
      <aside className="access-inspector access-panel" id="access-user-details" aria-label="Selected user details">
        <div className="access-panel-header">
          <div>
            <h3 ref={userInspectorHeadingRef} tabIndex={-1}>User Details: {selectedUser.display_name || selectedUser.email || selectedUser.subject}</h3>
            <p>{selectedUser.display_name || selectedUser.email || selectedUser.subject}</p>
          </div>
          <PanelToggle expanded={true} panelName="user details" controls="access-user-details" onToggle={() => setUserInspectorCollapsed(true)} />
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
                  <span>{SCOPE_LABELS[binding.scope_type] || binding.scope_type} · {bindingLabel(binding, projects, domains, applicationRepository)}</span>
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

  // A scoped role list (one role per scope id) shared by every service-account scope tier.
  const renderScopedRoleList = (
    label: string,
    description: string,
    items: { id: string; name: string }[],
    emptyHint: string,
    rolesKey: 'projectRoles' | 'domainRoles' | 'applicationRepositoryRoles',
    defaultRole: string,
    current: ServiceAccountForm,
    setter: Dispatch<SetStateAction<ServiceAccountForm>>,
  ) => (
    <div className="access-control-section">
      <div>
        <h4>{label}</h4>
        <p>{description}</p>
      </div>
      <div className="access-project-role-list">
        {items.length > 0 ? items.map(item => {
          const selectedRole = current[rolesKey][item.id]
          return (
            <div className="access-project-role-row" key={item.id}>
              <label className="access-project-checkbox">
                <input
                  type="checkbox"
                  checked={Boolean(selectedRole)}
                  onChange={event => setter(prev => {
                    const next = { ...prev[rolesKey] }
                    if (event.target.checked) {
                      next[item.id] = selectedRole || defaultRole
                    } else {
                      delete next[item.id]
                    }
                    return { ...prev, [rolesKey]: next }
                  })}
                />
                <span>{item.name}</span>
              </label>
              <select
                aria-label={`Role for ${item.name}`}
                value={selectedRole || defaultRole}
                disabled={!selectedRole}
                onChange={event => setter(prev => ({ ...prev, [rolesKey]: { ...prev[rolesKey], [item.id]: event.target.value } }))}
              >
                {SCOPED_ROLE_OPTIONS.map(option => (
                  <option key={option.id} value={option.id}>{option.label}</option>
                ))}
              </select>
            </div>
          )
        }) : (
          <p className="access-muted">{emptyHint}</p>
        )}
      </div>
    </div>
  )

  // Global roles, token scopes, and the project/domain/repository role pickers — shared by
  // the create form and the inline edit form so both can grant all four scope tiers. The
  // create form passes the active preset's project role as the default; edit defaults to viewer.
  const renderServiceAccountRoleControls = (current: ServiceAccountForm, setter: Dispatch<SetStateAction<ServiceAccountForm>>, projectDefaultRole = 'viewer') => (
    <>
      <div className="access-control-section">
        <div>
          <h4>Global Roles</h4>
          <p>System-wide RBAC roles granted to this key.</p>
        </div>
        <div className="access-chip-row">
          {GLOBAL_ROLE_OPTIONS.map(option => (
            <button
              key={option.id}
              type="button"
              aria-pressed={current.globalRoles.includes(option.id)}
              className={`access-chip ${current.globalRoles.includes(option.id) ? 'active' : ''}`}
              onClick={() => setter(prev => ({ ...prev, globalRoles: toggleValue(prev.globalRoles, option.id) }))}
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
              aria-pressed={current.scopes.includes(option.id)}
              className={`access-chip ${current.scopes.includes(option.id) ? 'active' : ''}`}
              onClick={() => setter(prev => ({ ...prev, scopes: toggleValue(prev.scopes, option.id) }))}
            >
              {option.label}
            </button>
          ))}
        </div>
        {current.scopes.includes('automation:*') && (
          <div className="access-alert warning" role="status">
            automation:* is a broad token scope. RBAC still applies, but the key will not be narrowed by per-feature token scopes.
          </div>
        )}
      </div>

      {renderScopedRoleList('Project Roles', 'Project-scoped role assignments.', projects.map(project => ({ id: project.id, name: project.name })), 'Create or select a project to assign project-scoped roles.', 'projectRoles', projectDefaultRole, current, setter)}
      {renderScopedRoleList('Domain Roles', 'Domain-scoped role assignments.', (domains || []).map(domain => ({ id: domain.id, name: domain.name })), 'No domains available to assign domain-scoped roles.', 'domainRoles', 'viewer', current, setter)}
      {applicationRepository ? renderScopedRoleList('Repository Roles', 'Application-repository-scoped role assignments.', [{ id: applicationRepository.id, name: applicationRepository.name }], '', 'applicationRepositoryRoles', 'viewer', current, setter) : null}
    </>
  )

  const renderAccountEditForm = (account: DashboardServiceAccount) => {
    const additionalEntries = additionalScopedRoleEntries(account, projects, domains, applicationRepository)
    return (
    <aside className="access-inspector access-panel" aria-label="Edit service account">
      <div className="access-panel-header">
        <div>
          <h3>Edit Account</h3>
          <p>{account.name}</p>
        </div>
        <button className="btn btn-ghost btn-sm" type="button" onClick={cancelEditAccount}>Cancel</button>
      </div>
      <div className="access-form-grid">
        <label>
          <span>Service Account Name</span>
          <input value={editAccountForm.name} onChange={event => setEditAccountForm(prev => ({ ...prev, name: event.target.value }))} />
        </label>
        <label>
          <span>Description</span>
          <input value={editAccountForm.description} onChange={event => setEditAccountForm(prev => ({ ...prev, description: event.target.value }))} />
        </label>
        <label>
          <span>Owner</span>
          <input value={editAccountForm.owner} onChange={event => setEditAccountForm(prev => ({ ...prev, owner: event.target.value }))} placeholder="team or person responsible" />
        </label>
        <label>
          <span>Contact Email</span>
          <input value={editAccountForm.contact} onChange={event => setEditAccountForm(prev => ({ ...prev, contact: event.target.value }))} placeholder="oncall@example.com" aria-invalid={editAccountContactInvalid || undefined} />
          {editAccountContactInvalid && <span className="access-field-error">Enter a valid email address.</span>}
        </label>
        <label>
          <span>Expires At</span>
          <input type="datetime-local" value={editAccountForm.expiresAt} onChange={event => setEditAccountForm(prev => ({ ...prev, expiresAt: event.target.value }))} />
        </label>
        <label>
          <span>Rotation interval (days)</span>
          <input type="number" min={0} step={1} value={editAccountForm.rotationDays} onChange={event => setEditAccountForm(prev => ({ ...prev, rotationDays: event.target.value }))} />
        </label>
      </div>
      {renderServiceAccountRoleControls(editAccountForm, setEditAccountForm)}
      {additionalEntries.length > 0 && (
        <div className="access-control-section access-readonly-section">
          <h4>Additional Roles</h4>
          <p>These roles are retained unless that scope is removed or replaced.</p>
          <div className="access-binding-list readonly">
            {additionalEntries.map(entry => (
              <div className="access-binding-row" key={entry.key}>
                <div>
                  <strong>{entry.label}</strong>
                  <span>{entry.roles.map(role => ROLE_LABELS[role] || role).join(', ')}</span>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
      {(actionError || actionMessage) && (
        <div className={`access-alert ${actionError ? 'error' : 'success'}`}>
          {actionError || actionMessage}
          {accountsLoadFailed && (
            <button className="btn btn-secondary btn-sm" type="button" onClick={() => { void loadAccounts() }} disabled={loading || accessMutationBusy}>
              Retry accounts
            </button>
          )}
        </div>
      )}
      <div className="access-account-actions">
        <button className="btn btn-primary btn-sm" type="button" onClick={() => updateAccount(account)} disabled={accessMutationBusy || !canManageServiceAccounts || editAccountContactInvalid}>Save Changes</button>
        <button className="btn btn-ghost btn-sm" type="button" onClick={cancelEditAccount} disabled={accessMutationBusy}>Cancel</button>
      </div>
    </aside>
    )
  }

  const renderAccountInspector = () => {
    if (!selectedAccount || accountInspectorCollapsed) return null
    if (accountEditMode) return renderAccountEditForm(selectedAccount)
    const roles = accountRoleList(selectedAccount)
    const accessLabels = accountAccessLabels(selectedAccount, projects, domains)
    return (
      <aside className="access-inspector access-panel" id="access-account-details" aria-label="Selected service account details">
        <div className="access-panel-header">
          <div>
            <h3>Account Details</h3>
            <p>{selectedAccount.name}</p>
          </div>
          <PanelToggle expanded={true} panelName="service account details" controls="access-account-details" onToggle={() => setAccountInspectorCollapsed(true)} />
        </div>
        <div className="access-badge-row">
          {accountBadges(selectedAccount).map(badge => (
            <span className={`access-status-badge ${badge.tone}`} key={badge.label}>{badge.label}</span>
          ))}
        </div>
        <dl className="access-detail-grid">
          <div><dt>Description</dt><dd>{selectedAccount.description || 'None'}</dd></div>
          <div><dt>Owner</dt><dd>{selectedAccount.owner_id || 'Unassigned'}</dd></div>
          <div><dt>Contact</dt><dd>{selectedAccount.contact_email || 'None'}</dd></div>
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
          <button className="btn btn-primary btn-sm" type="button" onClick={() => beginEditAccount(selectedAccount)} disabled={accessMutationBusy || !canManageServiceAccounts}>Edit</button>
          <button className="btn btn-secondary btn-sm" type="button" onClick={() => rotateAccount(selectedAccount)} disabled={accessMutationBusy || !canManageServiceAccounts}>Rotate Key</button>
          <button className="btn btn-secondary btn-sm" type="button" onClick={() => setAccountActive(selectedAccount, !selectedAccount.active)} disabled={accessMutationBusy || !canManageServiceAccounts}>
            {selectedAccount.active ? 'Disable' : 'Enable'}
          </button>
          <button className="btn btn-ghost btn-sm access-danger-action" type="button" onClick={() => deleteAccount(selectedAccount)} disabled={accessMutationBusy || !canManageServiceAccounts}>Delete</button>
        </div>
      </aside>
    )
  }

  const [reviewExportMessage, setReviewExportMessage] = useState('')
  const exportReview = async () => {
    if (!accessReview) return
    try {
      const rows: unknown[][] = [['type', 'id', 'name', 'priority', 'flags', 'owner', 'generated_at']]
      for (const user of accessReview.users) rows.push(['user', user.id, user.display_name || user.subject, reviewPriority(user.flags), user.flags.join('; '), '', accessReview.generated_at])
      for (const account of accessReview.service_accounts) rows.push(['service_account', account.id, account.name, reviewPriority(account.flags), account.flags.join('; '), accounts.find(item => item.id === account.id)?.owner_id || '', accessReview.generated_at])
      if (await downloadBlob(csvBlob(rows), 'apexload-access-review.csv') !== 'cancelled') setReviewExportMessage('Access review export downloaded.')
    } catch (error) { setReviewExportMessage(getErrorMessage(error, 'Unable to export access review')) }
  }
  const inspectReviewUser = async (id: string, subject: string) => {
    try {
      const user = await api.getAuthUser(id)
      if (!user) throw new Error('User is no longer available. Refresh the review.')
      usersLoadGenerationRef.current += 1
      authoritativeUsersRef.current.set(user.id, { entity: user, listScope: dataSignature({ page: 0, pageSize: userPageSize, domainID: '', projectID: '', query: subject.trim(), role: '', status: '' }), isSatisfiedBy: (serverUser, authoritativeUser) => userMutationStateSignature(serverUser) === userMutationStateSignature(authoritativeUser) })
      setUserFilters({ query: subject, status: '', role: '', domainID: '', projectID: '' })
      setUserPage(0); setUsers([user]); setSelectedUserID(id); setUserInspectorCollapsed(false); setUserSubTab('existing')
    } catch (error) { setAccessReviewError(getErrorMessage(error, 'Unable to inspect user')) }
  }
  const inspectReviewAccount = async (id: string) => {
    try {
      const response = await api.listAuthServiceAccounts()
      const refreshed = response.service_accounts || []
      if (!refreshed.some(account => account.id === id)) throw new Error('Account is no longer available. Refresh the review.')
      accountsLoadGenerationRef.current += 1
      setAccounts(refreshed)
      setAccountFilters({ query: '', status: '', role: '', scope: '', domainID: '', projectID: '' })
      setSelectedAccountID(id); setAccountInspectorCollapsed(false); setServiceAccountSubTab('existing')
    } catch (error) { setAccessReviewError(getErrorMessage(error, 'Unable to inspect service account')) }
  }

  const renderAccessReview = () => {
    if (accessReviewLoading) return <div className="access-empty">Loading access review…</div>
    if (accessReviewError) return <div className="access-error" role="alert">{accessReviewError} <button onClick={() => void loadAccessReview()}>Retry access review</button></div>
    if (!accessReview) return <div className="access-empty">No access review data.</div>
    const { users: reviewUsers, service_accounts: reviewAccounts, stale_after_days } = accessReview
    return (
      <div className="access-review">
        {reviewUsers.length === 0 && reviewAccounts.length === 0 && <p role="status">Nothing to review — no stale, unbound, over-privileged, or expiring principals.</p>}
        <button className="btn btn-secondary" onClick={() => void exportReview()}>Export access review CSV</button>
        {reviewExportMessage && <p role="status">{reviewExportMessage}</p>}
        <p>Deployment-wide findings as of {formatTimestamp(accessReview.generated_at)}. Informational findings include disabled identities. Use Inspect to change access or rotate keys, then refresh to verify the finding is resolved.</p>
        <p className="setting-desc">
          Principals worth a periodic look. Users are flagged stale after {stale_after_days} days without sign-in.
          {' '}
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => loadAccessReview()}>Refresh</button>
        </p>
        {reviewUsers.length > 0 && (
          <section>
            <h4>Users ({reviewUsers.length})</h4>
            <table className="access-data-table access-review-table">
              <thead><tr><th>User</th><th>Global roles</th><th>Last seen</th><th>Priority / reasons</th><th>Action</th></tr></thead>
              <tbody>
                {reviewUsers.map(u => (
                  <tr key={u.id}>
                    <td><button className="access-row-primary" onClick={() => void inspectReviewUser(u.id, u.subject)}>{u.display_name || u.subject}</button></td>
                    <td className="cell-mono">{(u.global_roles || []).join(', ') || '—'}</td>
                    <td>{u.last_seen_at ? new Date(u.last_seen_at).toLocaleDateString() : 'never'}</td>
                    <td><strong className="access-review-priority">{reviewPriority(u.flags)}</strong>{u.flags.map(f => <span key={f} className="access-flag-badge">{f.replace(/_/g, ' ')}</span>)}</td>
                    <td><button className="btn btn-secondary btn-sm" onClick={() => void inspectReviewUser(u.id, u.subject)}>Inspect user</button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
        )}
        {reviewAccounts.length > 0 && (
          <section>
            <h4>Service Accounts ({reviewAccounts.length})</h4>
            <table className="access-data-table access-review-table">
              <thead><tr><th>Account</th><th>Scopes</th><th>Last used</th><th>Priority / reasons</th><th>Action</th></tr></thead>
              <tbody>
                {reviewAccounts.map(a => (
                  <tr key={a.id}>
                    <td><button className="access-row-primary" onClick={() => void inspectReviewAccount(a.id)}>{a.name}</button><span className="access-row-meta">Owner: {accounts.find(item => item.id === a.id)?.owner_id || 'Unassigned'}</span></td>
                    <td className="cell-mono">{(a.scopes || []).join(', ') || '—'}</td>
                    <td>{a.last_used_at ? new Date(a.last_used_at).toLocaleDateString() : 'never'}</td>
                    <td><strong className="access-review-priority">{reviewPriority(a.flags)}</strong>{a.flags.map(f => <span key={f} className="access-flag-badge">{f.replace(/_/g, ' ')}</span>)}</td>
                    <td><button className="btn btn-secondary btn-sm" onClick={() => void inspectReviewAccount(a.id)}>Inspect account</button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
        )}
      </div>
    )
  }

  const renderSimulator = () => {
    const principalOptions = simulatorPrincipalOptions
    // Mirror the backend normalization (empty project_id → "unassigned", empty domain_id →
    // "default") so the displayed scope matches what the simulator actually evaluates.
    const simProjectLabel = activeProject?.name || 'Unassigned'
    const simDomainLabel = domains.find(domain => domain.id === activeProject?.domain_id)?.name || activeDomain?.name || 'Default Domain'
    return (
      <section className="access-simulator">
        <h4>Role permissions preview</h4>
        <p className="setting-desc">
          Preview assigned role permissions in the active scope (project {simProjectLabel}, domain {simDomainLabel}). This is not an API authorization check: account status, token scopes and route policies may deny these operations.
        </p>
        {simulatorPrincipal.type === 'user' && <div className="access-table-toolbar">
          <label><span>Find a user for role preview</span><input value={simulatorQuery} onChange={event => { setSimulatorQuery(event.target.value); setSimulatorPage(0); setSimulatorPrincipal({ type: 'user', id: '' }); setSimulatorResult(null) }} /></label>
          <button disabled={simulatorDirectoryLoading || simulatorPage === 0} onClick={() => { setSimulatorPage(value => value - 1); setSimulatorPrincipal({ type: 'user', id: '' }); setSimulatorResult(null) }}>Previous users</button>
          <button disabled={simulatorDirectoryLoading || !simulatorHasMore} onClick={() => { setSimulatorPage(value => value + 1); setSimulatorPrincipal({ type: 'user', id: '' }); setSimulatorResult(null) }}>More users</button>
          {simulatorDirectoryLoading && <p role="status">Loading user choices…</p>}
          {simulatorDirectoryError && <p role="alert">{simulatorDirectoryError} <button onClick={() => setSimulatorDirectoryRevision(value => value + 1)}>Retry user search</button></p>}
        </div>}
        <div className="access-form-grid">
          <label>
            <span>Principal Type</span>
            <select
              value={simulatorPrincipal.type}
              onChange={event => { setSimulatorPrincipal({ type: event.target.value as 'user' | 'service_account', id: '' }); setSimulatorResult(null) }}
            >
              <option value="user">User</option>
              <option value="service_account">Service Account</option>
            </select>
          </label>
          <label>
            <span>Principal</span>
            <select value={simulatorPrincipal.id} onChange={event => { setSimulatorPrincipal(prev => ({ ...prev, id: event.target.value })); setSimulatorResult(null) }}>
              <option value="">Select…</option>
              {principalOptions.map(option => (
                <option key={option.id} value={option.id}>{option.label}</option>
              ))}
            </select>
          </label>
        </div>
        <div className="access-form-actions">
          <button className="btn btn-secondary btn-sm" type="button" onClick={runSimulator} disabled={simulatorLoading || !simulatorPrincipal.id}>
            {simulatorLoading ? 'Simulating…' : 'Simulate'}
          </button>
        </div>
        {simulatorError && <div className="access-alert error">{simulatorError}</div>}
        {simulatorResult && (
          <div className="access-simulator-result">
            <p className="setting-desc">
              <strong>{simulatorResult.principal_name || 'Principal'}</strong>
              {simulatorResult.scopes_narrow_api_access ? ' — token scopes further narrow this key’s API access.' : ''}
            </p>
            <table className="access-data-table access-review-table">
              <thead><tr><th>Resource</th><th>Operations granted by roles</th></tr></thead>
              <tbody>
                {simulatorResult.resources.map(resource => {
                  const allowed = resource.operations.filter(op => op.allowed)
                  return (
                    <tr key={resource.id}>
                      <td>{resource.name}</td>
                      <td className={allowed.length === 0 ? 'access-muted' : ''}>{allowed.length === 0 ? 'None' : allowed.map(op => op.label).join(', ')}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    )
  }

  const renderActivityInspector = () => {
    if (!selectedActivity || activityInspectorCollapsed) return null
    return (
      <aside className="access-inspector access-panel" id="access-activity-details" aria-label="Selected activity event details">
        <div className="access-panel-header">
          <div>
            <h3>Event Details</h3>
            <p>{selectedActivity.action}</p>
          </div>
          <PanelToggle expanded={true} panelName="activity event details" controls="access-activity-details" onToggle={() => setActivityInspectorCollapsed(true)} />
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
    <div className="page access-page section-layout">
      <AccessNavigation
        currentView={currentAccessView}
        onSelectView={view => runGuarded(() => setAccessView(view))}
        counts={{
          users: canUseUsers ? (usersTotal > users.length ? `${users.length} of ${usersTotal}` : users.length) : undefined,
          accounts: canManageServiceAccounts ? accounts.length : undefined,
          activity: canViewUserActivity ? activityEvents.length : undefined,
          reviewAttention: accessReviewAttention?.total,
        }}
      />
      <div className="section-layout-main">
      <div className="page-header">
        <div>
          <h2>Access</h2>
          <p className="page-subtitle">Users, automation keys, and activity across domains and projects.</p>
        </div>
        {!hasTopbarContributionProvider && (
          <div className="access-header-actions">
            <span className={`access-permission-badge ${accessWorkspaceAvailable ? 'healthy' : 'danger'}`}>
              {accessWorkspaceAvailable ? 'Access workspace available' : 'Read-only session'}
            </span>
          </div>
        )}
      </div>

      {activeTab === 'users' && (
        <section className={`access-workspace ${userSubTab === 'onboard' || userInspectorCollapsed || !selectedUser ? 'access-workspace-full' : ''}`} role="tabpanel" aria-label="Users access workspace">
          <article className="access-panel access-table-panel">
            <div className="access-panel-header">
              <div>
                <h3>Users</h3>
                <p>{usersLoading ? 'Loading users...' : `${filteredUsers.length} of ${usersTotal} users`}</p>
              </div>
              {userSubTab === 'existing' && selectedUser && userInspectorCollapsed && (
                <PanelToggle expanded={false} panelName="user details" controls="access-user-details" onToggle={() => setUserInspectorCollapsed(false)} />
              )}
            </div>
            <TabList className="access-subtab-row" role="tablist" aria-label="User workspace views" onKeyDown={handleTabKey}>
              <button type="button" role="tab" aria-selected={userSubTab === 'existing'} className={`access-subtab ${userSubTab === 'existing' ? 'active' : ''}`} onClick={() => runGuarded(() => setUserSubTab('existing'))} tabIndex={(userSubTab === 'existing') ? 0 : -1}>Existing Users</button>
              {canManageUserLifecycle && (
                <button type="button" role="tab" aria-selected={userSubTab === 'onboard'} className={`access-subtab ${userSubTab === 'onboard' ? 'active' : ''}`} onClick={() => runGuarded(() => setUserSubTab('onboard'))} tabIndex={(userSubTab === 'onboard') ? 0 : -1}>Onboard Users</button>
              )}
            </TabList>

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
                    {usersLoadFailed && (
                      <button className="btn btn-secondary btn-sm" type="button" onClick={() => { void loadUsers() }} disabled={usersLoading || accessMutationBusy}>
                        Retry users
                      </button>
                    )}
                  </div>
                )}

                <label className="access-column-choice"><input type="checkbox" checked={showUserMetadata} onChange={event => setShowUserMetadata(event.target.checked)} /> Show email, dates and usage columns</label>
                <div className="access-table-wrap">
                  <table className={`access-data-table access-users-table ${showUserMetadata ? '' : 'access-columns-compact'}`} data-table-pagination="off">
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
                        const accessLabels = userAccessLabels(user, projects, domains, applicationRepository)
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
                        <td aria-label="User actions">
                              <div className="access-table-actions">
                                <button className="btn btn-secondary btn-sm" type="button" onClick={event => { event.stopPropagation(); selectUser(user) }}>Inspect</button>
                                <button className="btn btn-secondary btn-sm" type="button" onClick={event => { event.stopPropagation(); editUser(user) }} disabled={accessMutationBusy || !canManageUserBindings}>Edit</button>
                                <button className="btn btn-ghost btn-sm" type="button" onClick={event => { event.stopPropagation(); setUserActive(user, !user.active) }} disabled={accessMutationBusy || !canManageUserLifecycle}>
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
                      <strong>{Object.values(userFilters).some(Boolean) ? 'No users match these filters' : 'No users in this scope'}</strong>
                      <span>{Object.values(userFilters).some(Boolean) ? 'Clear search or filters to see other users.' : canManageUserLifecycle ? 'Open Onboard Users to create the first account.' : 'Only users in your permitted scope are shown. Ask an identity administrator to create an account.'}</span>
                    </div>
                  )}
                </div>
                <div className="access-pagination-toolbar" aria-label="User pagination">
                  <span>{usersLoading ? 'Loading users...' : `Showing ${userRangeStart}-${userRangeEnd} of ${usersTotal}`}</span>
                  <label>
                    <span>Rows</span>
                    <select value={userPageSize} onChange={event => { setUserPageSize(Number(event.target.value)); setUserPage(0) }}>
                      {USER_PAGE_SIZE_OPTIONS.map(size => <option key={size} value={size}>{size}</option>)}
                    </select>
                  </label>
                  <div className="access-pagination-actions">
                    <button className="btn btn-secondary btn-sm" type="button" onClick={() => setUserPage(page => Math.max(0, page - 1))} disabled={userPage <= 0 || usersLoading}>Previous</button>
                    <span>Page {Math.min(userPage + 1, userPageCount)} of {userPageCount}</span>
                    <button className="btn btn-secondary btn-sm" type="button" onClick={() => setUserPage(page => Math.min(userPageCount - 1, page + 1))} disabled={userPage >= userPageCount - 1 || usersLoading}>Next</button>
                  </div>
                </div>
                {renderUserManagementPanel()}
              </>
            )}

            {userSubTab === 'onboard' && canManageUserLifecycle && (
              <>
                <p className="access-onboarding-help">This creates an access record, not an identity-provider account. No invitation or password is sent. {authMode === 'dev' || authMode === 'disabled' ? 'Development bypass is active; roles take effect when an identity-based authentication mode is enabled.' : authMode === 'local' ? 'Local Test Login uses the configured test personas; this form does not create a new local login.' : 'Create the identity in your configured provider first, copy its stable subject here, and have the user sign in through the deployment’s normal login flow.'} <a href={settingsHrefForAuth}>Review authentication setup</a></p>
                <div className="access-form-grid">
                  <label>
                    <span>Subject</span>
                    <input value={userForm.subject} onChange={event => setUserForm(prev => ({ ...prev, subject: event.target.value }))} placeholder="oidc-subject-or-upstream-user" aria-describedby="identity-subject-help" />
                    <small id="identity-subject-help">The stable user identifier supplied by your identity provider, not the display name. Ask your identity administrator for this value.</small>
                  </label>
                  <FormField label="Email" htmlFor="new-user-email" error={onboardEmailInvalid ? 'Enter a valid email address.' : undefined}><input value={userForm.email} onChange={event => setUserForm(prev => ({ ...prev, email: event.target.value }))} placeholder="person@example.com" aria-invalid={onboardEmailInvalid || undefined} /></FormField>
                  <FormField label="Display Name" htmlFor="new-user-displayName"><input value={userForm.displayName} onChange={event => setUserForm(prev => ({ ...prev, displayName: event.target.value }))} placeholder="Jordan Lee" /></FormField>
                </div>
                <div className="access-control-section">
                  <h4>Initial Permissions</h4><p>Viewer can inspect results. Editor can author and run tests. Project Admin additionally manages project access and settings.</p>
                  <p>Assign the user's starting global, domain, and project roles before the account is created.</p>
                  {renderUserBindingDraftEditor(onboardBindingDrafts, setOnboardBindingDrafts)}
                </div>
                {(userActionError || userActionMessage) && (
                  <div className={`access-alert ${userActionError ? 'error' : 'success'}`}>
                    {userActionError || userActionMessage}
                    {usersLoadFailed && (
                      <button className="btn btn-secondary btn-sm" type="button" onClick={() => { void loadUsers() }} disabled={usersLoading || accessMutationBusy}>
                        Retry users
                      </button>
                    )}
                  </div>
                )}
                <div className="access-form-actions">
                  <button className="btn btn-primary" type="button" onClick={createUser} disabled={accessMutationBusy || !canManageUserLifecycle || !userForm.subject.trim() || onboardEmailInvalid}>
                    Create User
                  </button>
                </div>
              </>
            )}
          </article>
          {userSubTab === 'existing' && renderUserInspector()}
        </section>
      )}

      {activeTab === 'service_accounts' && (
        <section className={`access-workspace ${serviceAccountSubTab === 'generate' || accountInspectorCollapsed || !selectedAccount ? 'access-workspace-full' : ''}`} role="tabpanel" aria-label="Service accounts access workspace">
          <article className="access-panel access-table-panel">
            <div className="access-panel-header">
              <div>
                <h3>Service Accounts</h3>
                <p>{loading ? 'Loading service accounts...' : `${filteredAccounts.length} of ${accounts.length} automation principals`}</p>
              </div>
              {serviceAccountSubTab === 'existing' && selectedAccount && accountInspectorCollapsed && (
                <PanelToggle expanded={false} panelName="service account details" controls="access-account-details" onToggle={() => setAccountInspectorCollapsed(false)} />
              )}
            </div>
            {accessReviewAttention && (accessReviewAttention.accountsFlagged > 0 || accessReviewAttention.denials > 0) && (
              <div className="access-alert access-attention-banner">
                {accessReviewAttention.accountsFlagged > 0 && (
                  <span>{accessReviewAttention.accountsFlagged} service account(s) have review findings. Inspect the reasons below, including unused accounts, rotation, expiry and scope. </span>
                )}
                {accessReviewAttention.denials > 0 && (
                  <span>{accessReviewAttention.denials} recent access denial(s). </span>
                )}
                {canManageUserLifecycle && (
                  <button type="button" className="btn btn-ghost btn-sm" onClick={() => runGuarded(() => { setActivitySubTab('review') })}>Open access review</button>
                )}
              </div>
            )}
            <TabList className="access-subtab-row" role="tablist" aria-label="Service account workspace views" onKeyDown={handleTabKey}>
              <button type="button" role="tab" aria-selected={serviceAccountSubTab === 'existing'} className={`access-subtab ${serviceAccountSubTab === 'existing' ? 'active' : ''}`} onClick={() => runGuarded(() => setServiceAccountSubTab('existing'))} tabIndex={(serviceAccountSubTab === 'existing') ? 0 : -1}>Existing Accounts</button>
              <button type="button" role="tab" aria-selected={serviceAccountSubTab === 'generate'} className={`access-subtab ${serviceAccountSubTab === 'generate' ? 'active' : ''}`} onClick={() => runGuarded(() => setServiceAccountSubTab('generate'))} tabIndex={(serviceAccountSubTab === 'generate') ? 0 : -1}>Create service account</button>
            </TabList>

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
                    {accountsLoadFailed && (
                      <button className="btn btn-secondary btn-sm" type="button" onClick={() => { void loadAccounts() }} disabled={loading || accessMutationBusy}>
                        Retry accounts
                      </button>
                    )}
                  </div>
                )}

                <label className="access-column-choice"><input type="checkbox" checked={showAccountMetadata} onChange={event => setShowAccountMetadata(event.target.checked)} /> Show last use, rotation, expiry and update columns</label>
                <div className="access-table-wrap">
                  <table className={`access-data-table access-accounts-table ${showAccountMetadata ? '' : 'access-columns-compact'}`}>
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
                            <td aria-label="Service account actions">
                              <div className="access-table-actions">
                                <button className="btn btn-secondary btn-sm" type="button" onClick={event => { event.stopPropagation(); selectAccount(account) }}>Inspect</button>
                                <button className="btn btn-secondary btn-sm" type="button" onClick={event => { event.stopPropagation(); rotateAccount(account) }} disabled={accessMutationBusy}>Rotate Key</button>
                                <button className="btn btn-ghost btn-sm" type="button" onClick={event => { event.stopPropagation(); setAccountActive(account, !account.active) }} disabled={accessMutationBusy}>
                                  {account.active ? 'Disable' : 'Enable'}
                                </button>
                                <button className="btn btn-ghost btn-sm access-danger-action" type="button" onClick={event => { event.stopPropagation(); deleteAccount(account) }} disabled={accessMutationBusy}>Delete</button>
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
                    <span>Owner</span>
                    <input value={form.owner} onChange={event => setForm(prev => ({ ...prev, owner: event.target.value }))} placeholder="team or person responsible" />
                  </label>
                  <label>
                    <span>Contact Email</span>
                    <input value={form.contact} onChange={event => setForm(prev => ({ ...prev, contact: event.target.value }))} placeholder="oncall@example.com" aria-invalid={accountContactInvalid || undefined} />
                    {accountContactInvalid && <span className="access-field-error">Enter a valid email address.</span>}
                  </label>
                  <label>
                    <span>Expires At</span>
                    <input type="datetime-local" value={form.expiresAt} onChange={event => setForm(prev => ({ ...prev, expiresAt: event.target.value }))} />
                  </label>
                  <label>
                    <span>Rotation interval (days)</span>
                    <input type="number" min={1} step={1} value={form.rotationDays} onChange={event => setForm(prev => ({ ...prev, rotationDays: event.target.value }))} />
                  </label>
                </div>

                {renderServiceAccountRoleControls(form, setForm, presetByID(form.purpose).projectRole || 'viewer')}

                {(actionError || actionMessage) && (
                  <div className={`access-alert ${actionError ? 'error' : 'success'}`}>
                    {actionError || actionMessage}
                    {accountsLoadFailed && (
                      <button className="btn btn-secondary btn-sm" type="button" onClick={() => { void loadAccounts() }} disabled={loading || accessMutationBusy}>
                        Retry accounts
                      </button>
                    )}
                  </div>
                )}

                <div className="access-form-actions">
                  <button className="btn btn-primary" type="button" onClick={createAccount} disabled={accessMutationBusy || !canManageServiceAccounts || !form.name.trim() || accountContactInvalid}>
                    Create Service Account
                  </button>
                </div>
              </>
            )}
          </article>
          {serviceAccountSubTab === 'existing' && renderAccountInspector()}
        </section>
      )}

      {activeTab === 'activity' && (
        <section className={`access-workspace ${activitySubTab !== 'events' || activityInspectorCollapsed || !selectedActivity ? 'access-workspace-full' : ''}`} role="tabpanel" aria-label="Activity access workspace">
          <article className="access-panel access-table-panel">
            <div className="access-panel-header">
              <div>
                <h3>{activitySubTab === 'events' ? 'Audit events' : activitySubTab === 'usage' ? 'Usage By User' : 'Access Review'}</h3>
                <p>{activitySubTab === 'events' ? activityLoading ? 'Loading events…' : `${activityEvents.length} events loaded${activityHasMore ? ' · older events available' : ''}` : activitySubTab === 'usage' ? 'Usage by user and matching runs' : 'Review flagged identities and their assigned roles'}</p>
              </div>
              <div className="access-account-actions">
                {activitySubTab === 'events' && selectedActivity && activityInspectorCollapsed && (
                  <PanelToggle expanded={false} panelName="activity event details" controls="access-activity-details" onToggle={() => setActivityInspectorCollapsed(false)} />
                )}
                {activitySubTab === 'events' && <button className="btn btn-secondary btn-sm" type="button" onClick={exportActivity} disabled={!canViewUserActivity || activityExporting}>
                  {activityExporting ? 'Exporting...' : 'Export all matching events CSV'}
                </button>}
              </div>
            </div>
            <TabList className="access-subtab-row" role="tablist" aria-label="Activity workspace views" onKeyDown={handleTabKey}>
              <button type="button" role="tab" aria-selected={activitySubTab === 'events'} className={`access-subtab ${activitySubTab === 'events' ? 'active' : ''}`} onClick={() => setActivitySubTab('events')} tabIndex={(activitySubTab === 'events') ? 0 : -1}>Events</button>
              <button type="button" role="tab" aria-selected={activitySubTab === 'usage'} className={`access-subtab ${activitySubTab === 'usage' ? 'active' : ''}`} onClick={() => setActivitySubTab('usage')} tabIndex={(activitySubTab === 'usage') ? 0 : -1}>Usage By User</button>
              {canManageUserLifecycle && (
                <button type="button" role="tab" aria-selected={activitySubTab === 'review'} className={`access-subtab ${activitySubTab === 'review' ? 'active' : ''}`} onClick={() => setActivitySubTab('review')} tabIndex={(activitySubTab === 'review') ? 0 : -1}>Access Review</button>
              )}
            </TabList>

            {activitySubTab === 'review' && (
              <>
                {renderAccessReview()}
                {canManageUserLifecycle && renderSimulator()}
              </>
            )}

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
                  <button className="btn btn-primary btn-sm" type="button" onClick={() => { setAppliedActivityFilter(activityFilter); setActivitySnapshot(new Date().toISOString()) }} disabled={!canViewUserActivity || activityLoading || !!activityValidation}>
                    {activityLoading ? 'Applying...' : 'Apply Filters'}
                  </button>
                </div>

                <div className="access-table-toolbar compact">
                  <label>
                    <span>Table Search</span>
                    <input value={activitySearch} onChange={event => setActivitySearch(event.target.value)} placeholder="actor, action, resource, details" />
                  </label>
                </div>

                {(activityError || activityMessage) && (
                  <div className={`access-alert ${activityError ? 'error' : 'success'}`} role="status" aria-live="polite">
                    {activityError || activityMessage}
                    {activityLoadFailed && (
                      <button className="btn btn-secondary btn-sm" type="button" onClick={() => { void loadActivity() }} disabled={activityLoading}>
                        Retry activity
                      </button>
                    )}
                  </div>
                )}

                {activityValidation && <p role="alert">{activityValidation}</p>}
                <p className="access-query-summary">History snapshot: {formatTimestamp(activitySnapshot)}. Filters apply when you choose Apply Filters. Table search and sorting cover the loaded events; export includes all events matching the applied filters.</p>
                <AuditEventTable paginated events={filteredActivityEvents} footer={<LoadOlderEvents loaded={activityEvents.length} hasMore={activityHasMore && !activityLoadFailed} loading={activityLoading || activityOlderLoading} error={activityOlderError} onLoad={() => { void loadOlderActivity() }} />} selectedId={activityInspectorCollapsed ? undefined : selectedActivityID} onSelect={selectActivity} scopeLabel={event => eventScopeLabel(event, projects, domains)} header={<>
                        <SortHeader label="Time" field="time" sort={activitySort} onSort={field => setActivitySort(prev => nextSort(prev, field, activitySortDefaults))} />
                        <SortHeader label="Actor" field="actor" sort={activitySort} onSort={field => setActivitySort(prev => nextSort(prev, field, activitySortDefaults))} />
                        <SortHeader label="Action" field="action" sort={activitySort} onSort={field => setActivitySort(prev => nextSort(prev, field, activitySortDefaults))} />
                        <SortHeader label="Resource" field="resource" sort={activitySort} onSort={field => setActivitySort(prev => nextSort(prev, field, activitySortDefaults))} />
                        <SortHeader label="Scope" field="scope" sort={activitySort} onSort={field => setActivitySort(prev => nextSort(prev, field, activitySortDefaults))} />
                        <SortHeader label="Status" field="status" sort={activitySort} onSort={field => setActivitySort(prev => nextSort(prev, field, activitySortDefaults))} />
                        <SortHeader label="Details" field="details" sort={activitySort} onSort={field => setActivitySort(prev => nextSort(prev, field, activitySortDefaults))} /></>} />
              </>
            )}

            {activitySubTab === 'usage' && <AccessUsagePanel key={`${activeDomain?.id || ""}:${activeProject?.id || ""}`} projects={projects} domains={domains} activeProject={activeProject} activeDomain={activeDomain} />}

          </article>
          {activitySubTab === 'events' && renderActivityInspector()}
        </section>
      )}

      {issuedKey && (
        <AccessIssuedKeyModal
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
    </div>
  )
}
