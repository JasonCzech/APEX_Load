import { type DashboardAuthEffectiveAccessResource } from '../types'

// isEffectiveOperationAllowed reports whether the effective-access payload grants an
// operation. It defaults to DENY when the operation is absent (e.g. effective access
// has not loaded yet) so the UI never optimistically shows an admin affordance the
// backend will then 403. Pass fallback=true only where an open default is intentional.
export function isEffectiveOperationAllowed(
  resources: DashboardAuthEffectiveAccessResource[],
  operationID: string,
  fallback = false,
): boolean {
  for (const resource of resources) {
    const operation = resource.operations.find(item => item.id === operationID)
    if (operation) {
      return operation.allowed
    }
  }
  return fallback
}

export function canCreateScriptFolders(resources: DashboardAuthEffectiveAccessResource[]): boolean {
  return isEffectiveOperationAllowed(resources, 'create_script_folders') ||
    isEffectiveOperationAllowed(resources, 'manage_projects')
}
