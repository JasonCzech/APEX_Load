const ACTION_LABELS: Record<string, string> = {
  create: 'Created', modify: 'Modified', delete: 'Deleted', move: 'Moved', archive: 'Archived', restore: 'Restored',
  start: 'Started', stop: 'Stopped', abort: 'Aborted', pause: 'Paused', resume: 'Resumed', prune: 'Result artifacts deleted',
}
export const ACTIVITY_ACTIONS = Object.keys(ACTION_LABELS)

/** Operator wording for an activity action; unknown actions keep their recorded name. */
export function activityActionLabel(action: string): string {
  return ACTION_LABELS[action] || action.replace(/_/g, ' ')
}

export type ActivityFilter = { search: string; actor: string; kind: string; action: string; after: string; before: string }
export const emptyActivityFilter: ActivityFilter = { search: '', actor: '', kind: '', action: '', after: '', before: '' }
const URL_KEYS: Record<keyof ActivityFilter, string> = { search: 'q', actor: 'actor', kind: 'type', action: 'action', after: 'after', before: 'before' }

// Filters and page live in the #/activity query (the app view's
// activityQuery) so a reload or shared link returns to the same events.
export function parseActivityQuery(query = ''): { filter: ActivityFilter; page: number } {
  const params = new URLSearchParams(query)
  const filter = { ...emptyActivityFilter }
  for (const field of Object.keys(URL_KEYS) as (keyof ActivityFilter)[]) filter[field] = params.get(URL_KEYS[field]) || ''
  const page = Number.parseInt(params.get('page') || '1', 10)
  return { filter, page: Number.isFinite(page) && page > 1 ? page - 1 : 0 }
}

export function activityQueryString(filter: ActivityFilter, page: number): string {
  const params = new URLSearchParams()
  for (const field of Object.keys(URL_KEYS) as (keyof ActivityFilter)[]) if (filter[field]) params.set(URL_KEYS[field], filter[field])
  if (page > 0) params.set('page', String(page + 1))
  return params.toString()
}
