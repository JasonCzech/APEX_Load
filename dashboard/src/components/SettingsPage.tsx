import { useCallback, useEffect, useMemo, useState, type Dispatch, type DragEvent, type SetStateAction } from 'react'
import { type DashboardTheme } from '../hooks/useDashboardTheme'
import { useApplicationApi } from '../hooks/api/useApplicationApi'
import { useProjectApi } from '../hooks/api/useProjectApi'
import { useRepositoryApi } from '../hooks/api/useRepositoryApi'
import { useApplication } from '../hooks/useApplication'
import { useProject } from '../hooks/useProject'
import { useApi } from '../hooks/useApi'
import { ArchiveRetentionPlanResponse, CapacityReservation, DashboardAuditEvent, DashboardAuthAccessResource, DashboardAuthDiagnostics, DashboardAuthDiagnosticsCheck, DashboardAuthEffectiveAccessResource, DashboardAuthRoleDefinition, DashboardAuthStatus, DashboardServiceAccount, DashboardSystemSettings, Domain, GitHubProjectPolicy, HealthResponse, Project, SharedDataPool, SharedDataPoolRow, SystemReadinessResponse, UsageAccountingResponse, UsageAccountingSnapshot } from '../types'
import { isEffectiveOperationAllowed } from '../utils/authAccessUtils'
import { readStoredString, removeStoredItem, writeStoredJSON } from '../utils/browserStorage'
import { getErrorMessage } from '../utils/errorUtils'
import {
  CAPACITY_SETTINGS_SUBSECTIONS,
  GENERAL_SETTINGS_SUBSECTIONS,
  INTEGRATION_SETTINGS_SUBSECTIONS,
  isCapacitySettingsSubsectionId,
  isIntegrationSettingsSubsectionId,
  isProjectSettingsSubsectionId,
  mergeSettingsOrder,
  moveSettingsSection,
  PROJECT_SETTINGS_SUBSECTIONS,
  readSettingsSectionArray,
  readSettingsSubsectionId,
  SETTINGS_SECTIONS,
  type CapacitySettingsSubsectionId,
  type GeneralSettingsSubsectionId,
  type IntegrationSettingsSubsectionId,
  type ProjectSettingsSubsectionId,
  type SettingsSectionId,
} from './settingsSectionLayout'
import EntitlementsPanel from './EntitlementsPanel'
import EntitlementHeadroom from './EntitlementHeadroom'
import TimeslotsPanel from './TimeslotsPanel'
import {
  formatAccessOperation,
  formatAuditActor,
  formatAuditTimestamp,
  formatAuthDiagnosticStatus,
  formatAuthDiagnosticsHeadline,
  formatAuthMode,
  formatAuthRoleList,
  formatAuthScopes,
  formatAuthStatusSummary,
  formatBytes,
  formatDateTime,
  formatDurationSeconds,
  formatGrantSource,
  formatHeaderContractValue,
  formatProjectRoleAssignments,
  formatReadinessCheckName,
  formatReadinessDetails,
  formatReadinessStatus,
  formatRoleScope,
  formatSchedulePolicyLabel,
  formatStatusCounts,
  fromDatetimeLocal,
  normalizeProjectRedactionRules,
  parseAuthScopes,
  parseOptionalNumber,
  readinessTone,
  sortProjects,
  toDatetimeLocal,
  toDraftRedactionRules,
  type DraftRedactionRule,
} from './settingsPageFormatters'
import {
  applyLiveGeneratorPresetToSettings,
  buildProjectSettingsDraft,
  DEFAULT_SETTINGS,
  deriveLiveGeneratorPreset,
  GENERATOR_PRESETS,
  normalizeDashboardSystemSettings,
  OIDC_PROVIDER_PRESETS,
  splitListDraft,
  usesDefaultGeneratorCapacity,
  validateOptionalPositiveInteger,
  validateProjectDraft,
  validateProjectQuotaField,
  validateSettings,
} from './settingsPageModel'
import './CreateTestModal.css'
import { downloadBlob } from '../utils/fileUtils'
import LibraryFederationPanel from './LibraryFederationPanel'
import GitHubIntegrationPanel from './GitHubIntegrationPanel'
import PageViewToggle from './PageViewToggle'
import { useTopbarContribution } from '../hooks/TopbarContributionProvider'

const SETTINGS_SECTION_TOPBAR_LABELS: Record<SettingsSectionId, string> = {
  general: 'General',
  thresholds: 'Thresholds',
  capacity: 'Capacity',
  domains: 'Domains',
  applications: 'Apps & Releases',
  projects: 'Projects',
  'data-pools': 'Data Pools',
  integrations: 'Integrations',
}

const SETTINGS_SECTION_TOPBAR_OPTIONS = SETTINGS_SECTIONS.map(section => ({
  value: section.id,
  label: SETTINGS_SECTION_TOPBAR_LABELS[section.id],
  ariaLabel: section.title,
  title: section.title,
}))

const NOTIFICATION_FORMAT_OPTIONS = [
  { value: 'slack', label: 'Slack Block Kit' },
  { value: 'teams', label: 'Microsoft Teams Adaptive Card' },
  { value: 'dynatrace', label: 'Dynatrace Events API v2' },
] as const


interface SettingsPageProps {
  theme: DashboardTheme
  onThemeChange: (theme: DashboardTheme) => void
  authStatus?: DashboardAuthStatus
  onNavigate?: (page: 'access') => void
  domains?: Domain[]
  activeDomain?: Domain | null
  onDomainChange?: (domain: Domain) => void
  onDomainsRefresh?: (preferredId?: string) => Promise<Domain[]>
}

type SettingsSubsectionDefinition<SubsectionId extends string> = {
  id: SubsectionId
  title: string
}

