/**
 * Column order and clipboard parsing for DataRowGrid, the one row editor
 * shared by data pools, live tables and replay seeds.
 */

/**
 * The preferred order first (keeping only columns some row uses, unless
 * keepUnused), then any other column a row uses, alphabetically. Row data
 * arrives as JSON objects, so the preferred order is the only record of the
 * source's column order.
 */
export function orderedGridColumns(preferred: readonly string[] | undefined, rows: readonly Record<string, string>[], keepUnused = false): string[] {
  const present = new Set<string>()
  for (const row of rows) for (const key of Object.keys(row)) present.add(key)
  const ordered: string[] = []
  const listed = new Set<string>()
  for (const column of preferred || []) {
    if (listed.has(column) || (!keepUnused && !present.has(column))) continue
    listed.add(column)
    ordered.push(column)
  }
  const rest = [...present].filter(column => !listed.has(column)).sort((left, right) => left < right ? -1 : left > right ? 1 : 0)
  return [...ordered, ...rest]
}

/** True when scripts can reference the column as {{data.alias.column}}. */
export function isReferenceableColumnName(name: string): boolean {
  return /^[\w-]+$/.test(name)
}

export const COLUMN_NAME_RULE = 'Use only letters, numbers, underscores and hyphens, so scripts can reference the column as {{data.alias.column}}.'

/** Why a new column name can't be added, or '' when it can. */
export function newColumnError(name: string, columns: readonly string[], maxColumns?: number): string {
  const trimmed = name.trim()
  if (!trimmed) return 'Enter a column name.'
  if (!isReferenceableColumnName(trimmed)) return COLUMN_NAME_RULE
  if (columns.includes(trimmed)) return `There is already a column named "${trimmed}".`
  if (maxColumns !== undefined && columns.length >= maxColumns) return `This table can have at most ${maxColumns} columns.`
  return ''
}

/** True when pasted text spans more than one cell (a tab or a line break). */
export function isTabularText(text: string): boolean {
  return /[\t\r\n]/.test(text.replace(/(\r?\n)+$/, ''))
}

function splitTabularLines(text: string): string[][] {
  const lines = text.replace(/\r\n?/g, '\n').replace(/\n+$/, '').split('\n')
  return lines.map(line => line.split('\t'))
}

export interface ParsedPaste {
  rows: Record<string, string>[]
  /** The first line named existing columns and was used as the header. */
  usedHeader: boolean
  /** Values that fell past the last column and were dropped. */
  droppedValues: number
  /** Names in a pasted header that the table lacks; nothing is parsed then. */
  unknownColumns: string[]
}

/**
 * Turns spreadsheet text (tab-separated lines) into rows. A first line whose
 * cells are filled and distinct, at least half of them naming columns, is a
 * header: values go to the named columns, and a header naming columns the table lacks
 * returns them in unknownColumns instead of rows. Otherwise values fill the
 * columns from startColumn rightward. Blank lines are skipped. Every pasted
 * cell is kept as given, so an empty pasted cell becomes an empty value rather
 * than an absent one.
 */
export function parseTabularPaste(text: string, columns: readonly string[], startColumn = 0): ParsedPaste {
  const lines = splitTabularLines(text).filter(cells => cells.some(cell => cell.trim() !== ''))
  if (lines.length === 0) return { rows: [], usedHeader: false, droppedValues: 0, unknownColumns: [] }
  const header = lines[0].map(cell => cell.trim())
  // At least half the cells must name columns: one data value that happens to
  // equal a column name doesn't make a header.
  const named = header.filter(cell => columns.includes(cell)).length
  const looksLikeHeader = lines.length > 1 && header.every(cell => cell !== '') && new Set(header).size === header.length && named > 0 && named * 2 >= header.length
  if (looksLikeHeader) {
    const unknownColumns = header.filter(cell => !columns.includes(cell))
    if (unknownColumns.length > 0) return { rows: [], usedHeader: true, droppedValues: 0, unknownColumns }
  }
  const targets = looksLikeHeader ? header : columns.slice(Math.max(0, startColumn))
  let droppedValues = 0
  const rows = (looksLikeHeader ? lines.slice(1) : lines).map(cells => {
    const row: Record<string, string> = {}
    cells.forEach((cell, index) => {
      if (index < targets.length) row[targets[index]] = cell
      else if (cell !== '') droppedValues += 1
    })
    return row
  })
  return { rows, usedHeader: looksLikeHeader, droppedValues, unknownColumns: [] }
}

function csvCell(value: string): string {
  return /[",\r\n]/.test(value) || value !== value.trim() ? `"${value.replace(/"/g, '""')}"` : value
}

/**
 * A CSV file of rows under the given columns, for appending pasted rows in one
 * import. Cells a row doesn't set are written empty.
 */
export function rowsToCSV(columns: readonly string[], rows: readonly Record<string, string>[]): string {
  const lines = [columns.map(csvCell).join(',')]
  for (const row of rows) lines.push(columns.map(column => csvCell(row[column] ?? '')).join(','))
  return `${lines.join('\r\n')}\r\n`
}
