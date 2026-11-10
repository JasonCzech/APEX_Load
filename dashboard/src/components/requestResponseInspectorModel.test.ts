import { describe, expect, it } from 'vitest'
import type { BrowserReplayEvent } from '../types/replay'
import {
  adjacentFailureIndexes,
  availableInspectorTabs,
  buildCurlCommand,
  capturedResponseBody,
  countLiteralMatches,
  curlCommandGaps,
  defaultInspectorTabForEvent,
  eventIndexById,
  extractorTesterUnappliedSettings,
  extractorTesterUnsupportedSurface,
  extractorUpdateBlocker,
  failingExtractorName,
  findExtractorDefinition,
  followerVerdictText,
  formatFindResult,
  formatTimingMs,
  htmlPageInsight,
  inspectorTabBadge,
  inspectorTimingBar,
  inspectorTimingBarLabel,
  isFollowerFailure,
  isNavigableFailure,
  replayNetworkEntries,
  unresolvedVariables,
} from './requestResponseInspectorModel'

function event(overrides: Partial<BrowserReplayEvent> = {}): BrowserReplayEvent {
  return {
    phase: 'action', action_index: 0, action_name: 'Checkout', action_type: 'request', method: 'POST', target: '/checkout',
    success: true, status_code: 200, started_at: '2026-10-25T12:00:00Z', completed_at: '2026-10-25T12:00:01Z', duration_ms: 1000,
    ...overrides,
  } as BrowserReplayEvent
}

const follower = (overrides: Partial<NonNullable<BrowserReplayEvent['diagnosis']>> = {}) => event({
  event_id: 'aura-1', success: false, status_code: 401, error_message: 'expected status 200, got 401',
  diagnosis: {
    category: 'assertion', severity: 'error', cause: 'expected status 200, got 401',
    follows_failure_event_id: 'login', follows_failure_action: 'T02_LoginSFApp', follows_reason: 'aura_invalid_session',
    ...overrides,
  },
})

describe('follow-on failures', () => {
  it('knows a follower and its unset values', () => {
    expect(isFollowerFailure(follower())).toBe(true)
    // Only a failure follows; a linked id on a passed event is ignored.
    expect(isFollowerFailure({ ...follower(), success: true })).toBe(false)
    expect(isFollowerFailure(event({ success: false }))).toBe(false)
    expect(unresolvedVariables(follower({ unresolved_variables: ['c_interactionid', '', 'c_other'] }))).toEqual(['c_interactionid', 'c_other'])
    expect(unresolvedVariables(event())).toEqual([])
  })

  it('says what happened first and why this request failed, in plain words', () => {
    expect(followerVerdictText(follower())).toEqual({
      lead: "This request failed after the run's first failure, T02_LoginSFApp.",
      reason: 'Salesforce rejected the session (aura:invalidSession).',
    })
    expect(followerVerdictText(follower({ follows_reason: 'http_401' })).reason).toBe('The server answered 401.')
    expect(followerVerdictText(follower({ follows_reason: 'http_403' })).reason).toBe('The server answered 403.')
    expect(followerVerdictText(follower({ follows_reason: 'unresolved_variable', unresolved_variables: ['c_interactionid'] })).reason)
      .toBe('The request still contains {{var.c_interactionid}} because that value was never set.')
    expect(followerVerdictText(follower({ follows_reason: 'unresolved_variable', unresolved_variables: ['a', 'b'] })).reason)
      .toBe('The request still contains {{var.a}}, {{var.b}} because those values were never set.')
    // Unset values are named after the leading reason too.
    expect(followerVerdictText(follower({ unresolved_variables: ['c_interactionid'] })).reason)
      .toBe('Salesforce rejected the session (aura:invalidSession). The request still contains {{var.c_interactionid}} because that value was never set.')
    expect(followerVerdictText(follower({ follows_failure_action: '' })).lead).toBe("This request failed after the run's first failure.")
    expect(followerVerdictText(follower({ follows_reason: 'something_new' })).reason).toBe('')
  })

  it("offers Raw for a follower's rule-based diagnosis", () => {
    expect(availableInspectorTabs(follower()).has('raw')).toBe(true)
    expect(availableInspectorTabs(event({ success: false })).has('raw')).toBe(false)
  })

  it('finds an event by id, or says it is not retained', () => {
    const events = [event({ event_id: 'login' }), event({ event_id: 'aura-1' }), event()]
    expect(eventIndexById(events, 'aura-1')).toBe(1)
    expect(eventIndexById(events, 'evicted')).toBeNull()
    expect(eventIndexById(events, undefined)).toBeNull()
    expect(eventIndexById(events, '')).toBeNull()
  })

  it('steps to the previous and next failure, leaving out async callbacks', () => {
    const failed = (phase = 'action') => event({ success: false, phase })
    const events = [failed('init'), event(), failed('async'), failed(), event(), failed('teardown')]
    expect(isNavigableFailure(events[2])).toBe(false)
    expect(isNavigableFailure(events[3])).toBe(true)
    expect(isNavigableFailure(null)).toBe(false)
    expect(adjacentFailureIndexes(events, 0)).toEqual({ previous: null, next: 3 })
    expect(adjacentFailureIndexes(events, 1)).toEqual({ previous: 0, next: 3 })
    expect(adjacentFailureIndexes(events, 3)).toEqual({ previous: 0, next: 5 })
    expect(adjacentFailureIndexes(events, 5)).toEqual({ previous: 3, next: null })
    expect(adjacentFailureIndexes([event(), event()], 0)).toEqual({ previous: null, next: null })
  })
})