export default function SettingsPage({
  theme,
  onThemeChange,
  authStatus,
  onNavigate,
  domains = [],
  activeDomain,
  onDomainChange,
  onDomainsRefresh,
}: SettingsPageProps) {
  const api = useApi()
  const applicationApi = useApplicationApi()
  const projectApi = useProjectApi()
  const repositoryApi = useRepositoryApi()
  const {
    applications,
    releases,
    activeApplication,
    activeRelease,
    setActiveApplication,
    setActiveRelease,
    refreshApplications,
    refreshReleases,
    createAndSelectApplication,
  } = useApplication()
  const { activeProject } = useProject()
  const [settings, setSettings] = useState<DashboardSystemSettings>(DEFAULT_SETTINGS)
  const [savedSettings, setSavedSettings] = useState<DashboardSystemSettings>(DEFAULT_SETTINGS)
  const [health, setHealth] = useState<HealthResponse | null>(null)
  const [authAccessResources, setAuthAccessResources] = useState<DashboardAuthAccessResource[]>([])
  const [authDiagnostics, setAuthDiagnostics] = useState<DashboardAuthDiagnostics | null>(null)
  const [effectiveAuthAccessResources, setEffectiveAuthAccessResources] = useState<DashboardAuthEffectiveAccessResource[]>([])
  const [authRoles, setAuthRoles] = useState<DashboardAuthRoleDefinition[]>([])
  const [serviceAccounts, setServiceAccounts] = useState<DashboardServiceAccount[]>([])
  const [auditEvents, setAuditEvents] = useState<DashboardAuditEvent[]>([])
  const [auditActionMessage, setAuditActionMessage] = useState('')
  const [auditActionError, setAuditActionError] = useState('')
  const [auditActionFilter, setAuditActionFilter] = useState('')
  const [auditActorTypeFilter, setAuditActorTypeFilter] = useState('')
  const [auditStatusFilter, setAuditStatusFilter] = useState('')
  const [loading, setLoading] = useState(true)
  const [saveMessage, setSaveMessage] = useState('')
  const [requestError, setRequestError] = useState('')
  const [activeSection, setActiveSection] = useState<SettingsSectionId>('general')
  const [activeGeneralSubsection, setActiveGeneralSubsection] = useState<GeneralSettingsSubsectionId>(() => readSettingsSubsectionId('apexload.settings.generalSubsection'))
  const [activeCapacitySubsection, setActiveCapacitySubsection] = useState<CapacitySettingsSubsectionId>(() => readSettingsSubsectionId('apexload.settings.capacitySubsection', 'availability', isCapacitySettingsSubsectionId))
  const [activeProjectSubsection, setActiveProjectSubsection] = useState<ProjectSettingsSubsectionId>(() => readSettingsSubsectionId('apexload.settings.projectSubsection', 'catalog', isProjectSettingsSubsectionId))
  const [activeIntegrationSubsection, setActiveIntegrationSubsection] = useState<IntegrationSettingsSubsectionId>(() => readSettingsSubsectionId('apexload.settings.integrationSubsection', 'library', isIntegrationSettingsSubsectionId))
  const [confirmingResetAll, setConfirmingResetAll] = useState(false)
  const [customizingSections] = useState(false)
  const [draggedSectionId, setDraggedSectionId] = useState<SettingsSectionId | null>(null)
  const [sectionOrder, setSectionOrder] = useState<SettingsSectionId[]>(() => readSettingsSectionArray('apexload.settings.sectionOrder'))
  const [selectedDomainId, setSelectedDomainId] = useState(activeDomain?.id || domains[0]?.id || '')
  const [newDomainName, setNewDomainName] = useState('')
  const [newDomainDescription, setNewDomainDescription] = useState('')
  const [domainDraftName, setDomainDraftName] = useState('')
  const [domainDraftDescription, setDomainDraftDescription] = useState('')
  const [domainActionError, setDomainActionError] = useState('')
  const [domainActionMessage, setDomainActionMessage] = useState('')
  const [confirmDeleteDomain, setConfirmDeleteDomain] = useState<Domain | null>(null)
  const [managedReleases, setManagedReleases] = useState(releases)
  const [newApplicationName, setNewApplicationName] = useState('')
  const [applicationActionError, setApplicationActionError] = useState('')
  const [renamingApplicationName, setRenamingApplicationName] = useState<string | null>(null)
  const [renamingReleaseId, setRenamingReleaseId] = useState<string | null>(null)
  const [renamingReleaseName, setRenamingReleaseName] = useState('')
  const [confirmDeleteApp, setConfirmDeleteApp] = useState(false)
  const [confirmDeleteRelease, setConfirmDeleteRelease] = useState<{ id: string; name: string } | null>(null)
  const [confirmArchiveRelease, setConfirmArchiveRelease] = useState<{ id: string; name: string } | null>(null)
  const [confirmApplyRetention, setConfirmApplyRetention] = useState(false)
  const [confirmDeletePool, setConfirmDeletePool] = useState<SharedDataPool | null>(null)
  const [confirmDeleteRow, setConfirmDeleteRow] = useState<SharedDataPoolRow | null>(null)
  const [projects, setProjects] = useState<Project[]>([])
  const [selectedProjectId, setSelectedProjectId] = useState('')
  const [newProjectName, setNewProjectName] = useState('')
  const [projectDraftName, setProjectDraftName] = useState('')
  const [projectDraftRules, setProjectDraftRules] = useState<DraftRedactionRule[]>([])
  const [projectDraftMaxConcurrentTests, setProjectDraftMaxConcurrentTests] = useState('')
  const [projectDraftMaxVUsers, setProjectDraftMaxVUsers] = useState('')
  const [projectDraftMaxVUsersPerTest, setProjectDraftMaxVUsersPerTest] = useState('')
  const [projectDraftMaxTestDurationMinutes, setProjectDraftMaxTestDurationMinutes] = useState('')
  const [projectDraftGitHubEnabled, setProjectDraftGitHubEnabled] = useState(false)
  const [projectDraftGitHubAllowedRepos, setProjectDraftGitHubAllowedRepos] = useState('')
  const [projectDraftGitHubAllowedBranches, setProjectDraftGitHubAllowedBranches] = useState('')
  const [projectDraftGitHubPathTemplate, setProjectDraftGitHubPathTemplate] = useState('')
  const [projectDraftGitHubRequirePR, setProjectDraftGitHubRequirePR] = useState(true)
  const [projectDraftGitHubAllowDirect, setProjectDraftGitHubAllowDirect] = useState(false)
  const [projectDraftGitHubAllowImport, setProjectDraftGitHubAllowImport] = useState(true)
  const [projectDraftGitHubAllowPush, setProjectDraftGitHubAllowPush] = useState(true)
  const [projectDraftGitHubBulkSync, setProjectDraftGitHubBulkSync] = useState(false)
  const [projectDraftGitHubAutoSync, setProjectDraftGitHubAutoSync] = useState(false)
  const [projectDraftGitHubChecks, setProjectDraftGitHubChecks] = useState(false)
  const [projectDraftGitHubAppInstallation, setProjectDraftGitHubAppInstallation] = useState('')
  const [projectDraftGitHubDefaultRepo, setProjectDraftGitHubDefaultRepo] = useState('')
  const [projectDraftGitHubDefaultBranch, setProjectDraftGitHubDefaultBranch] = useState('')
  const [projectDraftGitHubSyncDirectory, setProjectDraftGitHubSyncDirectory] = useState('')
  const [projectActionError, setProjectActionError] = useState('')
  const [projectActionMessage, setProjectActionMessage] = useState('')
  const [archiveRetentionPlan, setArchiveRetentionPlan] = useState<ArchiveRetentionPlanResponse | null>(null)
  const [archiveRetentionActionError, setArchiveRetentionActionError] = useState('')
  const [archiveRetentionActionMessage, setArchiveRetentionActionMessage] = useState('')
  const [usageAccounting, setUsageAccounting] = useState<UsageAccountingResponse | null>(null)
  const [usageSnapshots, setUsageSnapshots] = useState<UsageAccountingSnapshot[]>([])
  const [usageActionError, setUsageActionError] = useState('')
  const [usageActionMessage, setUsageActionMessage] = useState('')
  const [systemReadiness, setSystemReadiness] = useState<SystemReadinessResponse | null>(null)
  const [readinessActionError, setReadinessActionError] = useState('')
  const [readinessActionMessage, setReadinessActionMessage] = useState('')
  const [dataPools, setDataPools] = useState<SharedDataPool[]>([])
  const [selectedDataPoolId, setSelectedDataPoolId] = useState('')
  const [dataPoolRows, setDataPoolRows] = useState<SharedDataPoolRow[]>([])
  const [dataPoolName, setDataPoolName] = useState('')
  const [dataPoolDescription, setDataPoolDescription] = useState('')
  const [dataPoolRowJson, setDataPoolRowJson] = useState('{\n  "customer_id": "C-10001"\n}')
  const [editingDataPoolRowId, setEditingDataPoolRowId] = useState<string | null>(null)
  const [dataPoolActionError, setDataPoolActionError] = useState('')

  const loadProjectGitHubDraft = useCallback((policy?: GitHubProjectPolicy) => {
    setProjectDraftGitHubEnabled(Boolean(policy?.enabled))
    setProjectDraftGitHubAllowedRepos((policy?.allowed_repos || []).join(', '))
    setProjectDraftGitHubAllowedBranches((policy?.allowed_branches || []).join(', '))
    setProjectDraftGitHubPathTemplate(policy?.path_template || '')
    setProjectDraftGitHubRequirePR(policy?.require_pull_request ?? true)
    setProjectDraftGitHubAllowDirect(Boolean(policy?.allow_direct_commit))
    setProjectDraftGitHubAllowImport(policy?.allow_import ?? true)
    setProjectDraftGitHubAllowPush(policy?.allow_push ?? true)
    setProjectDraftGitHubBulkSync(Boolean(policy?.bulk_sync_enabled))
    setProjectDraftGitHubAutoSync(Boolean(policy?.auto_sync_enabled))
    setProjectDraftGitHubChecks(Boolean(policy?.checks_enabled))
    setProjectDraftGitHubAppInstallation(policy?.app_installation_id ? String(policy.app_installation_id) : '')
    setProjectDraftGitHubDefaultRepo(policy?.default_repo || '')
    setProjectDraftGitHubDefaultBranch(policy?.default_branch || '')
    setProjectDraftGitHubSyncDirectory(policy?.default_sync_directory || '')
  }, [])

  const buildProjectGitHubDraft = useCallback((): GitHubProjectPolicy => ({
    enabled: projectDraftGitHubEnabled,
    allowed_repos: splitListDraft(projectDraftGitHubAllowedRepos),
    allowed_branches: splitListDraft(projectDraftGitHubAllowedBranches),
    path_template: projectDraftGitHubPathTemplate.trim(),
    require_pull_request: projectDraftGitHubRequirePR,
    allow_direct_commit: projectDraftGitHubAllowDirect,
    allow_import: projectDraftGitHubAllowImport,
    allow_push: projectDraftGitHubAllowPush,
    bulk_sync_enabled: projectDraftGitHubBulkSync,
    auto_sync_enabled: projectDraftGitHubAutoSync,
    checks_enabled: projectDraftGitHubChecks,
    app_installation_id: projectDraftGitHubAppInstallation.trim() ? Number(projectDraftGitHubAppInstallation) : undefined,
    default_repo: projectDraftGitHubDefaultRepo.trim(),
    default_branch: projectDraftGitHubDefaultBranch.trim(),
    default_sync_directory: projectDraftGitHubSyncDirectory.trim(),
  }), [
    projectDraftGitHubAllowDirect,
    projectDraftGitHubAllowImport,
    projectDraftGitHubAllowPush,
    projectDraftGitHubAllowedBranches,
    projectDraftGitHubAllowedRepos,
    projectDraftGitHubAppInstallation,
    projectDraftGitHubAutoSync,
    projectDraftGitHubBulkSync,
    projectDraftGitHubChecks,
    projectDraftGitHubDefaultBranch,
    projectDraftGitHubDefaultRepo,
    projectDraftGitHubEnabled,
    projectDraftGitHubPathTemplate,
    projectDraftGitHubRequirePR,
    projectDraftGitHubSyncDirectory,
  ])

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      setLoading(true)
      setRequestError('')
      try {
        const response = normalizeDashboardSystemSettings(await api.getSystemSettings())
        let resolvedSettings = response
        if (usesDefaultGeneratorCapacity(response)) {
          try {
            const inventory = await api.getSystemInventory()
            resolvedSettings = applyLiveGeneratorPresetToSettings(response, deriveLiveGeneratorPreset(inventory))
          } catch {
            resolvedSettings = response
          }
        }
        if (!cancelled) {
          setSettings(resolvedSettings)
          setSavedSettings(resolvedSettings)
        }
      } catch (error: unknown) {
        if (!cancelled) {
          setRequestError(getErrorMessage(error, 'Unable to load system settings'))
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
    const loadEffectiveAccess = async () => {
      try {
        const response = await api.getAuthEffectiveAccess(activeProject?.id)
        if (!cancelled) {
          setEffectiveAuthAccessResources(response.resources || [])
        }
      } catch {
        if (!cancelled) {
          setEffectiveAuthAccessResources([])
        }
      }
    }

    loadEffectiveAccess()
    return () => {
      cancelled = true
    }
  }, [activeProject?.id, api])

  useEffect(() => {
  let cancelled = false
  const loadAuthDiagnostics = async () => {
    try {
      const response = await api.getAuthDiagnostics()
      if (!cancelled) {
        setAuthDiagnostics(response)
      }
    } catch {
      if (!cancelled) {
        setAuthDiagnostics(null)
      }
    }
  }

  loadAuthDiagnostics()
  return () => {
    cancelled = true
  }
  }, [api])

  useEffect(() => {
  let cancelled = false
  const loadAuthAccessMatrix = async () => {
    try {
      const response = await api.getAuthAccessMatrix()
      if (!cancelled) {
        setAuthAccessResources(response.resources || [])
      }
    } catch {
      if (!cancelled) {
        setAuthAccessResources([])
      }
    }
  }

  loadAuthAccessMatrix()
  return () => {
    cancelled = true
  }
  }, [api])

  useEffect(() => {
    let cancelled = false
    const loadAuthRoleCatalog = async () => {
      try {
        const response = await api.getAuthRoleCatalog()
        if (!cancelled) {
          setAuthRoles(response.roles || [])
        }
      } catch {
        if (!cancelled) {
          setAuthRoles([])
        }
      }
    }

    loadAuthRoleCatalog()
    return () => {
      cancelled = true
    }
  }, [api])

  useEffect(() => {
    let cancelled = false
    const loadServiceAccounts = async () => {
      try {
        const response = await api.listAuthServiceAccounts()
        if (!cancelled) {
          setServiceAccounts(response.service_accounts || [])
        }
      } catch {
        if (!cancelled) {
          setServiceAccounts([])
        }
      }
    }

    loadServiceAccounts()
    return () => {
      cancelled = true
    }
  }, [api])

  useEffect(() => {
    let cancelled = false
    const loadAuditEvents = async () => {
      try {
        const response = await api.listAuthAuditEvents({
          limit: 20,
          action: auditActionFilter.trim() || undefined,
          actor_type: auditActorTypeFilter.trim() || undefined,
          status: auditStatusFilter.trim() || undefined,
        })
        if (!cancelled) {
          setAuditEvents(response.events || [])
        }
      } catch {
        if (!cancelled) {
          setAuditEvents([])
        }
      }
    }

    loadAuditEvents()
    return () => {
      cancelled = true
    }
  }, [api, auditActionFilter, auditActorTypeFilter, auditStatusFilter])

  useEffect(() => {
    let cancelled = false
    const loadUsage = async () => {
      try {
        const [usageResponse, snapshotResponse] = await Promise.all([
          api.getUsageAccounting(activeProject?.id),
          api.listUsageAccountingSnapshots(activeProject?.id),
        ])
        if (!cancelled) {
          setUsageAccounting(usageResponse)
          setUsageSnapshots(snapshotResponse.snapshots || [])
        }
      } catch {
        if (!cancelled) {
          setUsageAccounting(null)
          setUsageSnapshots([])
        }
      }
    }

    loadUsage()
    return () => {
      cancelled = true
    }
  }, [activeProject?.id, api])

  useEffect(() => {
    let cancelled = false
    const loadReadiness = async () => {
      try {
        const response = await api.getSystemReadiness()
        if (!cancelled) {
          setSystemReadiness(response)
        }
      } catch {
        if (!cancelled) {
          setSystemReadiness(null)
        }
      }
    }

    loadReadiness()
    return () => {
      cancelled = true
    }
  }, [api])

  useEffect(() => {
    let cancelled = false
    const loadHealth = async () => {
      if (typeof document !== 'undefined' && document.visibilityState !== 'visible') {
        return
      }
      try {
        const response = await api.getHealth()
        if (!cancelled) setHealth(response)
      } catch {
        if (!cancelled) setHealth(null)
      }
    }

    loadHealth()
    const interval = setInterval(loadHealth, 5000)

    const handleVisibilityChange = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'visible') {
        loadHealth()
      }
    }

    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', handleVisibilityChange)
    }

    return () => {
      cancelled = true
      clearInterval(interval)
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', handleVisibilityChange)
      }
    }
  }, [api])

  const validationErrors = useMemo(() => validateSettings(settings), [settings])
  const hasUnsavedChanges = useMemo(() => JSON.stringify(settings) !== JSON.stringify(savedSettings), [settings, savedSettings])
  const authDiagnosticsStale = useMemo(() => JSON.stringify(settings.auth) !== JSON.stringify(savedSettings.auth), [settings.auth, savedSettings.auth])
  const canManageSystem = useMemo(() => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_system'), [effectiveAuthAccessResources])
  const canManageDomains = useMemo(() => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_domains', false), [effectiveAuthAccessResources])
  const canManageProjects = useMemo(() => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_projects'), [effectiveAuthAccessResources])
  const canManageServiceAccounts = useMemo(() => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_service_accounts'), [effectiveAuthAccessResources])
  const canViewAudit = useMemo(() => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'view_audit'), [effectiveAuthAccessResources])
  const canSave = validationErrors.length === 0 && !loading && hasUnsavedChanges && canManageSystem
  const selectedDomain = useMemo(
    () => domains.find(domain => domain.id === selectedDomainId) || domains.find(domain => domain.id === activeDomain?.id) || domains[0] || null,
    [activeDomain?.id, domains, selectedDomainId],
  )
  const projectCountByDomain = useMemo(() => {
    const counts = new Map<string, number>()
    projects.forEach(project => {
      const domainID = project.domain_id || 'default'
      counts.set(domainID, (counts.get(domainID) || 0) + 1)
    })
    return counts
  }, [projects])
  const selectedDomainProjectCount = selectedDomain ? projectCountByDomain.get(selectedDomain.id) || 0 : 0
  const domainDraftValidationError = selectedDomain && !domainDraftName.trim() ? 'Domain name is required.' : ''
  const domainHasUnsavedChanges = Boolean(
    selectedDomain
    && (
      domainDraftName.trim() !== selectedDomain.name
      || domainDraftDescription.trim() !== (selectedDomain.description || '')
    ),
  )
  const selectedProject = useMemo(
    () => projects.find(project => project.id === selectedProjectId) || null,
    [projects, selectedProjectId],
  )
  const projectDraftValidationError = useMemo(
    () => {
      if (!selectedProject) return ''
      return (
        validateProjectDraft(projectDraftName, projectDraftRules) ||
        validateProjectQuotaField('Project max concurrent tests', projectDraftMaxConcurrentTests) ||
        validateProjectQuotaField('Project max VUsers', projectDraftMaxVUsers) ||
        validateProjectQuotaField('Project max VUsers per test', projectDraftMaxVUsersPerTest) ||
        validateProjectQuotaField('Project max test duration (minutes)', projectDraftMaxTestDurationMinutes) ||
        validateOptionalPositiveInteger('GitHub App installation ID', projectDraftGitHubAppInstallation)
      )
    },
    [projectDraftGitHubAppInstallation, projectDraftMaxConcurrentTests, projectDraftMaxVUsers, projectDraftMaxVUsersPerTest, projectDraftMaxTestDurationMinutes, projectDraftName, projectDraftRules, selectedProject],
  )
  const projectHasUnsavedChanges = useMemo(() => {
    if (!selectedProject) {
      return false
    }

    const current = {
      name: selectedProject.name,
      settings: buildProjectSettingsDraft(
        toDraftRedactionRules(selectedProject.settings?.redaction_rules),
        selectedProject.settings?.max_concurrent_tests !== undefined ? String(selectedProject.settings.max_concurrent_tests) : '',
        selectedProject.settings?.max_vusers !== undefined ? String(selectedProject.settings.max_vusers) : '',
        selectedProject.settings?.max_vusers_per_test !== undefined ? String(selectedProject.settings.max_vusers_per_test) : '',
        selectedProject.settings?.max_test_duration_minutes !== undefined ? String(selectedProject.settings.max_test_duration_minutes) : '',
        selectedProject.settings?.github,
      ),
    }
    const draft = {
      name: projectDraftName.trim(),
      settings: buildProjectSettingsDraft(projectDraftRules, projectDraftMaxConcurrentTests, projectDraftMaxVUsers, projectDraftMaxVUsersPerTest, projectDraftMaxTestDurationMinutes, buildProjectGitHubDraft()),
    }

    return JSON.stringify(current) !== JSON.stringify(draft)
  }, [
    buildProjectGitHubDraft,
    projectDraftMaxConcurrentTests,
    projectDraftMaxTestDurationMinutes,
    projectDraftMaxVUsers,
    projectDraftMaxVUsersPerTest,
    projectDraftName,
    projectDraftRules,
    selectedProject,
  ])
  const customRuleCount = useMemo(
    () => normalizeProjectRedactionRules(projectDraftRules).length,
    [projectDraftRules],
  )
  const availabilitySnapshot = health?.capacity_availability
  const notificationWebhookCount = settings.notifications.webhooks.length
  const availabilitySourceLabel = availabilitySnapshot?.source === 'configured_plus_live'
    ? availabilitySnapshot.live_available_generators !== undefined
      ? 'Live snapshot + configured ceiling'
      : 'Configured ceiling only'
    : 'Configured inventory only'
  const generalSubsectionSummaries = useMemo<Record<GeneralSettingsSubsectionId, string>>(() => ({
    platform: `${settings.general.max_concurrent_tests} tests`,
    usage: usageAccounting ? `${usageAccounting.totals.run_count} runs` : 'No usage yet',
    entitlements: 'Edition & limits',
    readiness: formatReadinessStatus(systemReadiness?.status),
    authentication: formatAuthMode(settings.auth.mode),
    governance: `${auditEvents.length} audit event${auditEvents.length === 1 ? '' : 's'}`,
  }), [auditEvents.length, settings.auth.mode, settings.general.max_concurrent_tests, systemReadiness?.status, usageAccounting])
  const capacitySubsectionSummaries = useMemo<Record<CapacitySettingsSubsectionId, string>>(() => ({
    availability: `${availabilitySnapshot?.effective_available_generators ?? settings.capacity_planning.availability.configured_available_generators} available`,
    generator: `${settings.capacity_planning.generator.cpu_cores} CPU / ${settings.capacity_planning.generator.memory_mb} MB`,
    reservations: `${settings.capacity_planning.reservations?.length ?? 0} reserved`,
    timeslots: 'Forward-booked windows',
    retention: settings.archive_retention.enabled ? `${settings.archive_retention.default_retention_days} days` : 'Disabled',
  }), [
    availabilitySnapshot?.effective_available_generators,
    settings.archive_retention.default_retention_days,
    settings.archive_retention.enabled,
    settings.capacity_planning.availability.configured_available_generators,
    settings.capacity_planning.generator.cpu_cores,
    settings.capacity_planning.generator.memory_mb,
    settings.capacity_planning.reservations?.length,
  ])
  const projectSubsectionSummaries = useMemo<Record<ProjectSettingsSubsectionId, string>>(() => ({
    catalog: `${projects.length} project${projects.length === 1 ? '' : 's'}`,
    limits: selectedProject ? selectedProject.name : 'Select project',
    github: projectDraftGitHubEnabled ? 'Enabled' : 'Disabled',
    redaction: `${customRuleCount} rule${customRuleCount === 1 ? '' : 's'}`,
  }), [customRuleCount, projectDraftGitHubEnabled, projects.length, selectedProject])
  const integrationSubsectionSummaries = useMemo<Record<IntegrationSettingsSubsectionId, string>>(() => ({
    library: activeProject?.name || 'Project scoped',
    github: settings.github.enabled ? 'Enabled' : 'Disabled',
    prometheus: settings.prometheus.enabled ? 'Enabled' : 'Disabled',
    notifications: `${notificationWebhookCount} webhook${notificationWebhookCount === 1 ? '' : 's'}`,
  }), [activeProject?.name, notificationWebhookCount, settings.github.enabled, settings.prometheus.enabled])

  useEffect(() => {
    setSectionOrder(prev => mergeSettingsOrder(prev))
  }, [])

  useEffect(() => {
    const targetSection = readStoredString('apexload.settings.focusSection') as SettingsSectionId | ''
    if (!targetSection) return
    removeStoredItem('apexload.settings.focusSection')
    if (!SETTINGS_SECTIONS.some(section => section.id === targetSection)) return
    setActiveSection(targetSection)
    window.setTimeout(() => {
      document.getElementById(`settings-section-${targetSection}`)?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
    }, 150)
  }, [])

  useEffect(() => {
    let cancelled = false
    const loadManagedReleases = async () => {
      if (!activeApplication?.id) {
        setManagedReleases([])
        return
      }
      try {
        const response = await applicationApi.listReleases(activeApplication.id, true)
        if (!cancelled) setManagedReleases(response.releases || [])
      } catch {
        if (!cancelled) setManagedReleases(releases)
      }
    }
    loadManagedReleases()
    return () => {
      cancelled = true
    }
  }, [activeApplication?.id, applicationApi, releases])

  useEffect(() => {
    let cancelled = false
    const loadProjects = async () => {
      try {
        const response = await projectApi.listProjects()
        if (cancelled) {
          return
        }

        const nextProjects = sortProjects(response.projects || [])
        const nextSelected = nextProjects[0] || null
        setProjects(nextProjects)
        setSelectedProjectId(nextSelected?.id || '')
        setProjectDraftName(nextSelected?.name || '')
        setProjectDraftRules(toDraftRedactionRules(nextSelected?.settings?.redaction_rules))
        setProjectDraftMaxConcurrentTests(nextSelected?.settings?.max_concurrent_tests !== undefined ? String(nextSelected.settings.max_concurrent_tests) : '')
        setProjectDraftMaxVUsers(nextSelected?.settings?.max_vusers !== undefined ? String(nextSelected.settings.max_vusers) : '')
        setProjectDraftMaxVUsersPerTest(nextSelected?.settings?.max_vusers_per_test !== undefined ? String(nextSelected.settings.max_vusers_per_test) : '')
        setProjectDraftMaxTestDurationMinutes(nextSelected?.settings?.max_test_duration_minutes !== undefined ? String(nextSelected.settings.max_test_duration_minutes) : '')
        loadProjectGitHubDraft(nextSelected?.settings?.github)
        setProjectActionError('')
      } catch (error: unknown) {
        if (!cancelled) {
          setProjectActionError(getErrorMessage(error, 'Unable to load projects'))
        }
      }
    }

    loadProjects()
    return () => {
      cancelled = true
    }
  }, [loadProjectGitHubDraft, projectApi])

  useEffect(() => {
    if (domains.length === 0) {
      setSelectedDomainId('')
      return
    }
    setSelectedDomainId(prev => {
      if (activeDomain?.id && domains.some(domain => domain.id === activeDomain.id)) {
        return activeDomain.id
      }
      if (prev && domains.some(domain => domain.id === prev)) {
        return prev
      }
      return domains[0].id
    })
  }, [activeDomain?.id, domains])

  useEffect(() => {
    setDomainDraftName(selectedDomain?.name || '')
    setDomainDraftDescription(selectedDomain?.description || '')
  }, [selectedDomain?.description, selectedDomain?.id, selectedDomain?.name])

  useEffect(() => {
    writeStoredJSON('apexload.settings.sectionOrder', sectionOrder)
  }, [sectionOrder])

  useEffect(() => {
    writeStoredJSON('apexload.settings.generalSubsection', activeGeneralSubsection)
  }, [activeGeneralSubsection])

  useEffect(() => {
    writeStoredJSON('apexload.settings.capacitySubsection', activeCapacitySubsection)
  }, [activeCapacitySubsection])

  useEffect(() => {
    writeStoredJSON('apexload.settings.projectSubsection', activeProjectSubsection)
  }, [activeProjectSubsection])

  useEffect(() => {
    writeStoredJSON('apexload.settings.integrationSubsection', activeIntegrationSubsection)
  }, [activeIntegrationSubsection])

  const loadDataPools = async (preferredId?: string) => {
    try {
      const response = await api.listDataPools(activeProject?.id)
      const pools: SharedDataPool[] = response.data_pools || []
      setDataPools(pools)
      const nextSelected = preferredId || selectedDataPoolId || pools[0]?.id || ''
      setSelectedDataPoolId(nextSelected)
      if (nextSelected) {
        const details = await api.getDataPool(nextSelected)
        setDataPoolRows(details.rows || [])
      } else {
        setDataPoolRows([])
      }
      setDataPoolActionError('')
    } catch (error: unknown) {
      setDataPoolActionError(getErrorMessage(error, 'Unable to load shared data pools'))
    }
  }

  useEffect(() => {
    loadDataPools()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeProject?.id])

  useEffect(() => {
    let cancelled = false
    const loadRows = async () => {
      if (!selectedDataPoolId) {
        setDataPoolRows([])
        return
      }
      try {
        const response = await api.getDataPool(selectedDataPoolId)
        if (!cancelled) setDataPoolRows(response.rows || [])
      } catch (error: unknown) {
        if (!cancelled) setDataPoolActionError(getErrorMessage(error, 'Unable to load data pool rows'))
      }
    }
    loadRows()
    return () => {
      cancelled = true
    }
  }, [api, selectedDataPoolId])

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

  const updateClientApp = (availability: DashboardSystemSettings['client_app']['availability']) => {
    setSettings((prev) => ({
      ...prev,
      client_app: {
        ...prev.client_app,
        availability,
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updateClientDownloads = (enabled: boolean) => {
    setSettings((prev) => ({
      ...prev,
      client_downloads: {
        ...prev.client_downloads,
        enabled,
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

  const updateScheduleDefaults = (key: keyof DashboardSystemSettings['schedule_defaults'], value: string) => {
    setSettings((prev) => ({
      ...prev,
      schedule_defaults: {
        ...prev.schedule_defaults,
        [key]: value,
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const addReservation = () => {
    setSettings((prev) => ({
      ...prev,
      capacity_planning: {
        ...prev.capacity_planning,
        reservations: [...(prev.capacity_planning.reservations ?? []), { project_id: '', reserved_generators: 1 }],
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updateReservation = (index: number, patch: Partial<CapacityReservation>) => {
    setSettings((prev) => {
      const reservations = [...(prev.capacity_planning.reservations ?? [])]
      const existing = reservations[index]
      if (!existing) return prev
      const next: CapacityReservation = { ...existing, ...patch }
      // Empty datetime inputs clear the window bound entirely.
      if (patch.starts_at === '') delete next.starts_at
      if (patch.ends_at === '') delete next.ends_at
      reservations[index] = next
      return { ...prev, capacity_planning: { ...prev.capacity_planning, reservations } }
    })
    setSaveMessage('')
    setRequestError('')
  }

  const removeReservation = (index: number) => {
    setSettings((prev) => ({
      ...prev,
      capacity_planning: {
        ...prev.capacity_planning,
        reservations: (prev.capacity_planning.reservations ?? []).filter((_, i) => i !== index),
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updateArchiveRetention = (key: keyof DashboardSystemSettings['archive_retention'], value: boolean | string) => {
    setSettings((prev) => ({
      ...prev,
      archive_retention: {
        ...prev.archive_retention,
        [key]: key === 'enabled' ? Boolean(value) : Math.max(1, Number(value) || 1),
      },
    }))
    setArchiveRetentionActionMessage('')
    setArchiveRetentionActionError('')
    setSaveMessage('')
    setRequestError('')
  }

  const planArchiveRetention = async () => {
    if (!canManageSystem) {
      setArchiveRetentionActionError('Current session cannot manage archive retention.')
      setArchiveRetentionActionMessage('')
      return
    }
    try {
      const plan = await api.planArchiveRetention()
      setArchiveRetentionPlan(plan)
      setArchiveRetentionActionMessage(`Retention plan found ${plan.summary.candidate_count} prune candidate${plan.summary.candidate_count === 1 ? '' : 's'}.`)
      setArchiveRetentionActionError('')
    } catch (error: unknown) {
      setArchiveRetentionActionError(getErrorMessage(error, 'Unable to plan archive retention'))
      setArchiveRetentionActionMessage('')
    }
  }

  const applyArchiveRetention = async () => {
    if (!canManageSystem) {
      setArchiveRetentionActionError('Current session cannot manage archive retention.')
      setArchiveRetentionActionMessage('')
      return
    }
    setConfirmApplyRetention(false)
    try {
      const plan = await api.applyArchiveRetention()
      setArchiveRetentionPlan(plan)
      setArchiveRetentionActionMessage(`Retention apply pruned ${plan.summary.pruned_count} archive${plan.summary.pruned_count === 1 ? '' : 's'}${plan.summary.failed_count ? ` with ${plan.summary.failed_count} failure${plan.summary.failed_count === 1 ? '' : 's'}` : ''}.`)
      setArchiveRetentionActionError('')
    } catch (error: unknown) {
      setArchiveRetentionActionError(getErrorMessage(error, 'Unable to apply archive retention'))
      setArchiveRetentionActionMessage('')
    }
  }

  const refreshUsageAccounting = async () => {
    try {
      const [usageResponse, snapshotResponse] = await Promise.all([
        api.getUsageAccounting(activeProject?.id),
        api.listUsageAccountingSnapshots(activeProject?.id),
      ])
      setUsageAccounting(usageResponse)
      setUsageSnapshots(snapshotResponse.snapshots || [])
      setUsageActionMessage('Usage accounting refreshed.')
      setUsageActionError('')
    } catch (error: unknown) {
      setUsageActionError(getErrorMessage(error, 'Unable to refresh usage accounting'))
      setUsageActionMessage('')
    }
  }

  const captureUsageSnapshot = async () => {
    if (!canManageSystem) {
      setUsageActionError('Current session cannot capture usage snapshots.')
      setUsageActionMessage('')
      return
    }
    try {
      const snapshot = await api.captureUsageAccountingSnapshot(activeProject?.id)
      const response = await api.listUsageAccountingSnapshots(activeProject?.id)
      setUsageSnapshots(response.snapshots || [snapshot])
      setUsageAccounting(snapshot.usage)
      setUsageActionMessage(`Usage snapshot captured for ${snapshot.usage.totals.run_count} run${snapshot.usage.totals.run_count === 1 ? '' : 's'}.`)
      setUsageActionError('')
    } catch (error: unknown) {
      setUsageActionError(getErrorMessage(error, 'Unable to capture usage snapshot'))
      setUsageActionMessage('')
    }
  }

  const exportUsageAccounting = async () => {
    try {
      const blob = await api.downloadUsageAccounting(activeProject?.id)
      const date = new Date().toISOString().slice(0, 10)
      downloadBlob(blob, `apexload-usage-accounting-${date}.csv`)
      setUsageActionMessage('Usage accounting export downloaded.')
      setUsageActionError('')
    } catch (error: unknown) {
      setUsageActionError(getErrorMessage(error, 'Unable to export usage accounting'))
      setUsageActionMessage('')
    }
  }

  const refreshSystemReadiness = async () => {
    try {
      const response = await api.getSystemReadiness()
      setSystemReadiness(response)
      setReadinessActionMessage('Production readiness refreshed.')
      setReadinessActionError('')
    } catch (error: unknown) {
      setReadinessActionError(getErrorMessage(error, 'Unable to refresh production readiness'))
      setReadinessActionMessage('')
    }
  }

  const updateAuth = (key: keyof DashboardSystemSettings['auth'], value: string | string[]) => {
    setSettings((prev) => ({
      ...prev,
      auth: {
        ...prev.auth,
        [key]: value,
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const exportAuditEvents = async () => {
    if (!canViewAudit) {
      setAuditActionError('Current session cannot export audit events.')
      setAuditActionMessage('')
      return
    }
    try {
      const blob = await api.downloadAuthAuditEvents({
        limit: 1000,
        action: auditActionFilter.trim() || undefined,
        actor_type: auditActorTypeFilter.trim() || undefined,
        status: auditStatusFilter.trim() || undefined,
      })
      const date = new Date().toISOString().slice(0, 10)
      downloadBlob(blob, `apexload-audit-events-${date}.csv`)
      setAuditActionMessage('Audit export downloaded.')
      setAuditActionError('')
    } catch (error: unknown) {
      setAuditActionError(getErrorMessage(error, 'Unable to export audit events'))
      setAuditActionMessage('')
    }
  }

  const refreshAuthDiagnostics = useCallback(async () => {
    try {
      const response = await api.getAuthDiagnostics()
      setAuthDiagnostics(response)
    } catch {
      setAuthDiagnostics(null)
    }
  }, [api])

  const saveSettings = useCallback(async () => {
    if (!canSave) return
    try {
      const response = normalizeDashboardSystemSettings(await api.updateSystemSettings(settings))
      setSettings(response)
      setSavedSettings(response)
      await refreshAuthDiagnostics()
      setSaveMessage('Settings saved.')
      setRequestError('')
    } catch (error: unknown) {
      setRequestError(getErrorMessage(error, 'Unable to save settings'))
      setSaveMessage('')
    }
  }, [api, canSave, refreshAuthDiagnostics, settings])

  const resetSettings = useCallback(async () => {
    try {
      const response = normalizeDashboardSystemSettings(await api.updateSystemSettings(DEFAULT_SETTINGS))
      setSettings(response)
      setSavedSettings(response)
      await refreshAuthDiagnostics()
      setSaveMessage('Settings reset to defaults.')
      setRequestError('')
      setConfirmingResetAll(false)
    } catch (error: unknown) {
      setRequestError(getErrorMessage(error, 'Unable to reset settings'))
      setSaveMessage('')
      setConfirmingResetAll(false)
    }
  }, [api, refreshAuthDiagnostics])

  const discardChanges = useCallback(() => {
    setSettings(savedSettings)
    setSaveMessage('')
    setRequestError('')
  }, [savedSettings])

  const applyGeneratorPreset = (preset: typeof GENERATOR_PRESETS[number]) => {
    setSettings(prev => ({
      ...prev,
      capacity_planning: {
        ...prev.capacity_planning,
        generator: {
          cpu_cores: preset.cpu_cores,
          memory_mb: preset.memory_mb,
          network_mbps: preset.network_mbps,
          max_vusers: preset.max_vusers,
        },
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const applyOIDCProviderPreset = (preset: typeof OIDC_PROVIDER_PRESETS[number]) => {
    setSettings(prev => ({
      ...prev,
      auth: {
        ...prev.auth,
        mode: 'oidc',
        provider_name: preset.provider_name,
        scopes: [...preset.scopes],
        roles_claim: preset.roles_claim,
        project_roles_claim: preset.project_roles_claim,
        client_secret_env_var: preset.client_secret_env_var,
        session_secret_env_var: preset.session_secret_env_var,
        logout_url: preset.logout_url,
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const applyLiveGeneratorPreset = async () => {
    try {
      const inventory = await api.getSystemInventory()
      const preset = deriveLiveGeneratorPreset(inventory)
      setSettings(prev => applyLiveGeneratorPresetToSettings(prev, preset))
      setSaveMessage(`Estimated from ${preset.generatorCount} connected load generator pod${preset.generatorCount === 1 ? '' : 's'}: CPU set to ${(preset.cpuCores ?? settings.capacity_planning.generator.cpu_cores).toLocaleString()}, memory set to ${(preset.memoryMB ?? settings.capacity_planning.generator.memory_mb).toLocaleString()} MB, max VUsers set to ${preset.maxVUsers.toLocaleString()}, and configured available generators set to ${preset.configuredAvailableGenerators}. Network remains manual until live pod bandwidth limits are reported.`)
      setRequestError('')
    } catch (error: unknown) {
      setRequestError(getErrorMessage(error, 'Unable to estimate generator capacity from live pods'))
      setSaveMessage('')
    }
  }

  const orderedSectionIds = mergeSettingsOrder(sectionOrder)
  const getSectionOrder = (id: SettingsSectionId) => orderedSectionIds.indexOf(id)
  const sectionClass = (id: SettingsSectionId) =>
    `settings-card ${customizingSections ? 'editable' : ''} ${draggedSectionId === id ? 'dragging' : ''}`

  const createApplication = async () => {
    const name = newApplicationName.trim()
    if (!name) return
    try {
      await createAndSelectApplication(name)
      setNewApplicationName('')
      setApplicationActionError('')
    } catch (error: unknown) {
      setApplicationActionError(getErrorMessage(error, 'Unable to create application'))
    }
  }

  const selectDomain = (domain: Domain) => {
    setSelectedDomainId(domain.id)
    setDomainDraftName(domain.name)
    setDomainDraftDescription(domain.description || '')
    setDomainActionError('')
    setDomainActionMessage('')
    onDomainChange?.(domain)
  }

  const createDomain = async () => {
    if (!canManageDomains) {
      setDomainActionError('Current session cannot manage domains.')
      setDomainActionMessage('')
      return
    }
    const name = newDomainName.trim()
    if (!name) return
    try {
      const created = await repositoryApi.createDomain({
        name,
        description: newDomainDescription.trim(),
      })
      setNewDomainName('')
      setNewDomainDescription('')
      setSelectedDomainId(created.domain.id)
      setDomainDraftName(created.domain.name)
      setDomainDraftDescription(created.domain.description || '')
      onDomainChange?.(created.domain)
      await onDomainsRefresh?.(created.domain.id)
      setDomainActionMessage(`Domain "${created.domain.name}" created.`)
      setDomainActionError('')
    } catch (error: unknown) {
      setDomainActionError(getErrorMessage(error, 'Unable to create domain'))
      setDomainActionMessage('')
    }
  }

  const saveDomain = async () => {
    if (!selectedDomain) return
    if (!canManageDomains) {
      setDomainActionError('Current session cannot manage domains.')
      setDomainActionMessage('')
      return
    }
    if (domainDraftValidationError) {
      setDomainActionError(domainDraftValidationError)
      setDomainActionMessage('')
      return
    }
    try {
      const updated = await repositoryApi.updateDomain(selectedDomain.id, {
        name: domainDraftName.trim(),
        description: domainDraftDescription.trim(),
      })
      setSelectedDomainId(updated.id)
      setDomainDraftName(updated.name)
      setDomainDraftDescription(updated.description || '')
      onDomainChange?.(updated)
      await onDomainsRefresh?.(updated.id)
      setDomainActionMessage(`Domain "${updated.name}" saved.`)
      setDomainActionError('')
    } catch (error: unknown) {
      setDomainActionError(getErrorMessage(error, 'Unable to save domain'))
      setDomainActionMessage('')
    }
  }

  const deleteDomain = async (domain: Domain) => {
    if (!canManageDomains) {
      setDomainActionError('Current session cannot manage domains.')
      setDomainActionMessage('')
      setConfirmDeleteDomain(null)
      return
    }
    if (domain.id === 'default') {
      setDomainActionError('Default domain cannot be deleted.')
      setDomainActionMessage('')
      setConfirmDeleteDomain(null)
      return
    }
    const projectCount = projectCountByDomain.get(domain.id) || 0
    if (projectCount > 0) {
      setDomainActionError(`Domain "${domain.name}" still has ${projectCount} project${projectCount === 1 ? '' : 's'}. Move or delete those projects before deleting the domain.`)
      setDomainActionMessage('')
      setConfirmDeleteDomain(null)
      return
    }
    try {
      await repositoryApi.deleteDomain(domain.id)
      const fallbackDomain = domains.find(candidate => candidate.id !== domain.id) || null
      if (fallbackDomain) {
        setSelectedDomainId(fallbackDomain.id)
        onDomainChange?.(fallbackDomain)
      } else {
        setSelectedDomainId('')
      }
      await onDomainsRefresh?.(fallbackDomain?.id)
      setDomainActionMessage(`Domain "${domain.name}" deleted.`)
      setDomainActionError('')
      setConfirmDeleteDomain(null)
    } catch (error: unknown) {
      setDomainActionError(getErrorMessage(error, 'Unable to delete domain'))
      setDomainActionMessage('')
      setConfirmDeleteDomain(null)
    }
  }

  const selectProject = (project: Project | null) => {
    setSelectedProjectId(project?.id || '')
    setProjectDraftName(project?.name || '')
    setProjectDraftRules(toDraftRedactionRules(project?.settings?.redaction_rules))
    setProjectDraftMaxConcurrentTests(project?.settings?.max_concurrent_tests !== undefined ? String(project.settings.max_concurrent_tests) : '')
    setProjectDraftMaxVUsers(project?.settings?.max_vusers !== undefined ? String(project.settings.max_vusers) : '')
    setProjectDraftMaxVUsersPerTest(project?.settings?.max_vusers_per_test !== undefined ? String(project.settings.max_vusers_per_test) : '')
    setProjectDraftMaxTestDurationMinutes(project?.settings?.max_test_duration_minutes !== undefined ? String(project.settings.max_test_duration_minutes) : '')
    loadProjectGitHubDraft(project?.settings?.github)
    setProjectActionError('')
    setProjectActionMessage('')
  }

  const createProject = async () => {
    if (!canManageProjects) {
      setProjectActionError('Current session cannot manage projects.')
      setProjectActionMessage('')
      return
    }
    const name = newProjectName.trim()
    if (!name) return
    try {
      const created = await projectApi.createProject({ name })
      const nextProjects = sortProjects([...projects, created])
      setProjects(nextProjects)
      setNewProjectName('')
      selectProject(created)
      setProjectActionMessage(`Project "${created.name}" created.`)
    } catch (error: unknown) {
      setProjectActionError(getErrorMessage(error, 'Unable to create project'))
      setProjectActionMessage('')
    }
  }

  const updateProjectRule = (index: number, key: keyof DraftRedactionRule, value: string) => {
    setProjectDraftRules(prev => prev.map((rule, ruleIndex) => (ruleIndex === index ? { ...rule, [key]: value } : rule)))
    setProjectActionError('')
    setProjectActionMessage('')
  }

  const addProjectRule = () => {
    setProjectDraftRules(prev => [...prev, { name: '', pattern: '', replacement: '' }])
    setProjectActionError('')
    setProjectActionMessage('')
  }

  const removeProjectRule = (index: number) => {
    setProjectDraftRules(prev => prev.filter((_, ruleIndex) => ruleIndex !== index))
    setProjectActionError('')
    setProjectActionMessage('')
  }

  const saveProjectCompliance = async () => {
    if (!selectedProject) return
    if (!canManageProjects) {
      setProjectActionError('Current session cannot manage this project.')
      setProjectActionMessage('')
      return
    }
    if (projectDraftValidationError) {
      setProjectActionError(projectDraftValidationError)
      setProjectActionMessage('')
      return
    }

    try {
      const updated = await projectApi.updateProject(selectedProject.id, {
        name: projectDraftName.trim(),
        settings: buildProjectSettingsDraft(projectDraftRules, projectDraftMaxConcurrentTests, projectDraftMaxVUsers, projectDraftMaxVUsersPerTest, projectDraftMaxTestDurationMinutes, buildProjectGitHubDraft()),
      })
      const nextProjects = sortProjects(projects.map(project => (project.id === updated.id ? updated : project)))
      setProjects(nextProjects)
      selectProject(updated)
      setProjectActionMessage(`Saved project governance defaults for ${updated.name}.`)
    } catch (error: unknown) {
      setProjectActionError(getErrorMessage(error, 'Unable to save project governance defaults'))
      setProjectActionMessage('')
    }
  }

  const startRenamingApplication = () => {
    if (!activeApplication) return
    setRenamingApplicationName(activeApplication.name)
  }

  const confirmRenameApplication = async () => {
    if (!activeApplication || renamingApplicationName === null) return
    const name = renamingApplicationName.trim()
    if (!name) return
    try {
      const updated = await applicationApi.updateApplication(activeApplication.id, { name })
      await refreshApplications()
      setActiveApplication(updated)
      setApplicationActionError('')
      setRenamingApplicationName(null)
    } catch (error: unknown) {
      setApplicationActionError(getErrorMessage(error, 'Unable to rename application'))
    }
  }

  const deleteApplication = async () => {
    if (!activeApplication) return
    try {
      await applicationApi.deleteApplication(activeApplication.id)
      await refreshApplications()
      setApplicationActionError('')
      setConfirmDeleteApp(false)
    } catch (error: unknown) {
      setApplicationActionError(getErrorMessage(error, 'Unable to delete application'))
      setConfirmDeleteApp(false)
    }
  }

  const startRenamingRelease = (releaseId: string, currentName: string) => {
    setRenamingReleaseId(releaseId)
    setRenamingReleaseName(currentName)
  }

  const confirmRenameRelease = async () => {
    if (!renamingReleaseId) return
    const name = renamingReleaseName.trim()
    if (!name) return
    try {
      const updated = await applicationApi.updateRelease(renamingReleaseId, { name })
      await refreshReleases(activeApplication?.id)
      const response = await applicationApi.listReleases(activeApplication?.id || '', true)
      setManagedReleases(response.releases || [])
      if (!updated.archived_at) setActiveRelease(updated)
      setApplicationActionError('')
      setRenamingReleaseId(null)
      setRenamingReleaseName('')
    } catch (error: unknown) {
      setApplicationActionError(getErrorMessage(error, 'Unable to rename release'))
    }
  }

  const archiveRelease = async (releaseId: string, releaseName: string) => {
    const results = await api.listTests({ application_id: activeApplication?.id, release_id: releaseId })
    if ((results.tests || []).length > 0) {
      setApplicationActionError(`Release "${releaseName}" still has associated results. Archive or delete those results before archiving the release.`)
      setConfirmArchiveRelease(null)
      return
    }
    try {
      await applicationApi.archiveRelease(releaseId)
      await refreshReleases(activeApplication?.id)
      const response = await applicationApi.listReleases(activeApplication?.id || '', true)
      setManagedReleases(response.releases || [])
      setApplicationActionError('')
      setConfirmArchiveRelease(null)
    } catch (error: unknown) {
      setApplicationActionError(getErrorMessage(error, 'Unable to archive release'))
      setConfirmArchiveRelease(null)
    }
  }

  const deleteRelease = async (releaseId: string, releaseName: string) => {
    const results = await api.listTests({ application_id: activeApplication?.id, release_id: releaseId })
    if ((results.tests || []).length > 0) {
      setApplicationActionError(`Release "${releaseName}" still has associated results. Archive or delete those results before deleting the release.`)
      setConfirmDeleteRelease(null)
      return
    }
    try {
      await applicationApi.deleteRelease(releaseId)
      await refreshReleases(activeApplication?.id)
      const response = await applicationApi.listReleases(activeApplication?.id || '', true)
      setManagedReleases(response.releases || [])
      setApplicationActionError('')
      setConfirmDeleteRelease(null)
    } catch (error: unknown) {
      setApplicationActionError(getErrorMessage(error, 'Unable to delete release'))
      setConfirmDeleteRelease(null)
    }
  }

  const createDataPool = async () => {
    const name = dataPoolName.trim()
    if (!name) return
    try {
      const pool = await api.createDataPool({ name, description: dataPoolDescription.trim(), project_id: activeProject?.id })
      setDataPoolName('')
      setDataPoolDescription('')
      await loadDataPools(pool.id)
      setDataPoolActionError('')
    } catch (error: unknown) {
      setDataPoolActionError(getErrorMessage(error, 'Unable to create data pool'))
    }
  }

  const deleteDataPool = async (pool: SharedDataPool) => {
    setConfirmDeletePool(null)
    try {
      await api.deleteDataPool(pool.id)
      await loadDataPools('')
      setDataPoolActionError('')
    } catch (error: unknown) {
      setDataPoolActionError(getErrorMessage(error, 'Unable to delete data pool'))
    }
  }

  const saveDataPoolRow = async () => {
    if (!selectedDataPoolId) return
    let parsed: Record<string, string>
    try {
      parsed = JSON.parse(dataPoolRowJson)
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
        throw new Error('Row data must be a JSON object.')
      }
    } catch (error: unknown) {
      setDataPoolActionError(getErrorMessage(error, 'Invalid row JSON'))
      return
    }
    try {
      if (editingDataPoolRowId) {
        await api.updateDataPoolRow(selectedDataPoolId, editingDataPoolRowId, { data: parsed })
      } else {
        await api.createDataPoolRow(selectedDataPoolId, { data: parsed })
      }
      setEditingDataPoolRowId(null)
      setDataPoolRowJson('{\n  "customer_id": "C-10001"\n}')
      const response = await api.getDataPool(selectedDataPoolId)
      setDataPoolRows(response.rows || [])
      setDataPoolActionError('')
    } catch (error: unknown) {
      setDataPoolActionError(getErrorMessage(error, 'Unable to save data row'))
    }
  }

  const editDataPoolRow = (row: SharedDataPoolRow) => {
    setEditingDataPoolRowId(row.id)
    setDataPoolRowJson(JSON.stringify(row.data || {}, null, 2))
  }

  const addNotificationWebhook = () => {
    setSettings(current => ({
      ...current,
      notifications: {
        ...current.notifications,
        webhooks: [
          ...current.notifications.webhooks,
          {
            name: '',
            enabled: true,
            url: '',
            format: 'slack',
            events: [],
            api_token: '',
            entity_selector: '',
          },
        ],
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updateNotificationWebhook = (index: number, updates: Partial<DashboardSystemSettings['notifications']['webhooks'][number]>) => {
    setSettings(current => ({
      ...current,
      notifications: {
        ...current.notifications,
        webhooks: current.notifications.webhooks.map((webhook, webhookIndex) => (
          webhookIndex === index ? { ...webhook, ...updates } : webhook
        )),
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const removeNotificationWebhook = (index: number) => {
    setSettings(current => ({
      ...current,
      notifications: {
        ...current.notifications,
        webhooks: current.notifications.webhooks.filter((_, webhookIndex) => webhookIndex !== index),
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updatePrometheus = (updates: Partial<DashboardSystemSettings['prometheus']>) => {
    setSettings(current => ({
      ...current,
      prometheus: {
        ...current.prometheus,
        ...updates,
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const deleteDataPoolRow = async (row: SharedDataPoolRow) => {
    if (!selectedDataPoolId) return
    setConfirmDeleteRow(null)
    try {
      await api.deleteDataPoolRow(selectedDataPoolId, row.id)
      const response = await api.getDataPool(selectedDataPoolId)
      setDataPoolRows(response.rows || [])
      setDataPoolActionError('')
    } catch (error: unknown) {
      setDataPoolActionError(getErrorMessage(error, 'Unable to delete data row'))
    }
  }

  const handleSectionDragStart = (event: DragEvent<HTMLDivElement>, id: SettingsSectionId) => {
    if (!customizingSections) return
    setDraggedSectionId(id)
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', id)
  }

  const handleSectionDragOver = (event: DragEvent<HTMLDivElement>, id: SettingsSectionId) => {
    if (!customizingSections || !draggedSectionId || draggedSectionId === id) return
    event.preventDefault()
    event.dataTransfer.dropEffect = 'move'
  }

  const handleSectionDrop = (event: DragEvent<HTMLDivElement>, id: SettingsSectionId) => {
    if (!customizingSections) return
    event.preventDefault()
    const sourceId = (event.dataTransfer.getData('text/plain') || draggedSectionId) as SettingsSectionId | null
    if (!sourceId) return
    setSectionOrder(prev => moveSettingsSection(mergeSettingsOrder(prev), sourceId, id))
    setDraggedSectionId(null)
  }

  const renderSettingsSubnav = <SubsectionId extends string,>(
    ariaLabel: string,
    idPrefix: string,
    subsections: readonly SettingsSubsectionDefinition<SubsectionId>[],
    activeSubsection: SubsectionId,
    setActiveSubsection: (id: SubsectionId) => void,
    summaries: Record<SubsectionId, string>,
  ) => (
    <nav className="settings-subnav" role="tablist" aria-label={ariaLabel}>
      {subsections.map(subsection => (
        <button
          key={subsection.id}
          id={`${idPrefix}-tab-${subsection.id}`}
          type="button"
          role="tab"
          aria-selected={activeSubsection === subsection.id}
          aria-controls={`${idPrefix}-panel-${subsection.id}`}
          className={`settings-subnav-item ${activeSubsection === subsection.id ? 'active' : ''}`}
          onClick={() => setActiveSubsection(subsection.id)}
        >
          <span className="settings-subnav-label">{subsection.title}</span>
          <span className="settings-subnav-meta">{summaries[subsection.id]}</span>
        </button>
      ))}
    </nav>
  )

  const sectionMessages = useMemo(() => {
    const msgs: Record<SettingsSectionId, { notices: string[]; errors: string[] }> = {
      general: { notices: [], errors: [] },
      thresholds: { notices: [], errors: [] },
      capacity: { notices: [], errors: [] },
      domains: { notices: [], errors: [] },
      applications: { notices: [], errors: [] },
      projects: { notices: [], errors: [] },
      'data-pools': { notices: [], errors: [] },
      integrations: { notices: [], errors: [] },
    }
    if (saveMessage) msgs[activeSection].notices.push(saveMessage)
    if (requestError) msgs[activeSection].errors.push(requestError)
    if (domainActionMessage) msgs.domains.notices.push(domainActionMessage)
    if (domainActionError) msgs.domains.errors.push(domainActionError)
    if (applicationActionError) msgs.applications.errors.push(applicationActionError)
    if (projectActionMessage) msgs.projects.notices.push(projectActionMessage)
    if (projectActionError) msgs.projects.errors.push(projectActionError)
    if (dataPoolActionError) msgs['data-pools'].errors.push(dataPoolActionError)
    return msgs
  }, [saveMessage, requestError, domainActionMessage, domainActionError, applicationActionError, projectActionMessage, projectActionError, dataPoolActionError, activeSection])

  const activeSectionMessages = sectionMessages[activeSection]
  const settingsTopbarContribution = useMemo(() => ({
    id: 'settings-page',
    title: 'Settings',
    controls: (
      <PageViewToggle
        value={activeSection}
        ariaLabel="Settings sections"
        className="settings-section-toggle settings-topbar-section-toggle new-test-view-toggle"
        options={SETTINGS_SECTION_TOPBAR_OPTIONS}
        onChange={setActiveSection}
      />
    ),
    primaryActions: [
      {
        id: 'save-settings',
        label: hasUnsavedChanges ? 'Save Changes' : 'Saved',
        compactLabel: hasUnsavedChanges ? 'Save' : 'Saved',
        onClick: () => { void saveSettings() },
        disabled: !canSave,
        tone: 'primary' as const,
        title: canSave ? 'Save settings changes' : 'Settings are already saved or cannot be saved',
      },
    ],
    secondaryActions: [
      {
        id: 'discard-settings',
        label: 'Discard Changes',
        compactLabel: 'Discard',
        onClick: discardChanges,
        hidden: !hasUnsavedChanges,
        tone: 'secondary' as const,
      },
    ],
    priority: 20,
  }), [activeSection, canSave, discardChanges, hasUnsavedChanges, saveSettings])

  useTopbarContribution(settingsTopbarContribution)

  return (
    <div className="page settings-page">
      <div className="page-header">
        <div>
          <h2>Settings</h2>
          <p className="page-subtitle">Configuration, thresholds, and integrations</p>
        </div>
        {!canManageSystem && (
          <p className="settings-error">Current session can view settings but cannot save global configuration changes.</p>
        )}
      </div>

      <div className="settings-tabbed-layout">
        <div className="settings-main">
          {loading && <p className="settings-notice">Loading settings...</p>}
          {activeSectionMessages.notices.map(msg => (
            <p key={msg} className="settings-notice">{msg}</p>
          ))}
          {activeSectionMessages.errors.map(msg => (
            <p key={msg} className="settings-error-inline">{msg}</p>
          ))}
          {validationErrors.length > 0 && (
            <div className="settings-errors" role="alert">
              {validationErrors.map((error) => (
                <p key={error}>{error}</p>
              ))}
            </div>
          )}

          <div className="settings-grid">
        <div
          id="settings-section-general"
          className={sectionClass('general')}
          hidden={activeSection !== 'general'}
          draggable={customizingSections}
          onDragStart={event => handleSectionDragStart(event, 'general')}
          onDragOver={event => handleSectionDragOver(event, 'general')}
          onDrop={event => handleSectionDrop(event, 'general')}
          onDragEnd={() => setDraggedSectionId(null)}
          style={{ order: getSectionOrder('general') }}
        >
          <div className="settings-subsection-layout">
            <nav className="settings-subnav" role="tablist" aria-label="General settings categories">
              {GENERAL_SETTINGS_SUBSECTIONS.map(subsection => (
                <button
                  key={subsection.id}
                  id={`settings-general-tab-${subsection.id}`}
                  type="button"
                  role="tab"
                  aria-selected={activeGeneralSubsection === subsection.id}
                  aria-controls={`settings-general-panel-${subsection.id}`}
                  className={`settings-subnav-item ${activeGeneralSubsection === subsection.id ? 'active' : ''}`}
                  onClick={() => setActiveGeneralSubsection(subsection.id)}
                >
                  <span className="settings-subnav-label">{subsection.title}</span>
                  <span className="settings-subnav-meta">{generalSubsectionSummaries[subsection.id]}</span>
                </button>
              ))}
            </nav>

            <section
              id="settings-general-panel-platform"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-general-tab-platform"
              hidden={activeGeneralSubsection !== 'platform'}
            >
              <div className="settings-group">
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">API Endpoint <span className="settings-restart-badge">Restart required</span></span>
                <span className="setting-desc">Controller API server address. Changes take effect after restarting the controller.</span>
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
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Usage Snapshot Cadence (seconds)</span>
                <span className="setting-desc">Optional wall-clock cadence for durable usage accounting snapshots. Use 0 to disable scheduled capture.</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={0}
                step={60}
                value={settings.general.usage_snapshot_interval_seconds || 0}
                onChange={(e) => updateGeneral('usage_snapshot_interval_seconds', e.target.value)}
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Dashboard Theme</span>
                <span className="setting-desc">Stored locally in this browser and applied immediately</span>
              </div>
              <select
                className="settings-input"
                value={theme}
                onChange={(e) => onThemeChange(e.target.value as DashboardTheme)}
              >
                <option value="dark">Dark</option>
                <option value="light">Light</option>
                <option value="solarized-dark">Solarized Dark</option>
                <option value="solarized-light">Solarized Light</option>
                <option value="monokai-dimmed">Monokai Dimmed</option>
              </select>
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Desktop Client Promotion</span>
                <span className="setting-desc">Controls whether the hosted browser UI promotes the optional desktop workspace. Authorized API access is still governed by RBAC.</span>
              </div>
              <label className="settings-checkbox-label">
                <input
                  type="checkbox"
                  checked={settings.client_app.availability === 'enabled'}
                  onChange={event => updateClientApp(event.target.checked ? 'enabled' : 'disabled')}
                  disabled={!canManageSystem}
                />
                <span>{settings.client_app.availability === 'enabled' ? 'Enabled' : 'Disabled'}</span>
              </label>
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Client Software Downloads</span>
                <span className="setting-desc">Controls whether the dashboard offers the “Download client” menu (Recording Agent and desktop app). When disabled, the menu is hidden and the download endpoints are refused.</span>
              </div>
              <label className="settings-checkbox-label">
                <input
                  type="checkbox"
                  checked={settings.client_downloads.enabled}
                  onChange={event => updateClientDownloads(event.target.checked)}
                  disabled={!canManageSystem}
                />
                <span>{settings.client_downloads.enabled ? 'Enabled' : 'Disabled'}</span>
              </label>
            </div>
              </div>
            </section>

            <section
              id="settings-general-panel-usage"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-general-tab-usage"
              hidden={activeGeneralSubsection !== 'usage'}
            >
              <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Usage Accounting</span>
                  <span className="setting-desc">Current project chargeback rollup plus durable snapshots for governance review.</span>
                </div>
                <strong>{usageAccounting ? usageAccounting.totals.run_count : '-'}</strong>
              </div>
              <div className="new-test-summary-grid">
                <div><span>VUser Seconds</span><strong>{usageAccounting ? usageAccounting.totals.vuser_seconds.toLocaleString() : '-'}</strong></div>
                <div><span>Duration</span><strong>{usageAccounting ? formatDurationSeconds(usageAccounting.totals.duration_seconds) : '-'}</strong></div>
                <div><span>Archive Bytes</span><strong>{usageAccounting ? formatBytes(usageAccounting.totals.archive_bytes) : '-'}</strong></div>
                <div><span>Status Counts</span><strong>{usageAccounting ? formatStatusCounts(usageAccounting.totals.status_counts) : '-'}</strong></div>
              </div>
              <div className="settings-inline-actions">
                <button className="btn btn-secondary" type="button" onClick={refreshUsageAccounting}>
                  Refresh Usage
                </button>
                <button className="btn btn-secondary" type="button" onClick={captureUsageSnapshot} disabled={!canManageSystem}>
                  Capture Snapshot
                </button>
                <button className="btn btn-secondary" type="button" onClick={exportUsageAccounting}>
                  Export Usage CSV
                </button>
              </div>
              {usageActionMessage && <p className="settings-notice">{usageActionMessage}</p>}
              {usageActionError && <p className="settings-error-inline">{usageActionError}</p>}
              <div className="settings-release-list">
                {usageSnapshots.slice(0, 5).map(snapshot => (
                  <div key={snapshot.id} className="settings-release-row">
                    <div className="settings-release-main">
                      <span>{snapshot.id}</span>
                      <small>Captured {formatDateTime(snapshot.created_at)}</small>
                      <small>{snapshot.project_id || 'all projects'} / {snapshot.usage.totals.run_count} runs / {snapshot.usage.totals.vuser_seconds.toLocaleString()} VUser seconds</small>
                    </div>
                    <span className="setting-value">{formatBytes(snapshot.usage.totals.archive_bytes)}</span>
                  </div>
                ))}
                {usageSnapshots.length === 0 && <p className="new-test-inline-note">No usage snapshots captured yet.</p>}
              </div>
            </div>
            <EntitlementHeadroom />
              </div>
            </section>

            <section
              id="settings-general-panel-entitlements"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-general-tab-entitlements"
              hidden={activeGeneralSubsection !== 'entitlements'}
            >
              <div className="settings-group">
                <div className="settings-subsection">
                  <div className="settings-manager-summary">
                    <div>
                      <span className="setting-label">Entitlements</span>
                      <span className="setting-desc">Edition, enabled protocol bundles, operational limits, and current consumption.</span>
                    </div>
                  </div>
                  <EntitlementsPanel />
                </div>
              </div>
            </section>

            <section
              id="settings-general-panel-readiness"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-general-tab-readiness"
              hidden={activeGeneralSubsection !== 'readiness'}
            >
              <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Production Readiness</span>
                  <span className="setting-desc">Controller-owned readiness checks for storage, archive writes, generator RPC, telemetry, auth, and scheduler state.</span>
                </div>
                <strong>{formatReadinessStatus(systemReadiness?.status)}</strong>
              </div>
              <div className="new-test-summary-grid">
                <div><span>Generated</span><strong>{systemReadiness ? formatDateTime(systemReadiness.generated_at) : '-'}</strong></div>
                <div><span>Ready</span><strong>{systemReadiness ? systemReadiness.checks.filter(check => check.status === 'ready').length : '-'}</strong></div>
                <div><span>Degraded</span><strong>{systemReadiness ? systemReadiness.checks.filter(check => check.status === 'degraded').length : '-'}</strong></div>
                <div><span>Blocked</span><strong>{systemReadiness ? systemReadiness.checks.filter(check => check.status === 'not_ready').length : '-'}</strong></div>
              </div>
              <div className="settings-inline-actions">
                <button className="btn btn-secondary" type="button" onClick={refreshSystemReadiness}>
                  Refresh Readiness
                </button>
              </div>
              {readinessActionMessage && <p className="settings-notice">{readinessActionMessage}</p>}
              {readinessActionError && <p className="settings-error-inline">{readinessActionError}</p>}
              <div className="settings-release-list">
                {(systemReadiness?.checks || []).map(check => {
                  const details = formatReadinessDetails(check.details)
                  return (
                    <div key={check.name} className="settings-release-row">
                      <div className="settings-release-main">
                        <span>{formatReadinessCheckName(check.name)}</span>
                        <small>{check.message}</small>
                        {details && <small>{details}</small>}
                      </div>
                      <span className={`script-health-token ${readinessTone(check.status)}`}>{formatReadinessStatus(check.status)}</span>
                    </div>
                  )
                })}
                {!systemReadiness && <p className="new-test-inline-note">Production readiness is unavailable.</p>}
              </div>
            </div>
              </div>
            </section>

            <section
              id="settings-general-panel-authentication"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-general-tab-authentication"
              hidden={activeGeneralSubsection !== 'authentication'}
            >
              <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Access Control</span>
                  <span className="setting-desc">Backend 3.1 scaffold for local development bypass, Microsoft Entra ID-backed trusted headers, service-account API keys, and governance audit history.</span>
                </div>
                <strong>{formatAuthMode(settings.auth.mode)}</strong>
              </div>
              <div className="setting-row">
                <div className="setting-info">
                  <span className="setting-label">Access Control Mode <span className="settings-restart-badge">Restart required</span></span>
                  <span className="setting-desc">Dev Bypass keeps local development open by default. Trusted Headers expects an authenticating reverse proxy to inject user and role headers before requests reach APEX Load. Changes to auth mode take effect after restarting the controller.</span>
                </div>
                <select
                  className="settings-input"
                  value={settings.auth.mode}
                  onChange={(e) => updateAuth('mode', e.target.value)}
                >
                  <option value="dev">Dev Bypass</option>
                  <option value="oidc">Native OIDC</option>
                  <option value="trusted_headers">Trusted Headers</option>
                </select>
              </div>
              {settings.auth.mode === 'dev' && (
                <div className="settings-subsection">
                  <div className="settings-manager-summary">
                    <div>
                      <span className="setting-label">Development Bypass</span>
                      <span className="setting-desc">Local development bypass is active. APEX Load allows dashboard and API access without upstream identity, while still recording audit events and supporting service-account API keys for automation flows.</span>
                    </div>
                    <strong>{formatAuthStatusSummary(authStatus)}</strong>
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Current Access</span>
                      <span className="setting-desc">All operations are currently permitted through the explicit dev-mode bypass. Switch to Trusted Headers before production use.</span>
                    </div>
                    <span className="settings-value-display">Open access for local development</span>
                  </div>
                </div>
              )}
              {settings.auth.mode === 'trusted_headers' && (
                <>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Provider Label</span>
                      <span className="setting-desc">Optional identity provider or gateway name shown in the dashboard sign-in screen.</span>
                    </div>
                    <input
                      className="settings-input"
                      value={settings.auth.provider_name || ''}
                      onChange={(e) => updateAuth('provider_name', e.target.value)}
                      placeholder="Microsoft Entra ID"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Login URL</span>
                      <span className="setting-desc">Optional upstream sign-in URL or absolute path used by the dashboard access gate.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.login_url || ''}
                      onChange={(e) => updateAuth('login_url', e.target.value)}
                      placeholder="https://login.example.com/apexload/start"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Logout URL</span>
                      <span className="setting-desc">Optional upstream sign-out URL or absolute path surfaced for future dashboard navigation.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.logout_url || ''}
                      onChange={(e) => updateAuth('logout_url', e.target.value)}
                      placeholder="/logout"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">User Header</span>
                      <span className="setting-desc">Required identity header. Missing values return 401 before the request reaches protected handlers.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.user_header}
                      onChange={(e) => updateAuth('user_header', e.target.value)}
                      placeholder="X-Auth-Request-User"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Display Name Header</span>
                      <span className="setting-desc">Optional friendly name forwarded into the request context for future UI and audit use.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.name_header}
                      onChange={(e) => updateAuth('name_header', e.target.value)}
                      placeholder="X-Auth-Request-Name"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Email Header</span>
                      <span className="setting-desc">Optional email address from the upstream identity provider.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.email_header}
                      onChange={(e) => updateAuth('email_header', e.target.value)}
                      placeholder="X-Auth-Request-Email"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Global Roles Header</span>
                      <span className="setting-desc">Comma or pipe-delimited roles such as viewer, editor, project_admin, or system_admin.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.roles_header}
                      onChange={(e) => updateAuth('roles_header', e.target.value)}
                      placeholder="X-APEXLoad-Roles"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Project Roles Header</span>
                      <span className="setting-desc">Project-scoped assignments in the form project-id=viewer|editor;other-project=project_admin.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.project_roles_header}
                      onChange={(e) => updateAuth('project_roles_header', e.target.value)}
                      placeholder="X-APEXLoad-Project-Roles"
                    />
                  </div>
                  <div className="settings-subsection">
                    <div className="settings-manager-summary">
                      <div>
                        <span className="setting-label">Microsoft Entra ID Gateway Contract</span>
                        <span className="setting-desc">Use your Entra-backed ingress or reverse proxy to translate identity claims into the headers APEX Load evaluates for RBAC.</span>
                      </div>
                      <strong>{settings.auth.provider_name?.trim() || 'Microsoft Entra ID'}</strong>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Required Subject Header</span>
                        <span className="setting-desc">Stable user identifier forwarded on every authenticated request.</span>
                      </div>
                      <span className="settings-value-display cell-mono">{formatHeaderContractValue(settings.auth.user_header)}</span>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Optional Identity Headers</span>
                        <span className="setting-desc">Friendly display data forwarded from Entra claims when available.</span>
                      </div>
                      <span className="settings-value-display cell-mono">{formatHeaderContractValue(settings.auth.name_header)} | {formatHeaderContractValue(settings.auth.email_header)}</span>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Global Role Mapping</span>
                        <span className="setting-desc">Map Entra groups or app roles into APEX Load roles such as viewer, editor, project_admin, and system_admin.</span>
                      </div>
                      <span className="settings-value-display cell-mono">{formatHeaderContractValue(settings.auth.roles_header, 'No global role header')}</span>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Project Role Mapping</span>
                        <span className="setting-desc">Emit assignments in the form project-id=viewer|editor;other-project=project_admin.</span>
                      </div>
                      <span className="settings-value-display cell-mono">{formatHeaderContractValue(settings.auth.project_roles_header, 'No project role header')}</span>
                    </div>
                  </div>
                  <div className="settings-subsection">
                    <div className="settings-manager-summary">
                      <div>
                        <span className="setting-label">Current Session Access</span>
                        <span className="setting-desc">Shows the identity and role assignments from the trusted headers reaching APEX Load right now. User lifecycle remains managed by Microsoft Entra ID or another upstream OIDC or SAML gateway.</span>
                      </div>
                      <strong>{formatAuthStatusSummary(authStatus)}</strong>
                    </div>
                    {authStatus?.authenticated && authStatus.user ? (
                      <>
                        <div className="setting-row">
                          <div className="setting-info">
                            <span className="setting-label">Subject</span>
                            <span className="setting-desc">Stable upstream identity key used for access checks.</span>
                          </div>
                          <span className="settings-value-display cell-mono">{authStatus.user.subject}</span>
                        </div>
                        <div className="setting-row">
                          <div className="setting-info">
                            <span className="setting-label">Display Name</span>
                            <span className="setting-desc">Friendly identity label if the proxy forwards one.</span>
                          </div>
                          <span className="settings-value-display">{authStatus.user.display_name || 'Not provided'}</span>
                        </div>
                        <div className="setting-row">
                          <div className="setting-info">
                            <span className="setting-label">Email</span>
                            <span className="setting-desc">Optional email forwarded from the upstream identity provider.</span>
                          </div>
                          <span className="settings-value-display">{authStatus.user.email || 'Not provided'}</span>
                        </div>
                        <div className="setting-row">
                          <div className="setting-info">
                            <span className="setting-label">Global Roles</span>
                            <span className="setting-desc">Effective instance-wide roles on this request.</span>
                          </div>
                          <span className="settings-value-display">{formatAuthRoleList(authStatus.user.global_roles)}</span>
                        </div>
                        <div className="setting-row">
                          <div className="setting-info">
                            <span className="setting-label">Project Roles</span>
                            <span className="setting-desc">Project-scoped assignments resolved from the forwarded headers.</span>
                          </div>
                          <span className="settings-value-display">{formatProjectRoleAssignments(authStatus.user.project_roles)}</span>
                        </div>
                      </>
                    ) : (
                      <div className="setting-row">
                        <div className="setting-info">
                          <span className="setting-label">Session Status</span>
                          <span className="setting-desc">Open the dashboard through the authenticating proxy so APEX Load receives identity and role headers.</span>
                        </div>
                        <span className="settings-value-display">No authenticated user detected</span>
                      </div>
                    )}
                  </div>
                  <div className="settings-subsection">
                    <div className="settings-manager-summary">
                      <div>
                        <span className="setting-label">Microsoft Entra ID Troubleshooting</span>
                        <span className="setting-desc">Use this checklist when APEX Load is configured for trusted headers but the current request is missing an authenticated identity.</span>
                      </div>
                      <strong>{authStatus?.authenticated ? 'Headers detected' : 'Check gateway mapping'}</strong>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Open Through Entra Gateway</span>
                        <span className="setting-desc">Reach the dashboard through the Entra-protected ingress or reverse proxy instead of calling the controller origin directly.</span>
                      </div>
                      <span className="settings-value-display">{authStatus?.authenticated ? 'Current request is authenticated' : 'Required for sign-in'}</span>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Subject Header Check</span>
                        <span className="setting-desc">Verify the gateway forwards the configured subject header on every authenticated request.</span>
                      </div>
                      <span className="settings-value-display cell-mono">{formatHeaderContractValue(settings.auth.user_header)}</span>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Role Header Check</span>
                        <span className="setting-desc">Forward either the global roles header or the project roles header so APEX Load can evaluate RBAC.</span>
                      </div>
                      <span className="settings-value-display cell-mono">{formatHeaderContractValue(settings.auth.roles_header, 'No global role header')} | {formatHeaderContractValue(settings.auth.project_roles_header, 'No project role header')}</span>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Gateway UX</span>
                        <span className="setting-desc">Provider label and login URL should point operators back to the correct Microsoft Entra ID entry point when APEX Load blocks unauthenticated access.</span>
                      </div>
                      <span className="settings-value-display">{settings.auth.provider_name?.trim() || 'Microsoft Entra ID'}{settings.auth.login_url?.trim() ? ` | ${settings.auth.login_url.trim()}` : ''}</span>
                    </div>
                  </div>
                </>
              )}
              {settings.auth.mode === 'oidc' && (
                <>
                  <div className="settings-subsection">
                    <div>
                      <span className="setting-label">OIDC Provider Presets</span>
                      <span className="setting-desc">Apply safe provider defaults for label, scopes, claim names, and secret environment variables. Issuer URL, client ID, and redirect URL stay deployment-specific.</span>
                    </div>
                    <div className="settings-preset-row">
                      {OIDC_PROVIDER_PRESETS.map(preset => (
                        <button key={preset.id} type="button" className="btn btn-secondary" onClick={() => applyOIDCProviderPreset(preset)}>
                          {preset.label}
                        </button>
                      ))}
                    </div>
                    <p className="setting-desc">{OIDC_PROVIDER_PRESETS.find(preset => preset.provider_name === settings.auth.provider_name)?.description || 'Choose a preset, then fill the issuer, client, and callback values registered with your identity provider.'}</p>
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Provider Label</span>
                      <span className="setting-desc">Friendly identity provider name shown in the dashboard sign-in experience.</span>
                    </div>
                    <input
                      className="settings-input"
                      value={settings.auth.provider_name || ''}
                      onChange={(e) => updateAuth('provider_name', e.target.value)}
                      placeholder="Microsoft Entra ID"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Issuer URL</span>
                      <span className="setting-desc">Absolute OIDC issuer URL used for discovery and ID-token verification.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.issuer_url || ''}
                      onChange={(e) => updateAuth('issuer_url', e.target.value)}
                      placeholder="https://login.example.com/tenant/v2.0"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Client ID</span>
                      <span className="setting-desc">OIDC client/application identifier registered for the dashboard.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.client_id || ''}
                      onChange={(e) => updateAuth('client_id', e.target.value)}
                      placeholder="apexload-dashboard"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Client Secret Env Var</span>
                      <span className="setting-desc">Environment variable name read by the controller at runtime for the OIDC client secret.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.client_secret_env_var || ''}
                      onChange={(e) => updateAuth('client_secret_env_var', e.target.value)}
                      placeholder="APEXLOAD_OIDC_CLIENT_SECRET"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Redirect URL</span>
                      <span className="setting-desc">Absolute callback URL registered with the identity provider and served by the controller.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.redirect_url || ''}
                      onChange={(e) => updateAuth('redirect_url', e.target.value)}
                      placeholder="https://apexload.example.com/api/v1/auth/callback"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Scopes</span>
                      <span className="setting-desc">Comma or space-delimited OIDC scopes requested during sign-in.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={formatAuthScopes(settings.auth.scopes)}
                      onChange={(e) => updateAuth('scopes', parseAuthScopes(e.target.value))}
                      placeholder="openid, profile, email"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Roles Claim</span>
                      <span className="setting-desc">Token claim used to map global RBAC roles such as viewer, editor, project_admin, or system_admin.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.roles_claim || ''}
                      onChange={(e) => updateAuth('roles_claim', e.target.value)}
                      placeholder="roles"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Project Roles Claim</span>
                      <span className="setting-desc">Token claim that carries project-scoped role assignments.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.project_roles_claim || ''}
                      onChange={(e) => updateAuth('project_roles_claim', e.target.value)}
                      placeholder="project_roles"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Session Secret Env Var</span>
                      <span className="setting-desc">Environment variable name read by the controller to sign the APEX Load auth session cookie.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.session_secret_env_var || ''}
                      onChange={(e) => updateAuth('session_secret_env_var', e.target.value)}
                      placeholder="APEXLOAD_AUTH_SESSION_SECRET"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Post-Logout Redirect URL</span>
                      <span className="setting-desc">Optional absolute URL or absolute path used after APEX Load clears its local OIDC session.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.logout_url || ''}
                      onChange={(e) => updateAuth('logout_url', e.target.value)}
                      placeholder="/"
                    />
                  </div>
                  <div className="settings-subsection">
                    <div className="settings-manager-summary">
                      <div>
                        <span className="setting-label">Native OIDC Session Flow</span>
                        <span className="setting-desc">APEX Load redirects the dashboard through the identity provider, exchanges the authorization code, and then serves its own signed session cookie for API access.</span>
                      </div>
                      <strong>{settings.auth.provider_name?.trim() || 'OIDC Provider'}</strong>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Sign-In Route</span>
                        <span className="setting-desc">Dashboard entry point exposed by the controller when native OIDC mode is enabled.</span>
                      </div>
                      <span className="settings-value-display cell-mono">/api/v1/auth/login</span>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Callback Route</span>
                        <span className="setting-desc">Must match the registered redirect URI at the identity provider.</span>
                      </div>
                      <span className="settings-value-display cell-mono">{settings.auth.redirect_url?.trim() || 'Not configured'}</span>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Claim Mapping</span>
                        <span className="setting-desc">APEX Load maps token claims back onto the same RBAC role catalog used by service accounts and project governance.</span>
                      </div>
                      <span className="settings-value-display cell-mono">{settings.auth.roles_claim?.trim() || 'roles'} | {settings.auth.project_roles_claim?.trim() || 'project_roles'}</span>
                    </div>
                  </div>
                </>
              )}
              {(settings.auth.mode === 'trusted_headers' || settings.auth.mode === 'oidc') && (
                <div className="settings-subsection">
                  <div className="settings-manager-summary">
                    <div>
                      <span className="setting-label">Auth Diagnostics</span>
                      <span className="setting-desc">Backend-owned readiness checks for the active auth mode. These checks surface missing env vars, invalid URLs, discovery failures, and missing gateway guidance before sign-in failures turn into guesswork.</span>
                    </div>
                    <strong>{formatAuthDiagnosticsHeadline(authDiagnostics)}</strong>
                  </div>
                  {authDiagnostics ? (
                    <>
                      <div className="setting-row">
                        <div className="setting-info">
                          <span className="setting-label">Readiness Summary</span>
                          <span className="setting-desc">Overall backend assessment for the current saved auth mode.</span>
                        </div>
                        <span className="settings-value-display">{authDiagnostics.summary}</span>
                      </div>
                      {authDiagnosticsStale && (
                        <div className="setting-row">
                          <div className="setting-info">
                            <span className="setting-label">Draft Changes Pending</span>
                            <span className="setting-desc">These diagnostics still reflect the last saved auth configuration.</span>
                          </div>
                          <span className="settings-value-display">Save changes to refresh diagnostics</span>
                        </div>
                      )}
                      {authDiagnostics.checks.map((check: DashboardAuthDiagnosticsCheck) => (
                        <div className="setting-row" key={check.id}>
                          <div className="setting-info">
                            <span className="setting-label">{check.label}</span>
                            <span className="setting-desc">{check.summary}</span>
                          </div>
                          <span className="settings-value-display">
                            {formatAuthDiagnosticStatus(check.status)}
                            {check.detail ? ` | ${check.detail}` : ''}
                          </span>
                        </div>
                      ))}
                    </>
                  ) : (
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Diagnostics Status</span>
                        <span className="setting-desc">APEX Load could not load the auth diagnostics payload for this session.</span>
                      </div>
                      <span className="settings-value-display">Unavailable</span>
                    </div>
                  )}
                </div>
              )}
            </div>
              </div>
            </section>

            <section
              id="settings-general-panel-governance"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-general-tab-governance"
              hidden={activeGeneralSubsection !== 'governance'}
            >
              <div className="settings-group">
              {canManageSystem && (
                <div className="settings-subsection">
                  <div className="settings-manager-summary">
                    <div>
                      <span className="setting-label">Scenario Schedule Defaults</span>
                      <span className="setting-desc">Administrator-owned governance and calendar-window defaults applied when new recurring schedules are created.</span>
                    </div>
                    <strong>{formatSchedulePolicyLabel(settings.schedule_defaults.approval_policy || 'auto')}</strong>
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Approval Policy</span>
                      <span className="setting-desc">Default review policy used by New Test schedules before each recurring run materializes.</span>
                    </div>
                    <select
                      className="settings-input"
                      value={settings.schedule_defaults.approval_policy || 'auto'}
                      onChange={(e) => updateScheduleDefaults('approval_policy', e.target.value)}
                    >
                      <option value="auto">Auto approve</option>
                      <option value="manual_review">Manual review</option>
                      <option value="high_cost">High cost</option>
                      <option value="off_hours">Off hours</option>
                      <option value="production">Production</option>
                      <option value="quota_burst">Quota burst</option>
                    </select>
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Missed Run Policy</span>
                      <span className="setting-desc">Default reconciliation behavior when the scheduler starts after one or more cron windows were missed.</span>
                    </div>
                    <select
                      className="settings-input"
                      value={settings.schedule_defaults.missed_run_policy || 'skip'}
                      onChange={(e) => updateScheduleDefaults('missed_run_policy', e.target.value)}
                    >
                      <option value="skip">Skip missed runs</option>
                      <option value="catch_up_once">Catch up once</option>
                      <option value="catch_up_all">Catch up all</option>
                      <option value="require_operator_decision">Require operator decision</option>
                    </select>
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Blackout Window</span>
                      <span className="setting-desc">Optional default daily no-run range. Schedules apply it in their selected timezone.</span>
                    </div>
                    <div className="inline-create-row">
                      <input
                        className="settings-input"
                        value={settings.schedule_defaults.blackout_start_time || ''}
                        onChange={(e) => updateScheduleDefaults('blackout_start_time', e.target.value)}
                        placeholder="00:00"
                      />
                      <input
                        className="settings-input"
                        value={settings.schedule_defaults.blackout_end_time || ''}
                        onChange={(e) => updateScheduleDefaults('blackout_end_time', e.target.value)}
                        placeholder="06:00"
                      />
                    </div>
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Availability Window</span>
                      <span className="setting-desc">Optional default weekday allowed-run range. Leave blank to allow all weekdays.</span>
                    </div>
                    <div className="inline-create-row">
                      <input
                        className="settings-input"
                        value={settings.schedule_defaults.availability_start_time || ''}
                        onChange={(e) => updateScheduleDefaults('availability_start_time', e.target.value)}
                        placeholder="09:00"
                      />
                      <input
                        className="settings-input"
                        value={settings.schedule_defaults.availability_end_time || ''}
                        onChange={(e) => updateScheduleDefaults('availability_end_time', e.target.value)}
                        placeholder="17:00"
                      />
                    </div>
                  </div>
                </div>
              )}
              <div className="settings-subsection">
                <div className="settings-manager-summary">
                  <div>
                    <span className="setting-label">Service Account API Keys</span>
                    <span className="setting-desc">Generate, rotate, disable, and delete scoped API keys from Access & API Keys. Settings keeps the auth mode, role contract, and audit evidence here.</span>
                  </div>
                  <strong>{serviceAccounts.length}</strong>
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Provisioning Workspace</span>
                    <span className="setting-desc">Use the dedicated Access page for CI, MCP, reporting, script import, and system automation key generation.</span>
                  </div>
                  <div className="settings-inline-actions">
                    <button className="btn btn-secondary btn-accent-action" type="button" onClick={() => onNavigate?.('access')} disabled={!canManageServiceAccounts || !onNavigate}>
                      Open Access & API Keys
                    </button>
                  </div>
                </div>
                {!canManageServiceAccounts && (
                  <p className="settings-notice">Current session can view auth settings but cannot manage service-account API keys.</p>
                )}
              </div>
                  <div className="settings-subsection">
                    <div className="settings-manager-summary">
                      <div>
                        <span className="setting-label">Recent Audit Events</span>
                        <span className="setting-desc">Backend-recorded governance history for settings changes, project administration, service-account provisioning, and core test control actions.</span>
                      </div>
                      <strong>{auditEvents.length}</strong>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Audit Filters</span>
                        <span className="setting-desc">Narrow the recent audit feed and CSV export by action, actor type, or status.</span>
                      </div>
                      <div className="settings-inline-actions">
                        <input className="settings-input cell-mono" value={auditActionFilter} onChange={(e) => setAuditActionFilter(e.target.value)} placeholder="action" />
                        <input className="settings-input cell-mono" value={auditActorTypeFilter} onChange={(e) => setAuditActorTypeFilter(e.target.value)} placeholder="actor_type" />
                        <input className="settings-input cell-mono" value={auditStatusFilter} onChange={(e) => setAuditStatusFilter(e.target.value)} placeholder="status" />
                        <button className="btn btn-secondary" type="button" onClick={exportAuditEvents} disabled={!canViewAudit}>
                          Export CSV
                        </button>
                      </div>
                    </div>
                    {(auditActionMessage || auditActionError) && (
                      <div className={`settings-alert ${auditActionError ? 'error' : 'success'}`}>
                        {auditActionError || auditActionMessage}
                      </div>
                    )}
                    {auditEvents.length > 0 ? auditEvents.map((event) => (
                      <div className="setting-row" key={event.id}>
                        <div className="setting-info">
                          <span className="setting-label">{event.action}</span>
                          <span className="setting-desc">{formatAuditActor(event)} · {event.actor_type} · {event.status}</span>
                          <span className="setting-desc">Resource: {event.resource_type}{event.resource_id ? `/${event.resource_id}` : ''}{event.project_id ? ` | Project: ${event.project_id}` : ''}</span>
                          <span className="setting-desc">Recorded: {formatAuditTimestamp(event.created_at)}</span>
                        </div>
                      </div>
                    )) : (
                      <div className="setting-row">
                        <div className="setting-info">
                          <span className="setting-label">No audit events yet</span>
                          <span className="setting-desc">Audit history appears here after configuration, project, or execution lifecycle changes.</span>
                        </div>
                      </div>
                    )}
                  </div>
              {settings.auth.mode !== 'dev' && (
                <>
                  {authRoles.length > 0 && (
                    <div className="settings-subsection">
                      <div className="settings-manager-summary">
                        <div>
                          <span className="setting-label">Built-In Role Reference</span>
                          <span className="setting-desc">APEX Load applies these roles through the request RBAC matrix regardless of whether identity arrives from trusted headers or a native OIDC session.</span>
                        </div>
                        <strong>{authRoles.length} roles</strong>
                      </div>
                      {authRoles.map((role) => (
                        <div className="setting-row" key={role.id}>
                          <div className="setting-info">
                            <span className="setting-label">{role.name}</span>
                            <span className="setting-desc">{role.description}</span>
                          </div>
                          <span className="settings-value-display">{formatRoleScope(role.scope)}{role.capabilities && role.capabilities.length > 0 ? ` | ${role.capabilities.join(', ')}` : ''}</span>
                        </div>
                      ))}
                    </div>
                  )}
                  {authAccessResources.length > 0 && (
                    <div className="settings-subsection">
                      <div className="settings-manager-summary">
                        <div>
                          <span className="setting-label">Route Access Matrix</span>
                          <span className="setting-desc">Backend-owned summary of which roles can read or mutate each major platform surface.</span>
                        </div>
                        <strong>{authAccessResources.length} resources</strong>
                      </div>
                      {authAccessResources.map((resource) => (
                        <div className="setting-row" key={resource.id}>
                          <div className="setting-info">
                            <span className="setting-label">{resource.name}</span>
                            <span className="setting-desc">{resource.description}</span>
                            {resource.operations.map((operation) => (
                              <span className="setting-desc" key={operation.id}>{operation.label}: {formatAccessOperation(operation)}</span>
                            ))}
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                  {effectiveAuthAccessResources.length > 0 && (
                    <div className="settings-subsection">
                      <div className="settings-manager-summary">
                        <div>
                          <span className="setting-label">Effective Access For Current Session</span>
                          <span className="setting-desc">Evaluates the current interactive identity against the active project selection and the backend RBAC policy.</span>
                        </div>
                        <strong>{activeProject?.name || 'Global'}</strong>
                      </div>
                      {effectiveAuthAccessResources.map((resource) => (
                        <div className="setting-row" key={resource.id}>
                          <div className="setting-info">
                            <span className="setting-label">{resource.name}</span>
                            <span className="setting-desc">{resource.description}</span>
                            {resource.operations.map((operation) => (
                              <span className="setting-desc" key={operation.id}>{operation.label}: {formatGrantSource(operation.grant_source)}</span>
                            ))}
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                </>
              )}
              </div>
            </section>
          </div>
        </div>

        <div
          id="settings-section-thresholds"
          className={sectionClass('thresholds')}
          hidden={activeSection !== 'thresholds'}
          draggable={customizingSections}
          onDragStart={event => handleSectionDragStart(event, 'thresholds')}
          onDragOver={event => handleSectionDragOver(event, 'thresholds')}
          onDrop={event => handleSectionDrop(event, 'thresholds')}
          onDragEnd={() => setDraggedSectionId(null)}
          style={{ order: getSectionOrder('thresholds') }}
        >
          <div className="settings-group">
            <div className="settings-subgroup-header">
              <span className="settings-subgroup-title">Performance SLAs</span>
              <span className="settings-subgroup-desc">Alert and auto-stop thresholds for transaction-level quality gates</span>
            </div>
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
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Avg Server Processing (ms)</span>
                <span className="setting-desc">Optional: alert threshold for the worst transaction server-processing average</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.thresholds.max_avg_server_processing_ms ?? ''}
                onChange={(e) => updateThreshold('max_avg_server_processing_ms', e.target.value)}
                placeholder="Not configured"
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Avg Transfer Time (ms)</span>
                <span className="setting-desc">Optional: alert threshold for the worst transaction transfer average</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.thresholds.max_avg_transfer_ms ?? ''}
                onChange={(e) => updateThreshold('max_avg_transfer_ms', e.target.value)}
                placeholder="Not configured"
              />
            </div>
            <div className="settings-subgroup-header">
              <span className="settings-subgroup-title">Telemetry Pipeline</span>
              <span className="settings-subgroup-desc">Time budgets for each stage of the live metrics pipeline: generator capture, transport, controller fan-out, and browser render</span>
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Telemetry Sample Age (ms)</span>
                <span className="setting-desc">End-to-end live telemetry budget from collection through controller publish</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.thresholds.max_telemetry_sample_age_ms ?? ''}
                onChange={(e) => updateThreshold('max_telemetry_sample_age_ms', e.target.value)}
                placeholder="2000"
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Generator Lag (ms)</span>
                <span className="setting-desc">Budget for generator-side capture, aggregation, and serialization</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.thresholds.max_telemetry_generator_lag_ms ?? ''}
                onChange={(e) => updateThreshold('max_telemetry_generator_lag_ms', e.target.value)}
                placeholder="500"
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Transport Lag (ms)</span>
                <span className="setting-desc">Budget for generator-to-controller delivery</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.thresholds.max_telemetry_transport_lag_ms ?? ''}
                onChange={(e) => updateThreshold('max_telemetry_transport_lag_ms', e.target.value)}
                placeholder="500"
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Controller Fan-out (ms)</span>
                <span className="setting-desc">Budget for controller receipt through websocket publish</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.thresholds.max_telemetry_controller_fanout_ms ?? ''}
                onChange={(e) => updateThreshold('max_telemetry_controller_fanout_ms', e.target.value)}
                placeholder="500"
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Browser Render Lag (ms)</span>
                <span className="setting-desc">Budget reserved for dashboard client receive-to-render delay</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.thresholds.max_telemetry_browser_render_lag_ms ?? ''}
                onChange={(e) => updateThreshold('max_telemetry_browser_render_lag_ms', e.target.value)}
                placeholder="500"
              />
            </div>
          </div>
        </div>

        <div
          id="settings-section-capacity"
          className={sectionClass('capacity')}
          hidden={activeSection !== 'capacity'}
          draggable={customizingSections}
          onDragStart={event => handleSectionDragStart(event, 'capacity')}
          onDragOver={event => handleSectionDragOver(event, 'capacity')}
          onDrop={event => handleSectionDrop(event, 'capacity')}
          onDragEnd={() => setDraggedSectionId(null)}
          style={{ order: getSectionOrder('capacity') }}
        >
          <div className="settings-subsection-layout">
            {renderSettingsSubnav(
              'Capacity settings categories',
              'settings-capacity',
              CAPACITY_SETTINGS_SUBSECTIONS,
              activeCapacitySubsection,
              setActiveCapacitySubsection,
              capacitySubsectionSummaries,
            )}
            <section
              id="settings-capacity-panel-availability"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-capacity-tab-availability"
              hidden={activeCapacitySubsection !== 'availability'}
            >
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
            </section>

            <section
              id="settings-capacity-panel-generator"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-capacity-tab-generator"
              hidden={activeCapacitySubsection !== 'generator'}
            >
              <div className="settings-group">
            <div className="settings-subsection">
              <div>
                <span className="setting-label">Generator Presets</span>
                <span className="setting-desc">Apply common sizing defaults, then tune individual fields below.</span>
              </div>
              <div className="settings-preset-row">
                {GENERATOR_PRESETS.map(preset => (
                  <button key={preset.id} type="button" className="btn btn-secondary" onClick={() => applyGeneratorPreset(preset)}>
                    {preset.label}
                  </button>
                ))}
                <button type="button" className="btn btn-secondary" onClick={applyLiveGeneratorPreset}>
                  Live Pod Capacity
                </button>
              </div>
              <p className="setting-desc">Live Pod Capacity uses connected generators' reported pod CPU, pod memory, VUser capacity, and count from the controller inventory. Network remains manual for now.</p>
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
              </div>
            </section>

            <section
              id="settings-capacity-panel-reservations"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-capacity-tab-reservations"
              hidden={activeCapacitySubsection !== 'reservations'}
            >
              <div className="settings-group">
            <div className="settings-subsection" data-testid="capacity-reservations">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Reserved Capacity Pools</span>
                  <span className="setting-desc">Reserve generators for a project so other tenants are bounded to the shared pool. Add a time window to make it a timeslot; leave blank for an always-active reservation.</span>
                </div>
                <strong>{(settings.capacity_planning.reservations ?? []).length}</strong>
              </div>
              {(settings.capacity_planning.reservations ?? []).map((reservation, index) => (
                <div key={`reservation-${index}`} className="capacity-reservation-row setting-row">
                  <input
                    className="settings-input"
                    placeholder="Project ID"
                    aria-label="Reservation project"
                    value={reservation.project_id}
                    onChange={event => updateReservation(index, { project_id: event.target.value })}
                  />
                  <input
                    className="settings-input"
                    inputMode="numeric"
                    placeholder="Generators"
                    aria-label="Reserved generators"
                    value={String(reservation.reserved_generators)}
                    onChange={event => updateReservation(index, { reserved_generators: Math.max(0, Number(event.target.value) || 0) })}
                  />
                  <input
                    className="settings-input"
                    type="datetime-local"
                    aria-label="Reservation start"
                    value={toDatetimeLocal(reservation.starts_at)}
                    onChange={event => updateReservation(index, { starts_at: fromDatetimeLocal(event.target.value) })}
                  />
                  <input
                    className="settings-input"
                    type="datetime-local"
                    aria-label="Reservation end"
                    value={toDatetimeLocal(reservation.ends_at)}
                    onChange={event => updateReservation(index, { ends_at: fromDatetimeLocal(event.target.value) })}
                  />
                  <button className="btn btn-ghost" type="button" onClick={() => removeReservation(index)}>Remove</button>
                </div>
              ))}
              <button className="btn btn-secondary" type="button" onClick={addReservation}>Add Reservation</button>
            </div>
              </div>
            </section>

            <section
              id="settings-capacity-panel-timeslots"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-capacity-tab-timeslots"
              hidden={activeCapacitySubsection !== 'timeslots'}
            >
              <div className="settings-group">
                <div className="settings-subsection" data-testid="capacity-timeslots">
                  <div className="settings-manager-summary">
                    <div>
                      <span className="setting-label">Forward-Booked Capacity</span>
                      <span className="setting-desc">Reserve a future window of generator capacity for a planned run, with approval and test binding. Distinct from the always-on reserved pools above.</span>
                    </div>
                  </div>
                  <TimeslotsPanel projectId={activeProject?.id} canManage={canManageSystem} />
                </div>
              </div>
            </section>

            <section
              id="settings-capacity-panel-retention"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-capacity-tab-retention"
              hidden={activeCapacitySubsection !== 'retention'}
            >
              <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Archive Retention Executor</span>
                  <span className="setting-desc">Plan and apply audited archive pruning through the same retention executor used by the API.</span>
                </div>
                <strong>{archiveRetentionPlan ? archiveRetentionPlan.summary.candidate_count : '-'}</strong>
              </div>
              <div className="setting-row">
                <div className="setting-info">
                  <span className="setting-label">Retention Enabled</span>
                  <span className="setting-desc">When enabled, dry-run and apply operations evaluate archived runs against the saved retention policy.</span>
                </div>
                <label className="settings-checkbox-label">
                  <input
                    type="checkbox"
                    checked={settings.archive_retention.enabled}
                    onChange={event => updateArchiveRetention('enabled', event.target.checked)}
                  />
                  Enabled
                </label>
              </div>
              <div className="setting-row">
                <div className="setting-info">
                  <span className="setting-label">Default Retention Days</span>
                  <span className="setting-desc">Archives older than this policy window become prune candidates unless a scoped policy matches first.</span>
                </div>
                <input
                  className="settings-input settings-input-number"
                  type="number"
                  min={1}
                  step={1}
                  value={settings.archive_retention.default_retention_days}
                  onChange={event => updateArchiveRetention('default_retention_days', event.target.value)}
                />
              </div>
              <div className="settings-inline-actions">
                <button className="btn btn-secondary" type="button" onClick={planArchiveRetention} disabled={!canManageSystem}>
                  Plan Retention
                </button>
                <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} type="button" onClick={() => setConfirmApplyRetention(true)} disabled={!canManageSystem || !archiveRetentionPlan?.summary.candidate_count}>
                  Apply Retention
                </button>
              </div>
              {archiveRetentionActionMessage && <p className="settings-notice">{archiveRetentionActionMessage}</p>}
              {archiveRetentionActionError && <p className="settings-error-inline">{archiveRetentionActionError}</p>}
              {archiveRetentionPlan && (
                <div className="settings-release-list">
                  {archiveRetentionPlan.candidates.slice(0, 5).map(candidate => (
                    <div key={`${candidate.test_id}-${candidate.archive_path}`} className="settings-release-row">
                      <div className="settings-release-main">
                        <span>{candidate.test_id}</span>
                        <small>{candidate.policy_name || 'default'} / {candidate.artifact_type} / {candidate.compliance_class || 'standard'} / {candidate.reason || 'expired_by_policy'} / expired {new Date(candidate.expired_at).toLocaleDateString()}</small>
                      </div>
                      <span className="setting-value">{candidate.retention_days} days</span>
                    </div>
                  ))}
                  {archiveRetentionPlan.candidates.length > 5 && (
                    <p className="new-test-inline-note">{archiveRetentionPlan.candidates.length - 5} additional candidate{archiveRetentionPlan.candidates.length - 5 === 1 ? '' : 's'} hidden.</p>
                  )}
                  {archiveRetentionPlan.results?.length ? (
                    <p className="new-test-inline-note">{archiveRetentionPlan.summary.pruned_count} pruned / {archiveRetentionPlan.summary.failed_count} failed.</p>
                  ) : null}
                </div>
              )}
            </div>
              </div>
            </section>
          </div>
        </div>

        <div
          id="settings-section-domains"
          className={sectionClass('domains')}
          hidden={activeSection !== 'domains'}
          draggable={customizingSections}
          onDragStart={event => handleSectionDragStart(event, 'domains')}
          onDragOver={event => handleSectionDragOver(event, 'domains')}
          onDrop={event => handleSectionDrop(event, 'domains')}
          onDragEnd={() => setDraggedSectionId(null)}
          style={{ order: getSectionOrder('domains') }}
        >
          <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Domains</span>
                  <span className="setting-desc">Select the active tenancy boundary for repository projects and access scopes.</span>
                </div>
                <strong>{domains.length}</strong>
              </div>
              {domains.length > 0 ? (
                <div className="settings-app-grid">
                  {domains.map(domain => {
                    const projectCount = projectCountByDomain.get(domain.id) || 0
                    return (
                      <button
                        key={domain.id}
                        type="button"
                        className={`settings-app-card ${selectedDomain?.id === domain.id ? 'active' : ''}`}
                        onClick={() => selectDomain(domain)}
                      >
                        <span>{domain.name}</span>
                        <small>{projectCount} project{projectCount === 1 ? '' : 's'}{activeDomain?.id === domain.id ? ' | Active domain' : ''}</small>
                      </button>
                    )
                  })}
                </div>
              ) : (
                <p className="new-test-inline-note">No domains are available.</p>
              )}
              <div className="inline-create-row" style={{ gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr) auto' }}>
                <input
                  className="settings-input"
                  value={newDomainName}
                  onChange={event => {
                    setNewDomainName(event.target.value)
                    setDomainActionError('')
                    setDomainActionMessage('')
                  }}
                  placeholder="Create domain, e.g. Retail"
                  disabled={!canManageDomains}
                />
                <input
                  className="settings-input"
                  value={newDomainDescription}
                  onChange={event => {
                    setNewDomainDescription(event.target.value)
                    setDomainActionError('')
                    setDomainActionMessage('')
                  }}
                  placeholder="Optional description"
                  disabled={!canManageDomains}
                />
                <button className="btn btn-secondary" type="button" onClick={createDomain} disabled={!newDomainName.trim() || !canManageDomains}>
                  Add Domain
                </button>
              </div>
            </div>

            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Selected Domain</span>
                <span className="setting-desc">Rename the selected domain and maintain its administrative description.</span>
              </div>
              {selectedDomain ? (
                <>
                  <input
                    className="settings-input"
                    value={domainDraftName}
                    onChange={event => {
                      setDomainDraftName(event.target.value)
                      setDomainActionError('')
                      setDomainActionMessage('')
                    }}
                    placeholder="Domain name"
                    disabled={!canManageDomains}
                  />
                  <textarea
                    className="settings-input"
                    value={domainDraftDescription}
                    onChange={event => {
                      setDomainDraftDescription(event.target.value)
                      setDomainActionError('')
                      setDomainActionMessage('')
                    }}
                    placeholder="Optional description"
                    rows={3}
                    disabled={!canManageDomains}
                  />
                  {domainDraftValidationError && <p className="settings-error-inline">{domainDraftValidationError}</p>}
                  {!canManageDomains && <p className="settings-error-inline">Current session can view domains but cannot change them.</p>}
                  <div className="settings-inline-actions">
                    <button
                      className="btn btn-primary"
                      type="button"
                      onClick={saveDomain}
                      disabled={!domainHasUnsavedChanges || Boolean(domainDraftValidationError) || !canManageDomains}
                    >
                      Save Domain
                    </button>
                  </div>
                </>
              ) : (
                <p className="new-test-inline-note">Select a domain to edit it.</p>
              )}
            </div>

            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Delete Domain</span>
                <span className="setting-desc">Only non-default domains with no projects can be deleted.</span>
              </div>
              <div className="settings-inline-actions">
                <span className="setting-value">{selectedDomain?.name || 'No domain selected'}</span>
                <button
                  className="btn btn-ghost"
                  style={{ color: 'var(--danger)' }}
                  type="button"
                  onClick={() => selectedDomain && setConfirmDeleteDomain(selectedDomain)}
                  disabled={!selectedDomain || selectedDomain.id === 'default' || selectedDomainProjectCount > 0 || !canManageDomains}
                >
                  Delete
                </button>
              </div>
              {selectedDomain?.id === 'default' && <p className="new-test-inline-note">Default domain cannot be deleted.</p>}
              {selectedDomain && selectedDomain.id !== 'default' && selectedDomainProjectCount > 0 && (
                <p className="new-test-inline-note">
                  {selectedDomainProjectCount} project{selectedDomainProjectCount === 1 ? '' : 's'} must be moved or deleted first.
                </p>
              )}
              {confirmDeleteDomain && (
                <div className="settings-inline-actions">
                  <span className="settings-error-inline">Delete {confirmDeleteDomain.name}?</span>
                  <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} type="button" onClick={() => deleteDomain(confirmDeleteDomain)}>
                    Confirm Delete
                  </button>
                  <button className="btn btn-secondary" type="button" onClick={() => setConfirmDeleteDomain(null)}>
                    Cancel
                  </button>
                </div>
              )}
            </div>
          </div>
        </div>

        <div
          id="settings-section-applications"
          className={sectionClass('applications')}
          hidden={activeSection !== 'applications'}
          draggable={customizingSections}
          onDragStart={event => handleSectionDragStart(event, 'applications')}
          onDragOver={event => handleSectionDragOver(event, 'applications')}
          onDrop={event => handleSectionDrop(event, 'applications')}
          onDragEnd={() => setDraggedSectionId(null)}
          style={{ order: getSectionOrder('applications') }}
        >
          <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Applications</span>
                  <span className="setting-desc">Select one to manage its releases and rename or remove it.</span>
                </div>
                <strong>{applications.length}</strong>
              </div>
              <div className="settings-app-grid">
                {applications.map(application => (
                  <button
                    key={application.id}
                    type="button"
                    className={`settings-app-card ${activeApplication?.id === application.id ? 'active' : ''}`}
                    onClick={() => setActiveApplication(application)}
                  >
                  <span>{application.name}</span>
                  <small>{application.id === activeApplication?.id ? 'Current application' : 'Switch context'}</small>
                </button>
              ))}
              </div>
              <div className="inline-create-row">
                <input
                  className="settings-input"
                  value={newApplicationName}
                  onChange={event => setNewApplicationName(event.target.value)}
                  placeholder="Create application, e.g. ACC"
                />
                <button className="btn btn-secondary" type="button" onClick={createApplication} disabled={!newApplicationName.trim()}>
                  Add Application
                </button>
              </div>
            </div>

            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Current Application</span>
                <span className="setting-desc">Rename or delete the selected application.</span>
              </div>
              <div className="settings-inline-actions">
                {renamingApplicationName !== null ? (
                  <>
                    <input
                      type="text"
                      className="input"
                      value={renamingApplicationName}
                      onChange={e => setRenamingApplicationName(e.target.value)}
                      onKeyDown={e => { if (e.key === 'Enter') confirmRenameApplication(); if (e.key === 'Escape') setRenamingApplicationName(null); }}
                      autoFocus
                    />
                    <button className="btn btn-primary" type="button" onClick={confirmRenameApplication}>Save</button>
                    <button className="btn btn-secondary" type="button" onClick={() => setRenamingApplicationName(null)}>Cancel</button>
                  </>
                ) : (
                  <>
                    <span className="setting-value">{activeApplication?.name || 'No application selected'}</span>
                    <button className="btn btn-secondary" type="button" onClick={startRenamingApplication} disabled={!activeApplication}>
                      Rename
                    </button>
                    <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} type="button" onClick={() => setConfirmDeleteApp(true)} disabled={!activeApplication}>
                      Delete
                    </button>
                  </>
                )}
              </div>
            </div>

            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Releases</span>
                  <span className="setting-desc">These belong to {activeApplication?.name || 'the selected application'}. Create new releases from the New Test page.</span>
                </div>
                <strong>{managedReleases.length}</strong>
              </div>
              {managedReleases.length > 0 ? (
                <div className="settings-release-list">
                  {managedReleases.map(release => (
                    <div key={release.id} className={`settings-release-row ${!release.archived_at && activeRelease?.id === release.id ? 'active' : ''}`}>
                      <button
                        type="button"
                        className="settings-release-main"
                        onClick={() => !release.archived_at && setActiveRelease(release)}
                        disabled={Boolean(release.archived_at)}
                      >
                        <span>{release.name}</span>
                        <small>
                          {release.archived_at
                            ? `Archived ${new Date(release.archived_at).toLocaleDateString()}`
                            : activeRelease?.id === release.id
                              ? 'Current release'
                              : 'Set active release'}
                        </small>
                      </button>
                      <div className="settings-inline-actions">
                        {renamingReleaseId === release.id ? (
                          <>
                            <input
                              type="text"
                              className="input"
                              value={renamingReleaseName}
                              onChange={e => setRenamingReleaseName(e.target.value)}
                              onKeyDown={e => { if (e.key === 'Enter') confirmRenameRelease(); if (e.key === 'Escape') { setRenamingReleaseId(null); setRenamingReleaseName(''); } }}
                              autoFocus
                            />
                            <button className="btn btn-primary" type="button" onClick={confirmRenameRelease}>Save</button>
                            <button className="btn btn-secondary" type="button" onClick={() => { setRenamingReleaseId(null); setRenamingReleaseName(''); }}>Cancel</button>
                          </>
                        ) : (
                          <>
                            <button className="btn btn-secondary" type="button" onClick={() => startRenamingRelease(release.id, release.name)}>
                              Rename
                            </button>
                            {!release.archived_at && (
                              <button className="btn btn-secondary" type="button" onClick={() => setConfirmArchiveRelease({ id: release.id, name: release.name })}>
                                Archive
                              </button>
                            )}
                            <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} type="button" onClick={() => setConfirmDeleteRelease({ id: release.id, name: release.name })}>
                              Delete
                            </button>
                          </>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="new-test-inline-note">No releases yet for this application.</p>
              )}
            </div>
          </div>
        </div>

        <div
          id="settings-section-projects"
          className={sectionClass('projects')}
          hidden={activeSection !== 'projects'}
          draggable={customizingSections}
          onDragStart={event => handleSectionDragStart(event, 'projects')}
          onDragOver={event => handleSectionDragOver(event, 'projects')}
          onDrop={event => handleSectionDrop(event, 'projects')}
          onDragEnd={() => setDraggedSectionId(null)}
          style={{ order: getSectionOrder('projects') }}
        >
          <div className="settings-subsection-layout">
            {renderSettingsSubnav(
              'Project settings categories',
              'settings-projects',
              PROJECT_SETTINGS_SUBSECTIONS,
              activeProjectSubsection,
              setActiveProjectSubsection,
              projectSubsectionSummaries,
            )}
            <section
              id="settings-projects-panel-catalog"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-projects-tab-catalog"
              hidden={activeProjectSubsection !== 'catalog'}
            >
              <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Projects</span>
                  <span className="setting-desc">Compliance defaults apply automatically to tests and replay sessions that resolve this project by `project_id`.</span>
                </div>
                <strong>{projects.length}</strong>
              </div>
              {projects.length > 0 ? (
                <div className="settings-app-grid">
                  {projects.map(project => (
                    <button
                      key={project.id}
                      type="button"
                      className={`settings-app-card ${selectedProject?.id === project.id ? 'active' : ''}`}
                      onClick={() => selectProject(project)}
                    >
                      <span>{project.name}</span>
                      <small>
                        {selectedProject?.id === project.id
                          ? 'Editing compliance defaults'
                          : `${project.settings?.redaction_rules?.length || 0} custom rule${project.settings?.redaction_rules?.length === 1 ? '' : 's'}`}
                      </small>
                    </button>
                  ))}
                </div>
              ) : (
                <p className="new-test-inline-note">No projects yet. Create one to define project-specific redaction defaults.</p>
              )}
              <div className="inline-create-row">
                <input
                  className="settings-input"
                  value={newProjectName}
                  onChange={event => setNewProjectName(event.target.value)}
                  placeholder="Create project, e.g. Customer Portal"
                />
                <button className="btn btn-secondary" type="button" onClick={createProject} disabled={!newProjectName.trim() || !canManageProjects}>
                  Add Project
                </button>
              </div>
            </div>
              </div>
            </section>

            {selectedProject ? (
              <>
                <section
                  id="settings-projects-panel-limits"
                  className="settings-subpanel"
                  role="tabpanel"
                  aria-labelledby="settings-projects-tab-limits"
                  hidden={activeProjectSubsection !== 'limits'}
                >
                  <div className="settings-group">
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Selected Project</span>
                    <span className="setting-desc">Built-in token and PII scrubbing still runs globally. Add regex rules here only for project-specific payload shapes.</span>
                  </div>
                  <input
                    className="settings-input"
                    value={projectDraftName}
                    onChange={event => {
                      setProjectDraftName(event.target.value)
                      setProjectActionError('')
                      setProjectActionMessage('')
                    }}
                    placeholder="Project name"
                  />
                </div>

                <div className="settings-subsection">
                  <div className="settings-manager-summary">
                    <div>
                      <span className="setting-label">Project Limits</span>
                      <span className="setting-desc">Optional project-level caps that reject over-sized runs before execution starts.</span>
                    </div>
                    <strong>{selectedProject.name}</strong>
                  </div>

                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Project Max Concurrent Tests</span>
                      <span className="setting-desc">Leave blank or set to 0 for no project-level concurrency cap.</span>
                    </div>
                    <input
                      className="settings-input"
                      inputMode="numeric"
                      value={projectDraftMaxConcurrentTests}
                      onChange={event => {
                        setProjectDraftMaxConcurrentTests(event.target.value)
                        setProjectActionError('')
                        setProjectActionMessage('')
                      }}
                      placeholder="Unlimited"
                    />
                  </div>

                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Project Max VUsers</span>
                      <span className="setting-desc">Leave blank or set to 0 for no project-level VUser cap.</span>
                    </div>
                    <input
                      className="settings-input"
                      inputMode="numeric"
                      value={projectDraftMaxVUsers}
                      onChange={event => {
                        setProjectDraftMaxVUsers(event.target.value)
                        setProjectActionError('')
                        setProjectActionMessage('')
                      }}
                      placeholder="Unlimited"
                    />
                  </div>

                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Project Max VUsers Per Test</span>
                      <span className="setting-desc">Reject any single test requesting more than this many VUsers. Blank or 0 = no per-test cap.</span>
                    </div>
                    <input
                      className="settings-input"
                      inputMode="numeric"
                      value={projectDraftMaxVUsersPerTest}
                      onChange={event => {
                        setProjectDraftMaxVUsersPerTest(event.target.value)
                        setProjectActionError('')
                        setProjectActionMessage('')
                      }}
                      placeholder="Unlimited"
                    />
                  </div>

                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Project Max Test Duration (minutes)</span>
                      <span className="setting-desc">Reject any single test longer than this many minutes. Blank or 0 = no duration cap.</span>
                    </div>
                    <input
                      className="settings-input"
                      inputMode="numeric"
                      value={projectDraftMaxTestDurationMinutes}
                      onChange={event => {
                        setProjectDraftMaxTestDurationMinutes(event.target.value)
                        setProjectActionError('')
                        setProjectActionMessage('')
                      }}
                      placeholder="Unlimited"
                    />
                  </div>
                </div>
                  </div>
                </section>

                <section
                  id="settings-projects-panel-github"
                  className="settings-subpanel"
                  role="tabpanel"
                  aria-labelledby="settings-projects-tab-github"
                  hidden={activeProjectSubsection !== 'github'}
                >
                  <div className="settings-group">
                <div className="settings-subsection">
                  <div className="settings-manager-summary" style={{ marginTop: 16 }}>
                    <div>
                      <span className="setting-label">GitHub Project Policy</span>
                      <span className="setting-desc">Repository, branch, path, and automation rules enforced by the controller for GitHub sync actions in this project.</span>
                    </div>
                    <span className={`script-health-token ${projectDraftGitHubEnabled ? 'healthy' : ''}`}>
                      {projectDraftGitHubEnabled ? 'enabled' : 'disabled'}
                    </span>
                  </div>
                  <div className="project-redaction-rule-grid">
                    <label className="project-redaction-rule-field" style={{ flexDirection: 'row', alignItems: 'center', gap: 8 }}>
                      <input
                        type="checkbox"
                        checked={projectDraftGitHubEnabled}
                        disabled={!canManageProjects}
                        onChange={event => {
                          setProjectDraftGitHubEnabled(event.target.checked)
                          setProjectActionError('')
                          setProjectActionMessage('')
                        }}
                      />
                      <span>Enable policy</span>
                    </label>
                    <label className="project-redaction-rule-field">
                      <span>Allowed Repositories</span>
                      <input
                        className="settings-input"
                        value={projectDraftGitHubAllowedRepos}
                        disabled={!canManageProjects}
                        onChange={event => {
                          setProjectDraftGitHubAllowedRepos(event.target.value)
                          setProjectActionError('')
                          setProjectActionMessage('')
                        }}
                        placeholder="org/perf-scripts, org/team-*"
                      />
                    </label>
                    <label className="project-redaction-rule-field">
                      <span>Allowed Branches</span>
                      <input
                        className="settings-input"
                        value={projectDraftGitHubAllowedBranches}
                        disabled={!canManageProjects}
                        onChange={event => {
                          setProjectDraftGitHubAllowedBranches(event.target.value)
                          setProjectActionError('')
                          setProjectActionMessage('')
                        }}
                        placeholder="main, release/*"
                      />
                    </label>
                    <label className="project-redaction-rule-field">
                      <span>Path Template</span>
                      <input
                        className="settings-input"
                        value={projectDraftGitHubPathTemplate}
                        disabled={!canManageProjects}
                        onChange={event => {
                          setProjectDraftGitHubPathTemplate(event.target.value)
                          setProjectActionError('')
                          setProjectActionMessage('')
                        }}
                        placeholder="apexload/scripts/{project}/{ref_key}.json"
                      />
                    </label>
                    <label className="project-redaction-rule-field">
                      <span>Default Repository</span>
                      <input
                        className="settings-input"
                        value={projectDraftGitHubDefaultRepo}
                        disabled={!canManageProjects}
                        onChange={event => {
                          setProjectDraftGitHubDefaultRepo(event.target.value)
                          setProjectActionError('')
                          setProjectActionMessage('')
                        }}
                        placeholder="org/perf-scripts"
                      />
                    </label>
                    <label className="project-redaction-rule-field">
                      <span>Default Branch</span>
                      <input
                        className="settings-input"
                        value={projectDraftGitHubDefaultBranch}
                        disabled={!canManageProjects}
                        onChange={event => {
                          setProjectDraftGitHubDefaultBranch(event.target.value)
                          setProjectActionError('')
                          setProjectActionMessage('')
                        }}
                        placeholder="main"
                      />
                    </label>
                    <label className="project-redaction-rule-field">
                      <span>Default Sync Folder</span>
                      <input
                        className="settings-input"
                        value={projectDraftGitHubSyncDirectory}
                        disabled={!canManageProjects}
                        onChange={event => {
                          setProjectDraftGitHubSyncDirectory(event.target.value)
                          setProjectActionError('')
                          setProjectActionMessage('')
                        }}
                        placeholder="apexload/scripts"
                      />
                    </label>
                    <label className="project-redaction-rule-field">
                      <span>App Installation ID</span>
                      <input
                        className="settings-input"
                        inputMode="numeric"
                        value={projectDraftGitHubAppInstallation}
                        disabled={!canManageProjects}
                        onChange={event => {
                          setProjectDraftGitHubAppInstallation(event.target.value)
                          setProjectActionError('')
                          setProjectActionMessage('')
                        }}
                        placeholder="12345678"
                      />
                    </label>
                  </div>
                  <div className="settings-inline-actions" style={{ flexWrap: 'wrap', marginTop: 8 }}>
                    {[
                      ['Require PR', projectDraftGitHubRequirePR, setProjectDraftGitHubRequirePR],
                      ['Allow direct commit', projectDraftGitHubAllowDirect, setProjectDraftGitHubAllowDirect],
                      ['Allow import', projectDraftGitHubAllowImport, setProjectDraftGitHubAllowImport],
                      ['Allow push', projectDraftGitHubAllowPush, setProjectDraftGitHubAllowPush],
                      ['Bulk sync', projectDraftGitHubBulkSync, setProjectDraftGitHubBulkSync],
                      ['Webhook auto-sync', projectDraftGitHubAutoSync, setProjectDraftGitHubAutoSync],
                      ['Publish checks', projectDraftGitHubChecks, setProjectDraftGitHubChecks],
                    ].map(([label, checked, setter]) => (
                      <label key={label as string} className="project-redaction-rule-field" style={{ flexDirection: 'row', alignItems: 'center', gap: 8, minWidth: 150 }}>
                        <input
                          type="checkbox"
                          checked={checked as boolean}
                          disabled={!canManageProjects}
                          onChange={event => {
                            const applyChecked = setter as Dispatch<SetStateAction<boolean>>
                            applyChecked(event.target.checked)
                            setProjectActionError('')
                            setProjectActionMessage('')
                          }}
                        />
                        <span>{label as string}</span>
                      </label>
                    ))}
                  </div>
                </div>
                  </div>
                </section>

                <section
                  id="settings-projects-panel-redaction"
                  className="settings-subpanel"
                  role="tabpanel"
                  aria-labelledby="settings-projects-tab-redaction"
                  hidden={activeProjectSubsection !== 'redaction'}
                >
                  <div className="settings-group">
                <div className="settings-subsection">
                  <div className="settings-manager-summary">
                    <div>
                      <span className="setting-label">Custom Redaction Rules</span>
                      <span className="setting-desc">Each regex runs after the built-in scrubbing pass. Leave replacement empty to fall back to the engine default `[REDACTED]` token.</span>
                    </div>
                    <strong>{customRuleCount}</strong>
                  </div>
                  {projectDraftRules.length > 0 ? (
                    <div className="project-redaction-rule-list">
                      {projectDraftRules.map((rule, index) => (
                        <div key={`${selectedProject.id}-rule-${index}`} className="project-redaction-rule-card">
                          <div className="project-redaction-rule-header">
                            <strong>Rule {index + 1}</strong>
                            <button className="btn btn-ghost" type="button" onClick={() => removeProjectRule(index)}>
                              Remove
                            </button>
                          </div>
                          <div className="project-redaction-rule-grid">
                            <label className="project-redaction-rule-field">
                              <span>Name</span>
                              <input
                                className="settings-input"
                                value={rule.name}
                                onChange={event => updateProjectRule(index, 'name', event.target.value)}
                                placeholder="Optional label"
                              />
                            </label>
                            <label className="project-redaction-rule-field">
                              <span>Replacement</span>
                              <input
                                className="settings-input"
                                value={rule.replacement}
                                onChange={event => updateProjectRule(index, 'replacement', event.target.value)}
                                placeholder="Defaults to [REDACTED]"
                              />
                            </label>
                          </div>
                          <label className="project-redaction-rule-field">
                            <span>Regex Pattern</span>
                            <textarea
                              className="settings-input project-redaction-rule-pattern"
                              rows={3}
                              value={rule.pattern}
                              onChange={event => updateProjectRule(index, 'pattern', event.target.value)}
                              placeholder="customer-[0-9]+"
                            />
                          </label>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <p className="new-test-inline-note">No custom rules yet. Built-in header, token, SSN, MRN, and high-entropy redaction still applies.</p>
                  )}

                  <div className="settings-inline-actions">
                    <button className="btn btn-secondary" type="button" onClick={addProjectRule} disabled={!canManageProjects}>
                      Add Rule
                    </button>
                  </div>
                </div>
                  </div>
                </section>

                <div className="settings-subsection settings-subsection-actions">
                  {projectDraftValidationError && <p className="settings-error-inline">{projectDraftValidationError}</p>}
                  {!canManageProjects && <p className="settings-error-inline">Current session can view this project but cannot change project governance.</p>}
                  <div className="settings-inline-actions">
                    <button
                      className="btn btn-primary"
                      type="button"
                      onClick={saveProjectCompliance}
                      disabled={!projectHasUnsavedChanges || Boolean(projectDraftValidationError) || !canManageProjects}
                    >
                      Save Project Governance
                    </button>
                  </div>
                </div>
              </>
            ) : (
              <div className="settings-subsection settings-empty-subpanel">
                <p className="new-test-inline-note">Select a project to edit its compliance defaults.</p>
              </div>
            )}
          </div>
        </div>

        <div
          id="settings-section-data-pools"
          className={sectionClass('data-pools')}
          hidden={activeSection !== 'data-pools'}
          draggable={customizingSections}
          onDragStart={event => handleSectionDragStart(event, 'data-pools')}
          onDragOver={event => handleSectionDragOver(event, 'data-pools')}
          onDrop={event => handleSectionDrop(event, 'data-pools')}
          onDragEnd={() => setDraggedSectionId(null)}
          style={{ order: getSectionOrder('data-pools') }}
        >
          <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Reusable Data</span>
                  <span className="setting-desc">Create CRUD data pools that scenarios can bind by alias and share across scripts.</span>
                </div>
                <strong>{dataPools.length}</strong>
              </div>
              <div className="inline-create-row" style={{ gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr) auto' }}>
                <input
                  className="settings-input"
                  value={dataPoolName}
                  onChange={event => setDataPoolName(event.target.value)}
                  placeholder="Pool name, e.g. Customer Accounts"
                />
                <input
                  className="settings-input"
                  value={dataPoolDescription}
                  onChange={event => setDataPoolDescription(event.target.value)}
                  placeholder="Optional description"
                />
                <button className="btn btn-secondary btn-sm" type="button" onClick={createDataPool} disabled={!dataPoolName.trim()}>
                  Add Pool
                </button>
              </div>
            </div>

            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Data Pools</span>
                  <span className="setting-desc">Select a pool to inspect and edit its rows.</span>
                </div>
                <strong>{selectedDataPoolId ? `${dataPoolRows.length} rows` : 'No pool selected'}</strong>
              </div>
              {dataPools.length > 0 ? (
                <div className="settings-release-list">
                  {dataPools.map(pool => (
                    <div key={pool.id} className={`settings-release-row ${selectedDataPoolId === pool.id ? 'active' : ''}`}>
                      <button type="button" className="settings-release-main" onClick={() => setSelectedDataPoolId(pool.id)}>
                        <span>{pool.name}</span>
                        <small>{pool.description || `Updated ${new Date(pool.updated_at).toLocaleDateString()}`}</small>
                      </button>
                      <div className="settings-inline-actions">
                        <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} type="button" onClick={() => setConfirmDeletePool(pool)}>
                          Delete
                        </button>
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="new-test-inline-note">No shared data pools have been created yet.</p>
              )}
            </div>

            {selectedDataPoolId && (
              <div className="settings-subsection">
                <div className="settings-manager-summary">
                  <div>
                    <span className="setting-label">{editingDataPoolRowId ? 'Edit Row' : 'Add Row'}</span>
                    <span className="setting-desc">Use JSON object keys as parameter names, for example customer_id, account_id, or zip.</span>
                  </div>
                  <strong>{editingDataPoolRowId ? 'Editing' : 'New'}</strong>
                </div>
                <textarea
                  className="settings-input cell-mono"
                  style={{ minHeight: 120, resize: 'vertical' }}
                  value={dataPoolRowJson}
                  onChange={event => setDataPoolRowJson(event.target.value)}
                />
                <div className="settings-inline-actions">
                  <button className="btn btn-primary" type="button" onClick={saveDataPoolRow}>
                    {editingDataPoolRowId ? 'Update Row' : 'Add Row'}
                  </button>
                  {editingDataPoolRowId && (
                    <button className="btn btn-secondary" type="button" onClick={() => { setEditingDataPoolRowId(null); setDataPoolRowJson('{\n  "customer_id": "C-10001"\n}') }}>
                      Cancel Edit
                    </button>
                  )}
                </div>

                {dataPoolRows.length > 0 ? (
                  <div className="script-parameter-table-wrap" style={{ marginTop: 16 }}>
                    <table className="script-parameter-table">
                      <thead>
                        <tr>
                          <th>Data</th>
                          <th>Status</th>
                          <th>Version</th>
                          <th>Updated</th>
                          <th>Actions</th>
                        </tr>
                      </thead>
                      <tbody>
                        {dataPoolRows.map(row => (
                          <tr key={row.id}>
                            <td className="cell-mono">{JSON.stringify(row.data)}</td>
                            <td>{row.status || 'active'}</td>
                            <td>{row.version}</td>
                            <td>{new Date(row.updated_at).toLocaleString()}</td>
                            <td>
                              <div className="settings-inline-actions">
                                <button className="btn btn-sm btn-secondary" type="button" onClick={() => editDataPoolRow(row)}>
                                  Edit
                                </button>
                                <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} type="button" onClick={() => setConfirmDeleteRow(row)}>
                                  Delete
                                </button>
                              </div>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  <p className="new-test-inline-note">No rows in this data pool yet.</p>
                )}
              </div>
            )}
          </div>
        </div>

        <div
          id="settings-section-integrations"
          className={sectionClass('integrations')}
          hidden={activeSection !== 'integrations'}
          draggable={customizingSections}
          onDragStart={event => handleSectionDragStart(event, 'integrations')}
          onDragOver={event => handleSectionDragOver(event, 'integrations')}
          onDrop={event => handleSectionDrop(event, 'integrations')}
          onDragEnd={() => setDraggedSectionId(null)}
          style={{ order: getSectionOrder('integrations') }}
        >
          <div className="settings-subsection-layout">
            {renderSettingsSubnav(
              'Integration settings categories',
              'settings-integrations',
              INTEGRATION_SETTINGS_SUBSECTIONS,
              activeIntegrationSubsection,
              setActiveIntegrationSubsection,
              integrationSubsectionSummaries,
            )}
            <section
              id="settings-integrations-panel-library"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-integrations-tab-library"
              hidden={activeIntegrationSubsection !== 'library'}
            >
              <div className="settings-group">
            <LibraryFederationPanel projectId={activeProject?.id} />
              </div>
            </section>

            <section
              id="settings-integrations-panel-github"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-integrations-tab-github"
              hidden={activeIntegrationSubsection !== 'github'}
            >
              <div className="settings-group">
            <GitHubIntegrationPanel
              value={settings.github}
              onChange={(next) => {
                setSettings((prev) => ({ ...prev, github: next }))
                setSaveMessage('')
                setRequestError('')
              }}
            />
              </div>
            </section>

            <section
              id="settings-integrations-panel-prometheus"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-integrations-tab-prometheus"
              hidden={activeIntegrationSubsection !== 'prometheus'}
            >
              <div className="settings-group">
                <div className="settings-subsection">
                  <div className="settings-manager-summary">
                    <div>
                      <span className="setting-label">Prometheus Graphs</span>
                      <span className="setting-desc">Proxy the configured Prometheus server through the backend for the curated APEX runtime graph pack.</span>
                    </div>
                    <strong>{settings.prometheus.enabled ? 'Enabled' : 'Disabled'}</strong>
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Enable Prometheus Graphs</span>
                      <span className="setting-desc">Shows the Prometheus graph category when the server URL is configured.</span>
                    </div>
                    <label className="settings-checkbox-label">
                      <input
                        type="checkbox"
                        checked={settings.prometheus.enabled}
                        onChange={event => updatePrometheus({ enabled: event.target.checked })}
                      />
                      <span>Enabled</span>
                    </label>
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Server URL</span>
                      <span className="setting-desc">Backend-only Prometheus HTTP API base URL.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.prometheus.base_url}
                      onChange={event => updatePrometheus({ base_url: event.target.value })}
                      placeholder="http://prometheus:9090"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Timeout (seconds)</span>
                      <span className="setting-desc">Maximum backend wait for each Prometheus query.</span>
                    </div>
                    <input
                      className="settings-input settings-input-number"
                      type="number"
                      min={1}
                      max={60}
                      step={1}
                      value={settings.prometheus.timeout_seconds}
                      onChange={event => updatePrometheus({ timeout_seconds: Number(event.target.value) })}
                    />
                  </div>
                </div>
              </div>
            </section>

            <section
              id="settings-integrations-panel-notifications"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-integrations-tab-notifications"
              hidden={activeIntegrationSubsection !== 'notifications'}
            >
              <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Notification Webhooks</span>
                  <span className="setting-desc">Send Slack, Microsoft Teams, or Dynatrace Events API notifications when tests or telemetry health change state.</span>
                </div>
                <strong>{notificationWebhookCount}</strong>
              </div>

              {settings.notifications.webhooks.length > 0 ? (
                <div className="settings-notification-webhook-list">
                  {settings.notifications.webhooks.map((webhook, index) => (
                    <div className="settings-notification-webhook-card" key={`${webhook.url}-${index}`}>
                      <div className="project-redaction-rule-header">
                        <div>
                          <strong>{webhook.name?.trim() || `Webhook ${index + 1}`}</strong>
                          <p className="setting-desc">All test lifecycle and telemetry health events are sent. Event filtering is supported by the backend and can be added to the UI later.</p>
                        </div>
                        <button className="btn btn-sm btn-ghost" type="button" onClick={() => removeNotificationWebhook(index)}>
                          Remove
                        </button>
                      </div>

                      <div className="project-redaction-rule-grid">
                        <label className="project-redaction-rule-field">
                          <span>Webhook Name</span>
                          <input
                            className="settings-input"
                            value={webhook.name || ''}
                            onChange={event => updateNotificationWebhook(index, { name: event.target.value })}
                            placeholder="Perf Alerts"
                          />
                        </label>
                        <label className="project-redaction-rule-field">
                          <span>Format</span>
                          <select
                            className="settings-select"
                            value={webhook.format}
                            onChange={event => updateNotificationWebhook(index, { format: event.target.value as DashboardSystemSettings['notifications']['webhooks'][number]['format'] })}
                          >
                            {NOTIFICATION_FORMAT_OPTIONS.map(option => (
                              <option key={option.value} value={option.value}>{option.label}</option>
                            ))}
                          </select>
                        </label>
                      </div>

                      <label className="project-redaction-rule-field">
                        <span>Destination URL</span>
                        <input
                          className="settings-input"
                          value={webhook.url}
                          onChange={event => updateNotificationWebhook(index, { url: event.target.value })}
                          placeholder={webhook.format === 'dynatrace' ? 'https://{environment}.live.dynatrace.com/api/v2/events/ingest' : 'https://hooks.slack.com/services/...'}
                        />
                      </label>

                      {webhook.format === 'dynatrace' && (
                        <div className="project-redaction-rule-grid">
                          <label className="project-redaction-rule-field">
                            <span>Dynatrace API Token</span>
                            <input
                              className="settings-input"
                              type="password"
                              value={webhook.api_token || ''}
                              onChange={event => updateNotificationWebhook(index, { api_token: event.target.value })}
                              placeholder="dt0c01..."
                            />
                          </label>
                          <label className="project-redaction-rule-field">
                            <span>Event Timeout (minutes)</span>
                            <input
                              className="settings-input"
                              type="number"
                              min="0"
                              max="360"
                              value={webhook.timeout_minutes ?? ''}
                              onChange={event => updateNotificationWebhook(index, {
                                timeout_minutes: event.target.value.trim() === '' ? undefined : Number(event.target.value),
                              })}
                              placeholder="15"
                            />
                          </label>
                          <label className="project-redaction-rule-field project-redaction-rule-field-wide">
                            <span>Entity Selector</span>
                            <input
                              className="settings-input"
                              value={webhook.entity_selector || ''}
                              onChange={event => updateNotificationWebhook(index, { entity_selector: event.target.value })}
                              placeholder="type(SERVICE),entityName.equals(CheckoutService)"
                            />
                          </label>
                        </div>
                      )}

                      <div className="setting-row">
                        <div className="setting-info">
                          <span className="setting-label">Enabled</span>
                          <span className="setting-desc">Disable the target without removing the stored destination.</span>
                        </div>
                        <label className="settings-toggle">
                          <input
                            type="checkbox"
                            checked={webhook.enabled}
                            onChange={event => updateNotificationWebhook(index, { enabled: event.target.checked })}
                          />
                          <span>{webhook.enabled ? 'Enabled' : 'Disabled'}</span>
                        </label>
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="new-test-inline-note">No webhook destinations configured yet.</p>
              )}

              <div className="settings-inline-actions">
                <button className="btn btn-secondary" type="button" onClick={addNotificationWebhook}>
                  Add Webhook
                </button>
              </div>
            </div>
              </div>
            </section>
          </div>
        </div>
          </div>

          <div className="settings-section-footer">
            <button className="btn btn-danger-outline btn-sm" type="button" onClick={() => setConfirmingResetAll(true)} disabled={!canManageSystem}>
              Reset All Settings to Defaults
            </button>
          </div>
        </div>
      </div>

      {confirmDeleteApp && (
        <div className="modal-overlay" onClick={() => setConfirmDeleteApp(false)}>
          <div className="modal" role="dialog" aria-modal="true" onClick={event => event.stopPropagation()}>
            <div className="modal-header">
              <h3>Delete Application</h3>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setConfirmDeleteApp(false)}>Cancel</button>
            </div>
            <div className="monitor-body">
              <p>Permanently delete <strong>{activeApplication?.name}</strong>? All associated releases and test results will also be removed.</p>
              <p style={{ color: 'var(--danger)', fontWeight: 600 }}>This action cannot be undone.</p>
              <div className="settings-inline-actions">
                <button className="btn btn-secondary" type="button" onClick={() => setConfirmDeleteApp(false)}>Cancel</button>
                <button className="btn btn-danger" type="button" onClick={deleteApplication}>Delete Application</button>
              </div>
            </div>
          </div>
        </div>
      )}

      {confirmDeleteRelease && (
        <div className="modal-overlay" onClick={() => setConfirmDeleteRelease(null)}>
          <div className="modal" role="dialog" aria-modal="true" onClick={event => event.stopPropagation()}>
            <div className="modal-header">
              <h3>Delete Release</h3>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setConfirmDeleteRelease(null)}>Cancel</button>
            </div>
            <div className="monitor-body">
              <p>Permanently delete release <strong>{confirmDeleteRelease.name}</strong>?</p>
              <p style={{ color: 'var(--danger)', fontWeight: 600 }}>This action cannot be undone.</p>
              <div className="settings-inline-actions">
                <button className="btn btn-secondary" type="button" onClick={() => setConfirmDeleteRelease(null)}>Cancel</button>
                <button className="btn btn-danger" type="button" onClick={() => deleteRelease(confirmDeleteRelease.id, confirmDeleteRelease.name)}>Delete Release</button>
              </div>
            </div>
          </div>
        </div>
      )}

      {confirmArchiveRelease && (
        <div className="modal-overlay" onClick={() => setConfirmArchiveRelease(null)}>
          <div className="modal" role="dialog" aria-modal="true" onClick={event => event.stopPropagation()}>
            <div className="modal-header">
              <h3>Archive Release</h3>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setConfirmArchiveRelease(null)}>Cancel</button>
            </div>
            <div className="monitor-body">
              <p>Archive release <strong>{confirmArchiveRelease.name}</strong>? Archived releases are read-only and cannot be used for new tests.</p>
              <div className="settings-inline-actions">
                <button className="btn btn-secondary" type="button" onClick={() => setConfirmArchiveRelease(null)}>Cancel</button>
                <button className="btn btn-primary" type="button" onClick={() => archiveRelease(confirmArchiveRelease.id, confirmArchiveRelease.name)}>Archive Release</button>
              </div>
            </div>
          </div>
        </div>
      )}

      {confirmApplyRetention && (
        <div className="modal-overlay" onClick={() => setConfirmApplyRetention(false)}>
          <div className="modal" role="dialog" aria-modal="true" onClick={event => event.stopPropagation()}>
            <div className="modal-header">
              <h3>Apply Archive Retention</h3>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setConfirmApplyRetention(false)}>Cancel</button>
            </div>
            <div className="monitor-body">
              <p>Apply archive retention pruning for the current plan? This will permanently remove {archiveRetentionPlan?.summary.candidate_count || 0} archive{(archiveRetentionPlan?.summary.candidate_count || 0) === 1 ? '' : 's'}.</p>
              <p style={{ color: 'var(--danger)', fontWeight: 600 }}>This action cannot be undone.</p>
              <div className="settings-inline-actions">
                <button className="btn btn-secondary" type="button" onClick={() => setConfirmApplyRetention(false)}>Cancel</button>
                <button className="btn btn-danger" type="button" onClick={applyArchiveRetention}>Apply Retention</button>
              </div>
            </div>
          </div>
        </div>
      )}

      {confirmDeletePool && (
        <div className="modal-overlay" onClick={() => setConfirmDeletePool(null)}>
          <div className="modal" role="dialog" aria-modal="true" onClick={event => event.stopPropagation()}>
            <div className="modal-header">
              <h3>Delete Data Pool</h3>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setConfirmDeletePool(null)}>Cancel</button>
            </div>
            <div className="monitor-body">
              <p>Delete data pool <strong>{confirmDeletePool.name}</strong>? Data pools bound to scenarios must be unbound first.</p>
              <p style={{ color: 'var(--danger)', fontWeight: 600 }}>This action cannot be undone.</p>
              <div className="settings-inline-actions">
                <button className="btn btn-secondary" type="button" onClick={() => setConfirmDeletePool(null)}>Cancel</button>
                <button className="btn btn-danger" type="button" onClick={() => deleteDataPool(confirmDeletePool)}>Delete Data Pool</button>
              </div>
            </div>
          </div>
        </div>
      )}

      {confirmDeleteRow && (
        <div className="modal-overlay" onClick={() => setConfirmDeleteRow(null)}>
          <div className="modal" role="dialog" aria-modal="true" onClick={event => event.stopPropagation()}>
            <div className="modal-header">
              <h3>Delete Data Row</h3>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setConfirmDeleteRow(null)}>Cancel</button>
            </div>
            <div className="monitor-body">
              <p>Permanently delete this data row?</p>
              <div className="settings-inline-actions">
                <button className="btn btn-secondary" type="button" onClick={() => setConfirmDeleteRow(null)}>Cancel</button>
                <button className="btn btn-danger" type="button" onClick={() => deleteDataPoolRow(confirmDeleteRow)}>Delete Row</button>
              </div>
            </div>
          </div>
        </div>
      )}

      {confirmingResetAll && (
        <div className="modal-overlay" onClick={() => setConfirmingResetAll(false)}>
          <div className="modal" role="dialog" aria-modal="true" aria-labelledby="reset-confirm-title" onClick={event => event.stopPropagation()}>
            <div className="modal-header">
              <h3 id="reset-confirm-title">Reset All Settings</h3>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setConfirmingResetAll(false)}>Cancel</button>
            </div>
            <div className="monitor-body">
              <p>This will reset every system setting to its factory default, including thresholds, capacity planning, notifications, and auth configuration.</p>
              <p style={{ color: 'var(--danger)', fontWeight: 600 }}>This action cannot be undone.</p>
              <div className="settings-inline-actions">
                <button className="btn btn-secondary" type="button" onClick={() => setConfirmingResetAll(false)}>Cancel</button>
                <button className="btn btn-danger" type="button" onClick={resetSettings}>Reset All Settings</button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
