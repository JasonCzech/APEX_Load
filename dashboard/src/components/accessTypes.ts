// Shared types for the Access page workspaces (extracted from AccessPage.tsx, A16).
import { ApplicationRepository, Domain, Project, DashboardUserRoleScopeType } from '../types'

export type AccessTab = 'users' | 'service_accounts' | 'activity'
export type UserSubTab = 'existing' | 'onboard'
export type ServiceAccountSubTab = 'existing' | 'generate'
export type ActivitySubTab = 'events' | 'usage' | 'review'
export type SortDirection = 'asc' | 'desc'
export type UserSortField = 'user' | 'status' | 'email' | 'roles' | 'access' | 'last_seen' | 'created' | 'updated' | 'usage'
export type AccountSortField = 'account' | 'status' | 'roles' | 'access' | 'scopes' | 'last_used' | 'rotation_due' | 'expires' | 'updated'
export type ActivitySortField = 'time' | 'actor' | 'action' | 'resource' | 'scope' | 'status' | 'details'
export type UsageSortField = 'user' | 'runs' | 'total_vusers' | 'duration' | 'vuser_seconds'

export type SortState<T extends string> = {
  field: T
  direction: SortDirection
}

export type AccessPageProps = {
  projects: Project[]
  domains?: Domain[]
  applicationRepository?: ApplicationRepository | null
  activeDomain?: Domain | null
  activeProject?: Project | null
  canManageUserLifecycle: boolean
  canManageUserBindings: boolean
  canViewUserActivity: boolean
  canManageServiceAccounts: boolean
  // Bumped by App when an `access_review_changed` WebSocket ping arrives, prompting a refetch.
  accessReviewRefreshSignal?: number
}

export type PurposePreset = {
  id: string
  label: string
  description: string
  defaultName: string
  globalRoles: string[]
  scopes: string[]
  projectRole?: string
}

export type ServiceAccountForm = {
  purpose: string
  name: string
  description: string
  owner: string
  contact: string
  globalRoles: string[]
  projectRoles: Record<string, string>
  domainRoles: Record<string, string>
  applicationRepositoryRoles: Record<string, string>
  scopes: string[]
  expiresAt: string
  rotationDays: string
}

export type UserForm = {
  subject: string
  email: string
  displayName: string
}

export type UserBindingForm = {
  id: string
  bindingID?: string
  scopeType: DashboardUserRoleScopeType
  scopeID: string
  role: string
}

export type UserFilterState = {
  query: string
  status: string
  role: string
  domainID: string
  projectID: string
}

export type AccountFilterState = {
  query: string
  status: string
  role: string
  scope: string
  domainID: string
  projectID: string
}

export type ActivityFilterForm = {
  actorSubject: string
  domainID: string
  projectID: string
  action: string
  status: string
  createdAfter: string
  createdBefore: string
}

export type IssuedKeyState = {
  key: string
  accountName: string
  mode: 'created' | 'rotated'
}
