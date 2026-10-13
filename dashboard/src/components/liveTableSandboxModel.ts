import type { LiveTableSandboxSeed } from '../types/replay'
import { usesTableSteps, type DataAliasUsage } from './scriptDataUsageModel'

// These limits match the isolated replay contract, not managed run capacity.
export function parseLiveTableSandboxSeeds(text: string | undefined): LiveTableSandboxSeed[] {
  if (text && new TextEncoder().encode(text).length > 65536) throw new Error('Sandbox seeds must fit within 64 KiB.')
  if (!text?.trim()) return []
  let parsed: unknown
  try { parsed = JSON.parse(text) } catch { throw new Error('Enter sandbox seeds as a JSON array.') }
  if (!Array.isArray(parsed) || parsed.length > 4) throw new Error('Use an array of at most four sandbox tables.')
  const aliases = new Set<string>()
  for (const seed of parsed) {
    if (!seed || typeof seed !== 'object' || Array.isArray(seed) || Object.keys(seed).some(key => !['alias', 'profile', 'columns', 'rows'].includes(key))) throw new Error('Each sandbox table accepts alias, profile, columns and rows only.')
    if (typeof seed.alias !== 'string' || !/^[A-Za-z_][\w-]{0,63}$/.test(seed.alias) || aliases.has(seed.alias.toLowerCase())) throw new Error('Use distinct sandbox aliases of at most 64 letters, numbers, underscores or hyphens, starting with a letter or underscore.')
    aliases.add(seed.alias.toLowerCase())
    if (seed.profile !== undefined && !['native_queue', 'vts_columns'].includes(seed.profile)) throw new Error('Use the native_queue or vts_columns profile for sandbox tables.')
    if (!Array.isArray(seed.columns) || !seed.columns.length || seed.columns.length > 32 || new Set(seed.columns).size !== seed.columns.length || seed.columns.some((column: unknown) => typeof column !== 'string' || !column.trim() || /[\0\r\n]/.test(column) || new TextEncoder().encode(column).length > 128)) throw new Error('Declare 1–32 distinct, nonempty sandbox columns, each at most 128 bytes.')
    if (!Array.isArray(seed.rows) || seed.rows.length > 256) throw new Error('Each sandbox needs a rows array with at most 256 rows; use [] for producers.')
    let fields = 0
    for (const row of seed.rows) {
      if (!row || typeof row !== 'object' || Array.isArray(row) || Object.keys(row).some(key => key !== 'values') || !row.values || typeof row.values !== 'object' || Array.isArray(row.values)) throw new Error('Each sandbox row accepts a values object only; record identities are created per replay.')
      for (const [column, value] of Object.entries(row.values)) {
        if (!seed.columns.includes(column) || typeof value !== 'string' || new TextEncoder().encode(value).length > 8192) throw new Error('Sandbox row values must be strings in declared columns, each at most 8 KiB.')
        if (/\{\{\s*secret\./.test(value)) throw new Error('Managed secrets cannot seed isolated live tables.')
        fields++
      }
    }
    if (fields > 4096) throw new Error('Each sandbox table allows at most 4,096 cells.')
  }
  return parsed as LiveTableSandboxSeed[]
}

export function liveTableSandboxError(text: string | undefined): string | null {
  try { parseLiveTableSandboxSeeds(text); return null } catch (error) { return (error as Error).message }
}

const SANDBOX_MAX_TABLES = 4

/**
 * Seeds with a table added for each alias the script's table steps use and
 * the seeds lack: its columns from the script (up to 32), shared columns only
 * for column steps, and no rows. Existing tables stay as they are.
 */
export function sandboxSeedsForScript(seeds: LiveTableSandboxSeed[], usage: DataAliasUsage[]): { seeds: LiveTableSandboxSeed[]; added: string[]; left: string[] } {
  const next = [...seeds]
  const have = new Set(seeds.map(seed => seed.alias.toLowerCase()))
  const added: string[] = []
  const left: string[] = []
  for (const item of usage.filter(usesTableSteps)) {
    if (have.has(item.alias.toLowerCase())) continue
    if (next.length >= SANDBOX_MAX_TABLES) { left.push(item.alias); continue }
    next.push({ alias: item.alias, profile: item.kinds.includes('vts_columns') && !item.kinds.includes('native_queue') ? 'vts_columns' : 'native_queue', columns: item.columns.length ? item.columns.slice(0, 32) : ['id'], rows: [] })
    have.add(item.alias.toLowerCase())
    added.push(item.alias)
  }
  return { seeds: next, added, left }
}

/** What a live-table step left under its result prefix in a replay. */
export interface LiveTableStepResult {
  prefix: string
  status?: string
  recordId?: string
  version?: string
  revision?: string
  count?: string
  value?: string
  /** The row's cells: a missing key is an absent cell, '' an empty one. */
  row?: Record<string, string>
  rows?: Array<{ id?: string; version?: number; values?: Record<string, string> }>
  cells?: Array<{ present: boolean; value?: string }>
  columns?: string[]
}

function parsed<T>(text: string | undefined): T | undefined {
  if (text === undefined) return undefined
  try { return JSON.parse(text) as T } catch { return undefined }
}

/**
 * A table step's result from its replay event's variables. Without the
 * step's prefix (the script changed since), the one the variables show is used.
 */
export function liveTableStepResult(extractions: Record<string, string> | undefined, prefix?: string): LiveTableStepResult | null {
  if (!extractions) return null
  const name = prefix || Object.keys(extractions).find(key => key.endsWith('.status') && `${key.slice(0, -'.status'.length)}.revision` in extractions)?.slice(0, -'.status'.length)
  if (!name || !(`${name}.status` in extractions)) return null
  const field = (key: string) => extractions[`${name}.${key}`]
  const lead = `${name}.values.`
  const row = Object.keys(extractions).filter(key => key.startsWith(lead)).reduce<Record<string, string> | undefined>((cells, key) => ({ ...cells, [key.slice(lead.length)]: extractions[key] }), undefined)
  return {
    prefix: name, status: field('status'), recordId: field('record_id'), version: field('record_version'), revision: field('revision'), count: field('count'), value: field('value'),
    row: row || (field('record_id') !== undefined ? {} : undefined),
    rows: parsed(field('rows')), cells: parsed(field('cells')), columns: parsed(field('columns')),
  }
}

/**
 * Why a table step failed, in the words of what to do next. An unknown
 * outcome is never something to simply run again: the change may have been made.
 */
export function liveTableFailureNote(message: string | undefined): string | null {
  if (!message) return null
  const notes: Array<[RegExp, string]> = [
    [/outcome is unknown|did not establish the original outcome/, 'The table may or may not have made this change. Check the table before running the step again: running it again could make the change twice.'],
    [/continuity was lost/, 'A lease expired or the table was lost, so later table steps and acknowledgments in this iteration are blocked. A leased row is delivered again once its lease expires.'],
    [/lease reference is missing or belongs to another table/, 'No lease is held under this name in this iteration. An earlier step must lease a row from the same table and save its results under this name.'],
    [/already settled/, 'This lease was already acknowledged or released.'],
    [/lease reference is already held/, 'A lease is already held under this name. Acknowledge or release it before leasing again.'],
    [/returned no available result/, 'The table had no row for this step. Add starting rows under Debug options › Isolated live tables, or let the step skip when no row is found.'],
    [/condition was not met/, 'The row no longer had the version or values this step expected: another VUser changed it first, or the expected version is out of date. Read the row again before changing it.'],
    [/alias is not bound|requires an admitted live binding/, 'This alias has no isolated table. Add one under Debug options › Isolated live tables.'],
    [/operation failed: expired/, 'The lease expired before this step, so the row will be delivered again. Lease for longer, or renew the lease before it runs out.'],
    [/operation failed: conflict/, 'Another change to the row came first. Read the row again before changing it.'],
    [/operation failed: capacity/, 'The table is full: it reached its row or size limit.'],
    [/managed secret/, 'Managed secret values cannot be stored in a table or compared with one.'],
  ]
  return notes.find(([pattern]) => pattern.test(message))?.[1] || null
}
