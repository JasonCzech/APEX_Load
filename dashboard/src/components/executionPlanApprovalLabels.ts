import type { ExecutionPlanApproval } from '../types'

// Plain words for an execution-plan approval's stored codes.

export function approvalPolicyLabel(policy: string): string {
  if (policy === 'quota_burst') return 'Over the project VUser limit'
  const words = policy.replace(/[_-]+/g, ' ').trim()
  return words ? words.charAt(0).toUpperCase() + words.slice(1) : 'Approval'
}

export function approvalStatusLabel(approval: Pick<ExecutionPlanApproval, 'status' | 'run_id'>): string {
  switch (approval.status) {
    case 'pending':
      return 'Waiting for a decision'
    case 'approved':
      return approval.run_id ? 'Approved and used' : 'Approved'
    case 'rejected':
      return 'Rejected'
    default:
      return approval.status
  }
}

export function approvalRoleLabel(role: string): string {
  const normalized = role.replace(/[\s_-]+/g, '').toLowerCase()
  if (normalized === 'projectadmin') return 'Needs a project admin'
  if (normalized === 'systemadmin') return 'Needs a system admin'
  return `Needs ${role.replace(/[_-]+/g, ' ').toLowerCase()}`
}

// Approvals record who acted by subject; the development-mode and service
// account subjects read as what they are.
export function approvalActorLabel(subject: string): string {
  if (subject === 'dev-mode') return 'development mode'
  if (subject.startsWith('service-account:')) return 'a service account'
  return subject
}
