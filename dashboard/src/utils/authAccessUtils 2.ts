import { type DashboardAuthEffectiveAccessResource } from '../types'

export function isEffectiveOperationAllowed(
  resources: DashboardAuthEffectiveAccessResource[],
  operationID: string,
  fallback = true,
): boolean {
  for (const resource of resources) {
    const operation = resource.operations.find(item => item.id === operationID)
    if (operation) {
      return operation.allowed
    }
  }
  return fallback
}