describe('the failing extractor', () => {
  it('reads the name from the engine error, unquoting it', () => {
    const failed = (error_message: string) => event({ success: false, error_message })
    expect(failingExtractorName(failed('extractor "c_cshc" found no match: extraction found no match: no match for regex "cshc=(.+?)"'))).toBe('c_cshc')
    expect(failingExtractorName(failed('Action failed: extractor "token" failed: bad JSON'))).toBe('token')
    expect(failingExtractorName(failed('extractor "out" output rejected: too long'))).toBe('out')
    expect(failingExtractorName(failed('extractor "say \\"hi\\"" found no match: none'))).toBe('say "hi"')
    expect(failingExtractorName(failed('expected status 200, got 500'))).toBeNull()
    // A passed event has no failing extractor; the diagnosis cause is read too.
    expect(failingExtractorName(event({ error_message: 'extractor "x" found no match' }))).toBeNull()
    expect(failingExtractorName(event({ success: false, diagnosis: { category: 'correlation', severity: 'error', cause: 'extractor "fromCause" found no match: x' } }))).toBe('fromCause')
  })

  it("finds the step's definition by name", () => {
    const extractors = [{ name: 'a', source: 'body', type: 'regex', expression: 'a=(\\d+)' }, { name: 'c_cshc', source: '', type: 'boundary', expression: 'cshc=|||&' }]
    expect(findExtractorDefinition(extractors, 'c_cshc')).toBe(extractors[1])
    expect(findExtractorDefinition(extractors, 'missing')).toBeNull()
    expect(findExtractorDefinition(undefined, 'a')).toBeNull()
    expect(findExtractorDefinition(extractors, null)).toBeNull()
  })
})

describe('default sub-tab for a selected event', () => {
  it('opens a failure on the view that explains it, and leaves a pass to the last pick', () => {
    expect(defaultInspectorTabForEvent(event())).toBeNull()
    expect(defaultInspectorTabForEvent(event({ success: false, error_message: 'extractor "c_cshc" found no match: x' }))).toBe('correlation')
    expect(defaultInspectorTabForEvent(event({ success: false, error_message: 'boom', diagnosis: { category: 'x', severity: 'error', cause: 'boom', unresolved_variables: ['v'] } }))).toBe('correlation')
    expect(defaultInspectorTabForEvent(event({ success: false, status_code: undefined, error_message: 'connection refused' }))).toBe('timing')
    expect(defaultInspectorTabForEvent(event({ success: false, status_code: undefined, snapshot: { response_status: 503 } }))).toBe('response')
    expect(defaultInspectorTabForEvent(event({ success: false, status_code: 500, error_message: 'expected 200' }))).toBe('response')
    // A follower opens on its rejected response even with unset values.
    expect(defaultInspectorTabForEvent(follower({ unresolved_variables: ['c_interactionid'] }))).toBe('response')
  })
})

