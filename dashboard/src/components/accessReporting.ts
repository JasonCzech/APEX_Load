/** Quote CSV fields and neutralize spreadsheet formulas, including whitespace prefixes. */
export function csvBlob(rows: unknown[][]): Blob {
  return new Blob(
    [
      '\uFEFF' +
        rows
          .map((row) =>
            row
              .map((value) => {
                const text = String(value ?? '')
                // Control characters must also be checked before spreadsheet formula prefixes.
                // eslint-disable-next-line no-control-regex
                const safe = /^[\s\u0000-\u001f]*[=+@-]/.test(text) ? `'${text}` : text
                return `"${safe.replace(/"/g, '""')}"`
              })
              .join(','),
          )
          .join('\r\n') +
        '\r\n',
    ],
    { type: 'text/csv;charset=utf-8' },
  )
}
export function reviewPriority(flags: string[]): 'Action needed' | 'Review recommended' | 'Informational' {
  if (flags.includes('disabled')) return 'Informational'
  if (
    flags.some((flag) =>
      ['expired', 'overdue_rotation', 'expiring_soon', 'broad_scope', 'system_admin'].includes(flag),
    )
  )
    return 'Action needed'
  return 'Review recommended'
}
export function activityDateError(after: string, before: string): string {
  if ((after && !Number.isFinite(Date.parse(after))) || (before && !Number.isFinite(Date.parse(before))))
    return 'Enter valid dates.'
  return after && before && Date.parse(after) > Date.parse(before)
    ? 'The start date must be before the end date.'
    : ''
}

/** A future end date must not let new events shift an in-progress history page. */
export function activitySnapshotBefore(before: string, snapshot: string): string {
  return before && Date.parse(before) < Date.parse(snapshot) ? new Date(before).toISOString() : snapshot
}
