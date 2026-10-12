import type { LiveTableSandboxSeed } from '../types/replay'

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