describe('Copy as cURL', () => {
  it('builds the wire request with shell quoting', () => {
    const curl = buildCurlCommand({
      request_method: 'post', request_url: "https://org.example.test/aura?r=1&q=it's",
      request_headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'Content-Length': '42', ':authority': 'org.example.test', 'Accept-Encoding': 'gzip, br', Cookie: '[REDACTED]' },
      request_body: "message={\"a\":\"it's\"}",
    })
    expect(curl).toBe([
      'curl',
      '--globoff',
      '--request POST',
      `--url 'https://org.example.test/aura?r=1&q=it'"'"'s'`,
      `--header 'Content-Type: application/x-www-form-urlencoded'`,
      `--header 'Accept-Encoding: gzip, br'`,
      `--header 'Cookie: [REDACTED]'`,
      '--compressed',
      `--data-raw 'message={"a":"it'"'"'s"}'`,
    ].join(' \\\n  '))
  })

  it('defaults the method, adds no body or --compressed it does not need, and needs a URL', () => {
    expect(buildCurlCommand({ request_url: 'https://example.test/', request_headers: { 'accept-encoding': 'identity' } }))
      .toBe("curl \\\n  --globoff \\\n  --request GET \\\n  --url 'https://example.test/' \\\n  --header 'accept-encoding: identity'")
    expect(buildCurlCommand({ request_method: 'GET' })).toBeNull()
    expect(buildCurlCommand(undefined)).toBeNull()
    // A method that is not a plain token is quoted like every other value.
    expect(buildCurlCommand({ request_method: 'get; rm -rf', request_url: 'https://example.test/' }))
      .toBe("curl \\\n  --globoff \\\n  --request 'GET; RM -RF' \\\n  --url 'https://example.test/'")
  })

  it('sends an empty header value with curl\'s "Name;" form ("Name:" would remove the header)', () => {
    const curl = buildCurlCommand({ request_method: 'GET', request_url: 'https://example.test/', request_headers: { 'X-Requested-With': '', Accept: '  ', 'X-Ok': 'v' } })!
    expect(curl.split(' \\\n  ')).toEqual([
      'curl', '--globoff', '--request GET', "--url 'https://example.test/'",
      "--header 'X-Requested-With;'", "--header 'Accept;'", "--header 'X-Ok: v'",
    ])
  })

  it('turns URL globbing off, so {{var.x}} and [0] in a URL reach curl as typed', () => {
    // Without --globoff curl reads {} and [] as globs and fails before sending
    // ("nested brace", "bad range"): an unresolved variable, an indexed
    // parameter and the retention marker all hit it.
    const curl = buildCurlCommand({ request_method: 'GET', request_url: 'https://org.example.test/aura?a={{var.x}}&filter[0]=x' })!
    expect(curl.split(' \\\n  ')).toEqual(['curl', '--globoff', '--request GET', "--url 'https://org.example.test/aura?a={{var.x}}&filter[0]=x'"])
    expect(buildCurlCommand({ request_url: 'https://org.example.test/aura?q=...[truncated]' })?.split(' \\\n  ').slice(0, 2)).toEqual(['curl', '--globoff'])
  })

  it('copies a HEAD request with --head, which does not wait for a body', () => {
    expect(buildCurlCommand({ request_method: 'head', request_url: 'https://example.test/health', request_headers: { Accept: '*/*' } }))
      .toBe("curl \\\n  --globoff \\\n  --head \\\n  --url 'https://example.test/health' \\\n  --header 'Accept: */*'")
    // curl refuses --head with --data-raw (two methods): a HEAD body is left out, and listed as a gap.
    const withBody = { request_method: 'HEAD', request_url: 'https://example.test/health', request_body: 'x=1' }
    expect(buildCurlCommand(withBody)).toBe("curl \\\n  --globoff \\\n  --head \\\n  --url 'https://example.test/health'")
    expect(curlCommandGaps(withBody)).toEqual(['HEAD request body left out (curl --head sends none)'])
    expect(curlCommandGaps({ ...withBody, request_method: 'POST' })).toEqual([])
  })

  it('lists what retention cut from the request, and keeps the copy a single curl command', () => {
    const complete = { request_method: 'POST', request_url: 'https://example.test/aura', request_body: 'message=1' }
    expect(curlCommandGaps(complete)).toEqual([])
    expect(buildCurlCommand(complete)?.startsWith('curl ')).toBe(true)

    const cut = { ...complete, request_body: 'message={"actions":[...[truncated]', request_headers_omitted: true, request_wire_headers_unavailable: true }
    expect(curlCommandGaps(cut)).toEqual([
      'body truncated by retention limits',
      'some headers omitted or shortened by retention limits',
      'wire headers could not be verified, so headers may be incomplete',
    ])
    // No comment lines (an interactive zsh runs "# …" as a command, and the
    // text after a ";" as another): the copy is the same command as a complete
    // snapshot's, with every line but the last continued.
    const curl = buildCurlCommand(cut)!
    const lines = curl.split('\n')
    expect(lines[0]).toBe('curl \\')
    expect(lines.some(line => line.trimStart().startsWith('#'))).toBe(false)
    expect(lines.slice(0, -1).every(line => line.endsWith(' \\'))).toBe(true)
    expect(curl).toBe(buildCurlCommand({ ...complete, request_body: cut.request_body }))
    expect(curl.endsWith(`--data-raw 'message={"actions":[...[truncated]'`)).toBe(true)
    expect(curlCommandGaps({ ...complete, request_url: 'https://example.test/aura?q=...[truncated]' })).toEqual(['URL truncated by retention limits'])
    expect(curlCommandGaps(undefined)).toEqual([])
  })
})

