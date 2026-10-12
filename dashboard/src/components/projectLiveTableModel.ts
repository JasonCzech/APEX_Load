import type { ProjectLiveTableCreate, ProjectLiveTableView } from '../types/projectLiveTables'

export function projectTableCreateErrors(value: ProjectLiveTableCreate): string[] {
  const errors: string[] = []
  if (!value.name.trim()) errors.push('Enter a table name.')
  if (!value.columns.length || value.columns.length > 128 || new Set(value.columns).size !== value.columns.length || value.columns.some(column => !column.trim() || /[\0\r\n]/.test(column) || new TextEncoder().encode(column).length > 128)) errors.push('Use 1–128 distinct column names of at most 128 bytes.')
  if (!Number.isInteger(value.limits.max_rows) || value.limits.max_rows < 1 || value.limits.max_rows > 100000) errors.push('Maximum rows must be between 1 and 100000.')
  if (!Number.isInteger(value.limits.max_bytes) || value.limits.max_bytes < 1024 || value.limits.max_bytes > 8388608) errors.push('Maximum table size must be between 1024 and 8388608 bytes.')
  return errors
}

export function projectTableLifecycleBlock(table: ProjectLiveTableView, action: 'reset' | 'archive'): string | null {
  if (!table.permissions.can_admin) return `${action === 'reset' ? 'Resetting' : 'Archiving'} a table requires project-administrator permission.`
  if (table.state !== 'quiesced') return 'Quiesce this table and wait for all runs to detach first.'
  if (table.attachments?.length || (table.stats?.counts?.leased ?? 0) > 0) return 'Wait for all attachments, retry windows and leases to finish.'
  if (!table.stats?.revision) return 'Refresh the table revision before changing its lifecycle.'
  return null
}

/** Revisions remain strings, including values beyond JavaScript’s safe integer range. */
export function decimalInteger(value: string): boolean { return value.length <= 20 && /^-?(0|[1-9]\d*)$/.test(value) && value !== '-0' && BigInt(value) >= -(2n ** 63n) && BigInt(value) <= 2n ** 63n - 1n }
