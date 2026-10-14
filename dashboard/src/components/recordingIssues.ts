import type { BrowserRecorderAnnotation } from '../hooks/api/useBrowserRecorderApi'

// One kind of console or network issue on one host, as the recording panel
// lists it: "[request] telemetry.example.test · HTTP 403 ×12".
export type RecordingIssueGroup = {
  key: string
  kind: 'console' | 'request'
  host: string
  error: string
  count: number
  // The latest occurrence, in full.
  sample: string
  firstParty: boolean
}

export type RecordingIssueSummary = {
  // First-party groups lead, then third-party; each by count, most first.
  groups: RecordingIssueGroup[]
  // Requests the page cancelled as it moved on (net::ERR_ABORTED), mostly
  // beacons: counted and named by host, not listed one by one.
  cancelled: { count: number; hosts: string[] }
}

const URL_IN_TEXT = /\b(?:https?|wss?):\/\/[^\s"'<>]+/i

function issueHost(annotation: BrowserRecorderAnnotation): string {
  const candidate = annotation.url || annotation.text.match(URL_IN_TEXT)?.[0] || ''
  try {
    return candidate ? new URL(candidate).host.toLowerCase() : ''
  } catch {
    return ''
  }
}

// Second levels that country codes register names under (example.co.uk).
const COUNTRY_SECOND_LEVELS = new Set(['ac', 'co', 'com', 'edu', 'gob', 'go', 'gov', 'mil', 'ne', 'net', 'or', 'org'])

// The registrable part of a host, roughly: its last two labels, or three
// under a country code's registration level (example.co.uk, example.com.au).
export function siteOf(host: string): string {
  const name = host.replace(/:\d+$/, '').replace(/\.$/, '').toLowerCase()
  if (/^[\d.]+$/.test(name) || name.includes(':') || !name.includes('.')) return name
  const labels = name.split('.')
  const keep = labels.length >= 3 && labels[labels.length - 1].length === 2 && COUNTRY_SECOND_LEVELS.has(labels[labels.length - 2]) ? 3 : 2
  return labels.slice(-keep).join('.')
}

function issueError(annotation: BrowserRecorderAnnotation): string {
  if (annotation.kind === 'console_error') return 'console error'
  if (annotation.status) return `HTTP ${annotation.status}`
  const failure = annotation.text.split(' — ').slice(1).join(' — ').trim()
  return failure.match(/net::[A-Z_]+/)?.[0] || failure || 'request failed'
}

// Groups a recording's console and network issues by host and error, puts
// the recorded site's own first, and folds requests cancelled by navigation
// into one count.
export function summarizeRecordingIssues(
  annotations: readonly BrowserRecorderAnnotation[],
  startURL: string,
): RecordingIssueSummary {
  let startSite = ''
  try {
    startSite = siteOf(new URL(startURL).host)
  } catch {
    startSite = ''
  }
  const groups = new Map<string, RecordingIssueGroup>()
  const cancelledHosts = new Map<string, number>()
  let cancelled = 0
  for (const annotation of annotations) {
    // The browser echoes each failed request to the console; the request's
    // own row already lists it.
    if (annotation.kind === 'console_error' && /^Failed to load resource:/i.test(annotation.text.trim())) continue
    const host = issueHost(annotation)
    const error = issueError(annotation)
    if (error === 'net::ERR_ABORTED') {
      cancelled += 1
      cancelledHosts.set(host || 'page', (cancelledHosts.get(host || 'page') ?? 0) + 1)
      continue
    }
    const kind = annotation.kind === 'console_error' ? 'console' : 'request'
    // Console errors on one page differ by message, so each message is its
    // own row; a request's message is its URL, which the host already groups.
    const message = kind === 'console' ? annotation.text.trim().slice(0, 160) : ''
    const key = `${kind}\u0000${host}\u0000${error}\u0000${message}`
    const existing = groups.get(key)
    if (existing) {
      existing.count += 1
      existing.sample = annotation.text
      continue
    }
    groups.set(key, {
      key,
      kind,
      host,
      error,
      count: 1,
      sample: annotation.text,
      firstParty: !host || !startSite || siteOf(host) === startSite,
    })
  }
  const ordered = Array.from(groups.values()).sort((left, right) =>
    Number(right.firstParty) - Number(left.firstParty) || right.count - left.count)
  const hosts = Array.from(cancelledHosts.entries())
    .sort((left, right) => right[1] - left[1])
    .map(([host]) => host)
  return { groups: ordered, cancelled: { count: cancelled, hosts } }
}