describe('what the extractor tester cannot apply', () => {
  it('names DFEs and a response scope other than the final response', () => {
    expect(extractorTesterUnappliedSettings(null)).toEqual([])
    expect(extractorTesterUnappliedSettings({})).toEqual([])
    expect(extractorTesterUnappliedSettings({ dfes: [' '], scope: { redirects: 'exclude', resources: '', header_names: [''] } })).toEqual([])
    expect(extractorTesterUnappliedSettings({
      dfes: ['JsonXml', 'UrlEncoding'],
      scope: { redirects: 'Include', resources: 'include', request_url: '*/aura*', content_type: 'text/*', header_names: ['Set-Cookie'] },
    })).toEqual([
      'its data-format transforms (JsonXml, UrlEncoding)', 'redirect hops', 'page resources',
      'its request URL filter', 'its Content-Type filter', 'its header-name filter',
    ])
  })
})

describe('updating the failing extractor', () => {
  const state = { onStep: true, jsonUnapplied: false, stepsInPlace: true, hasExtractor: true }

  it('says why Update cannot change the step, unapplied JSON edits before a changed step', () => {
    expect(extractorUpdateBlocker(state)).toBe('')
    expect(extractorUpdateBlocker({ ...state, jsonUnapplied: true })).toBe('apply or discard the JSON edits first.')
    // Dirty JSON is named even when the structured steps no longer match either.
    expect(extractorUpdateBlocker({ ...state, jsonUnapplied: true, stepsInPlace: false })).toBe('apply or discard the JSON edits first.')
    expect(extractorUpdateBlocker({ ...state, stepsInPlace: false })).toBe('the step changed since this replay.')
    expect(extractorUpdateBlocker({ ...state, hasExtractor: false })).toBe('the step no longer has it.')
    expect(extractorUpdateBlocker({ ...state, onStep: false, jsonUnapplied: true })).toBe('this response is not from a step of the script.')
  })
})

describe('extractor tester surfaces', () => {
  it('names a surface the tester cannot show, and nothing for the ones it can', () => {
    for (const source of ['', 'body', 'header', 'all', 'cookies']) expect(extractorTesterUnsupportedSurface({ source })).toBeNull()
    expect(extractorTesterUnsupportedSurface({ source: 'url' })).toBe('the URL')
    expect(extractorTesterUnsupportedSurface({ source: 'status' })).toBe('the status code')
    expect(extractorTesterUnsupportedSurface({ source: 'trailer' })).toBe('the “trailer” source')
    expect(extractorTesterUnsupportedSurface(null)).toBeNull()
  })
})

describe('timing bar', () => {
  const har = (phases: Array<[string, number, number]>) => ({ phases: phases.map(([name, start_ms, end_ms]) => ({ name, start_ms, end_ms })) })

  it("stacks the main request's phases and the rest of the action", () => {
    const bar = inspectorTimingBar(event({
      method: 'POST', target: '/login', duration_ms: 693,
      snapshot: { request_method: 'POST', request_url: 'https://org.example.test/login' },
      network_entries: [
        { method: 'GET', url: 'https://org.example.test/app.js', resource_type: 'script', har: har([['send', 0, 1], ['request_wait', 1, 50], ['response', 50, 60]]) },
        { method: 'POST', url: 'https://org.example.test/login', har: har([['dns', 0, 2], ['connect', 2, 5], ['send', 5, 5.1], ['request_wait', 5.1, 161.7], ['response', 161.7, 161.9]]) },
      ],
    }))
    expect(bar.phased).toBe(true)
    expect(bar.totalMs).toBe(693)
    expect(bar.segments.map(segment => segment.key)).toEqual(['connect', 'send', 'wait', 'receive', 'rest'])
    expect(bar.segments[0].durationMs).toBeCloseTo(5)
    expect(bar.segments[2].durationMs).toBeCloseTo(156.6)
    expect(bar.segments[4].durationMs).toBeCloseTo(693 - 161.9)
    expect(bar.segments.reduce((sum, segment) => sum + segment.widthPct, 0)).toBeCloseTo(100)
    expect(inspectorTimingBarLabel(bar)).toBe('Total 693 ms: connection setup 5 ms, send 0.1 ms, waiting 157 ms, receive 0.2 ms, sub-resources and processing 531 ms')
  })

  it("never takes another resource's phases for the action's, drops a sub-millisecond rest, and falls back to the total", () => {
    // A selector action (a click) has no request of its own: a stylesheet is not its main request.
    const click = inspectorTimingBar(event({ action_type: 'browser_click', method: undefined, target: '#submit', duration_ms: 54.4,
      network_entries: [{ url: 'https://shop.example.test/app.css', method: 'GET', resource_type: 'stylesheet', har: har([['send', 0, 1], ['request_wait', 1, 40]]) }] }))
    expect(click.phased).toBe(false)
    expect(click.segments.map(segment => segment.key)).toEqual(['total'])
    expect(click.totalMs).toBe(54.4)
    // Nor is a request to another URL.
    expect(inspectorTimingBar(event({ duration_ms: 54.4, network_entries: [{ url: '/other', method: 'POST', har: har([['request_wait', 0, 53.6]]) }] })).phased).toBe(false)

    // A browser navigation's main request is its document; it reports no send, so there is no Send segment.
    const phased = inspectorTimingBar(event({ action_type: 'browser_navigate', method: undefined, target: 'https://shop.example.test/cart', duration_ms: 54.4,
      network_entries: [
        { url: 'https://shop.example.test/app.js', method: 'GET', resource_type: 'script', browser_evidence: { phases: [{ name: 'request_wait', start_ms: 0, end_ms: 30 }] } as never },
        { url: 'https://shop.example.test/cart', method: 'GET', resource_type: 'document', browser_evidence: { phases: [{ name: 'request_wait', start_ms: 0, end_ms: 53.6 }, { name: 'response', start_ms: 53.6, end_ms: 53.8 }] } as never },
      ] }))
    expect(phased.segments.map(segment => segment.key)).toEqual(['wait', 'receive'])
    expect(phased.segments[0].durationMs).toBeCloseTo(53.6)
    expect(phased.totalMs).toBe(54.4)
    expect(inspectorTimingBarLabel(phased)).not.toContain('send')

    const total = inspectorTimingBar(event({ duration_ms: 1250 }))
    expect(total).toEqual({ totalMs: 1250, phased: false, segments: [{ key: 'total', label: 'Total', durationMs: 1250, widthPct: 100 }] })
    expect(inspectorTimingBarLabel(total)).toBe('Total 1.25 s')
    // Phases longer than the recorded duration widen the bar instead of overflowing it.
    const longer = inspectorTimingBar(event({ duration_ms: 10, network_entries: [{ method: 'POST', url: '/checkout', har: har([['request_wait', 0, 20]]) }] }))
    expect(longer.totalMs).toBe(20)
    expect(longer.segments.find(segment => segment.key === 'rest')).toBeUndefined()
  })

  it("counts a browser's TLS once: its connect already spans the handshake", () => {
    // Resource Timing: connect = connectStart → connectEnd, tls = secureConnectionStart → connectEnd.
    const bar = inspectorTimingBar(event({ action_type: 'browser_navigate', method: undefined, target: 'https://shop.example.test/', duration_ms: 200,
      network_entries: [{ url: 'https://shop.example.test/', method: 'GET', resource_type: 'document', browser_evidence: { phases: [
        { name: 'dns', start_ms: 0, end_ms: 10 }, { name: 'connect', start_ms: 10, end_ms: 110 }, { name: 'tls', start_ms: 50, end_ms: 110 },
        { name: 'request_wait', start_ms: 110, end_ms: 170 }, { name: 'response', start_ms: 170, end_ms: 180 },
      ] } as never }] }))
    expect(bar.segments.map(segment => segment.key)).toEqual(['connect', 'wait', 'receive', 'rest'])
    // dns 10 + connect 100 (TLS inside it), not 170.
    expect(bar.segments[0].durationMs).toBeCloseTo(110)
    expect(bar.segments[3].durationMs).toBeCloseTo(20)
    expect(bar.totalMs).toBe(200)
    expect(bar.segments.reduce((sum, segment) => sum + segment.widthPct, 0)).toBeCloseTo(100)

    // HTTP's connect and tls follow each other: both count.
    const http = inspectorTimingBar(event({ duration_ms: 100, network_entries: [{ method: 'POST', url: '/checkout', har: har([['connect', 0, 20], ['tls', 20, 50], ['send', 50, 51], ['request_wait', 51, 90], ['response', 90, 95]]) }] }))
    expect(http.segments[0]).toMatchObject({ key: 'connect', durationMs: 50 })
  })

  it('formats durations', () => {
    expect(formatTimingMs(0.12)).toBe('0.1 ms')
    expect(formatTimingMs(156.6)).toBe('157 ms')
    expect(formatTimingMs(2345)).toBe('2.35 s')
    expect(formatTimingMs(Number.NaN)).toBe('0 ms')
  })
})

describe('HTML page insight', () => {
  const html = (body: string, overrides: Partial<BrowserReplayEvent> = {}, headers: Record<string, string> = { 'Content-Type': 'text/html; charset=UTF-8' }) => event({
    snapshot: { request_method: 'POST', request_url: 'https://org.example.test/', response_status: 200, response_headers: headers, response_body: body, response_body_state: 'captured' },
    ...overrides,
  })

  it('reads the title and flags an unexpected login page', () => {
    expect(htmlPageInsight(html('<html><head><title> Login | Salesforce </title></head></html>'))).toEqual({ title: 'Login | Salesforce', looksLikeLogin: true, partial: false })
    expect(htmlPageInsight(html('<title>Fish &amp; Chips &#8211; Menu&nbsp;&#x21;</title>'))).toEqual({ title: 'Fish & Chips – Menu !', looksLikeLogin: false, partial: false })
    expect(htmlPageInsight(html('<form><INPUT name="pw" TYPE=password></form>'))).toEqual({ title: '', looksLikeLogin: true, partial: false })
    expect(htmlPageInsight(html("<input type='password'><title>Sign in</title>"))?.looksLikeLogin).toBe(true)
    expect(htmlPageInsight(html('<title>Home</title>'))?.looksLikeLogin).toBe(false)
    // "Login" inside a word is not a login title.
    expect(htmlPageInsight(html('<title>Blogin news</title>'))?.looksLikeLogin).toBe(false)
    expect(htmlPageInsight(html(`<title>${'x'.repeat(200)}</title>`))?.title).toHaveLength(120)
  })

  it('expects a login page from a GET to a login URL', () => {
    const page = (method: string, url: string) => {
      const value = html('<title>Login | Salesforce</title>', {}, { 'content-type': 'text/html' })
      return { ...value, snapshot: { ...value.snapshot!, request_method: method, request_url: url } }
    }
    expect(htmlPageInsight(page('GET', 'https://org.example.test/login.jsp'))?.looksLikeLogin).toBe(false)
    // Only the path counts: a query that mentions login does not ask for the page.
    expect(htmlPageInsight(page('GET', 'https://org.example.test/home?next=login'))?.looksLikeLogin).toBe(true)
    // A POST to the login URL that answers with the login page again is unexpected.
    expect(htmlPageInsight(page('POST', 'https://org.example.test/login'))?.looksLikeLogin).toBe(true)
  })

  it('expects a login page from a GET to a login host, whatever its path', () => {
    const page = (method: string, url: string) => {
      const value = html('<title>Login | Salesforce</title><input type="password">', {}, { 'content-type': 'text/html' })
      return { ...value, snapshot: { ...value.snapshot!, request_method: method, request_url: url } }
    }
    // Salesforce scripts start with this GET: a passing first step is not flagged.
    expect(htmlPageInsight(page('GET', 'https://login.salesforce.com/'))?.looksLikeLogin).toBe(false)
    expect(htmlPageInsight(page('GET', 'https://test.salesforce.com/'))?.looksLikeLogin).toBe(false)
    expect(htmlPageInsight(page('GET', 'https://LOGIN.example.com/?startURL=%2Fhome'))?.looksLikeLogin).toBe(false)
    expect(htmlPageInsight(page('GET', 'https://signin.example.com/'))?.looksLikeLogin).toBe(false)
    // A POST that lands on the login page is still flagged, even on a login host.
    expect(htmlPageInsight(page('POST', 'https://login.salesforce.com/'))?.looksLikeLogin).toBe(true)
    // An org's My Domain, sandbox and Lightning hosts serve their login page at the root.
    expect(htmlPageInsight(page('GET', 'https://acme.my.salesforce.com/'))?.looksLikeLogin).toBe(false)
    expect(htmlPageInsight(page('GET', 'https://acme.my.salesforce.com/?startURL=%2Fhome'))?.looksLikeLogin).toBe(false)
    expect(htmlPageInsight(page('GET', 'https://acme--uat.sandbox.my.salesforce.com/'))?.looksLikeLogin).toBe(false)
    expect(htmlPageInsight(page('GET', 'https://acme.lightning.force.com/'))?.looksLikeLogin).toBe(false)
    expect(htmlPageInsight(page('GET', 'https://acme.my.salesforce.com/login'))?.looksLikeLogin).toBe(false)
    // Deeper org pages and POSTs that land on the login page are still flagged.
    expect(htmlPageInsight(page('GET', 'https://acme.my.salesforce.com/lightning/page/home'))?.looksLikeLogin).toBe(true)
    expect(htmlPageInsight(page('POST', 'https://acme.my.salesforce.com/'))?.looksLikeLogin).toBe(true)
    expect(htmlPageInsight(page('GET', 'https://acme.my.salesforce.com.evil.example/'))?.looksLikeLogin).toBe(true)
    // "login" elsewhere in the host is not a login host, and a relative URL has none.
    expect(htmlPageInsight(page('GET', 'https://bloginator.example.com/'))?.looksLikeLogin).toBe(true)
    expect(htmlPageInsight(page('GET', 'https://my.login.example.com/'))?.looksLikeLogin).toBe(true)
    expect(htmlPageInsight(page('GET', '/'))?.looksLikeLogin).toBe(true)
  })

  it('says when it read only part of the page: a partial capture or a retention cut', () => {
    const cut = html('<title>Login | Salesforce</title><p>...</p>...[truncated]')
    expect(htmlPageInsight(cut)).toEqual({ title: 'Login | Salesforce', looksLikeLogin: true, partial: true })
    const stream = html('<title>Feed</title>')
    stream.snapshot!.response_body_state = 'partial'
    expect(htmlPageInsight(stream)?.partial).toBe(true)
    const windowed = html('<title>Feed</title>', { protocol_evidence: { response_content_state: 'collection_window' } })
    expect(htmlPageInsight(windowed)?.partial).toBe(true)
    expect(htmlPageInsight(html('<title>Whole</title>'))?.partial).toBe(false)
  })

  it('recognises HTML by its body and says nothing for other or uncaptured bodies', () => {
    expect(htmlPageInsight(html('<!DOCTYPE html><title>A</title>', {}, {}))?.title).toBe('A')
    expect(htmlPageInsight(html('  <html><title>B</title>', {}, { 'Content-Type': 'text/plain' }))?.title).toBe('B')
    expect(htmlPageInsight(html('{"title":"<title>x</title>"}', {}, { 'Content-Type': 'application/json' }))).toBeNull()
    const redacted = html('<title>Login</title>')
    redacted.snapshot!.response_body_state = 'redacted'
    expect(htmlPageInsight(redacted)).toBeNull()
    const partial = html('<title>Login</title>')
    partial.snapshot!.response_body_state = 'partial'
    expect(htmlPageInsight(partial)?.title).toBe('Login')
    expect(htmlPageInsight(event())).toBeNull()
    expect(htmlPageInsight(event({ snapshot: { response_body: '', response_headers: { 'Content-Type': 'text/html' } } }))).toBeNull()
  })
})

describe('find in body', () => {
  it('counts literal, case-insensitive, non-overlapping matches', () => {
    expect(countLiteralMatches('cshc=1 CSHC=2 xcshcx', 'cshc')).toBe(3)
    expect(countLiteralMatches('aaaa', 'aa')).toBe(2)
    expect(countLiteralMatches('a.b a+b', '.')).toBe(1)
    expect(countLiteralMatches('abc', '')).toBe(0)
    expect(countLiteralMatches(undefined, 'a')).toBe(0)
  })

  it('reads the captured body without the retention marker, and marks a cut body partial', () => {
    const withBody = (response_body: string, overrides: Partial<BrowserReplayEvent> = {}, state: 'captured' | 'partial' = 'captured') =>
      event({ snapshot: { response_body, response_body_state: state }, ...overrides })
    // Retention leaves the state "captured" but appends its marker: partial, and the marker is not body text.
    expect(capturedResponseBody(withBody('{"token":"abc"...[truncated]'))).toEqual({ text: '{"token":"abc"', partial: true })
    expect(countLiteralMatches(capturedResponseBody(withBody('a...[truncated]'))!.text, 'truncated')).toBe(0)
    expect(capturedResponseBody(withBody('{"token":"abc"}'))).toEqual({ text: '{"token":"abc"}', partial: false })
    expect(capturedResponseBody(withBody('data: one', {}, 'partial'))).toEqual({ text: 'data: one', partial: true })
    // The event's own state counts when the snapshot has none.
    expect(capturedResponseBody(event({ response_body_state: 'partial', snapshot: { response_body: 'x' } }))?.partial).toBe(true)
    expect(capturedResponseBody(withBody('x', { protocol_evidence: { response_content_state: 'collection_window' } }))?.partial).toBe(true)
    // A marker inside the body is text; only a trailing one is retention's.
    expect(capturedResponseBody(withBody('a ...[truncated] b'))).toEqual({ text: 'a ...[truncated] b', partial: false })
    expect(capturedResponseBody(withBody(''))).toBeNull()
    expect(capturedResponseBody(event())).toBeNull()
  })

  it('says the count, and when the body is partial', () => {
    expect(formatFindResult(0, false)).toBe('No matches')
    expect(formatFindResult(1, false)).toBe('1 match')
    expect(formatFindResult(3, false)).toBe('3 matches')
    expect(formatFindResult(3, true)).toBe('3 matches (partial body)')
  })
})

describe('network tab', () => {
  it("counts the Network table's rows: a resource and the entry for the same fetch count once", () => {
    const merged = event({
      network_entries: [{ method: 'GET', url: '/a.css', status_code: 200 }],
      snapshot: { resources: [{ request_url: '/a.css', response_status: 200, response_body_state: 'captured' }, { request_url: '/b.js', response_status: 404, response_body_state: 'not_captured' }] },
    } as Partial<BrowserReplayEvent>)
    // The 404 is a failed row: the badge says so in words, in the failure style.
    expect(inspectorTabBadge(merged, 'network')).toEqual({ text: '2 · 1 failed', failing: true })
    // A same-URL pair whose statuses disagree stays two rows rather than merging wrongly.
    const disagree = event({
      network_entries: [{ method: 'GET', url: '/a.css', status_code: 304 }],
      snapshot: { resources: [{ request_url: '/a.css', response_status: 200, response_body_state: 'captured' }] },
    } as Partial<BrowserReplayEvent>)
    expect(inspectorTabBadge(disagree, 'network')).toEqual({ text: '2', failing: false })
    expect(inspectorTabBadge(event(), 'network')).toBeNull()
  })

  it('counts the Correlation table rows, or names failed and unset ones', () => {
    const row = (direction: 'Extracts' | 'Reads' | 'Sets', tone: 'ok' | 'bad' | 'warn' | 'neutral') => ({ direction, tone })
    expect(inspectorTabBadge(event(), 'correlation', [row('Extracts', 'ok'), row('Reads', 'neutral'), row('Sets', 'neutral')])).toEqual({ text: '3', failing: false })
    expect(inspectorTabBadge(event(), 'correlation', [row('Extracts', 'bad'), row('Reads', 'neutral')])).toEqual({ text: '1 failed', failing: true })
    expect(inspectorTabBadge(event(), 'correlation', [row('Reads', 'bad'), row('Reads', 'bad')])).toEqual({ text: '2 unset', failing: true })
    expect(inspectorTabBadge(event(), 'correlation', [row('Extracts', 'bad'), row('Reads', 'bad')])).toEqual({ text: '1 failed · 1 unset', failing: true })
    expect(inspectorTabBadge(event(), 'correlation', [])).toBeNull()
  })

  it('names the unresolved references the request sent', () => {
    const failed = event({ success: false, diagnosis: { category: 'correlation', severity: 'error', cause: 'x', unresolved_variables: ['a', 'b'] } })
    expect(inspectorTabBadge(failed, 'request')).toEqual({ text: '2 unresolved', failing: true })
    expect(inspectorTabBadge(event(), 'request')).toBeNull()
    expect(inspectorTabBadge(failed, 'response')).toBeNull()
  })

  it("reads the event's own network entries before its snapshot's (re-exported for existing callers)", () => {
    const own = [{ method: 'GET', url: '/own' }]
    const snap = [{ method: 'GET', url: '/snap' }]
    expect(replayNetworkEntries(event({ network_entries: own, snapshot: { network_entries: snap } } as Partial<BrowserReplayEvent>))).toBe(own)
    expect(replayNetworkEntries(event({ snapshot: { network_entries: snap } } as Partial<BrowserReplayEvent>))).toBe(snap)
  })

  it("offers Network for any other request, and not for the event's own request alone", () => {
    const own = { method: 'POST', url: 'https://api.example.test/checkout' }
    const withSnapshot = (entries: unknown[], extra: Record<string, unknown> = {}) => event({
      network_entries: entries, snapshot: { request_method: 'POST', request_url: 'https://api.example.test/checkout', ...extra },
    } as Partial<BrowserReplayEvent>)
    expect(availableInspectorTabs(withSnapshot([own])).has('network')).toBe(false)
    // The relative target names the same request.
    expect(availableInspectorTabs(event({ network_entries: [{ method: 'POST', url: '/checkout' }] } as Partial<BrowserReplayEvent>)).has('network')).toBe(false)
    expect(availableInspectorTabs(withSnapshot([own, { method: 'GET', url: '/app.js' }])).has('network')).toBe(true)
    expect(availableInspectorTabs(withSnapshot([{ method: 'GET', url: 'https://api.example.test/checkout' }])).has('network')).toBe(true)
    expect(availableInspectorTabs(withSnapshot([], { omitted_resources: 3 })).has('network')).toBe(true)
  })
})
