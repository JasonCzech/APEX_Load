import { isSensitiveCredentialPreviewKey } from './credentialPreviewPolicy'
import fieldSchema from '../../../pkg/api/import_settings_fields.json'
import type { ScriptDefinition } from '../types/scripts'
import { getImportSummary } from './scriptEditorImportModel'
import { readImportedRuntimeFiles, readImportedRuntimeSettingDispositions } from './scriptEditorRuntimeSettingsModel'
import { draftRuntimeSettingNote, runtimeSettingEffects } from './importRuntimeSettingNotes'

type Field = { category: string; path: string; label: string; type: string; options?: string[] }
export const importRuntimeFields: Field[] = fieldSchema
export const readSetting = (script: ScriptDefinition, path: string): unknown => {
 const value = path.split('.').reduce<unknown>((v, key) => v && typeof v === 'object' ? (v as Record<string, unknown>)[key] : undefined, script)
 // Zero percentage is executable (including an explicit 0–0 range), and Go
 // omits zero-valued bounds from JSON. It is not an unavailable source value.
 if (value === undefined && script.config?.think_time?.type === 'random_percentage' && ['config.think_time.min_percent', 'config.think_time.max_percent'].includes(path)) return 0
 return value
}
const statuses = ['Requires decision', 'Mapped for replay', 'Preserved only', 'Not applicable']
const groupForStatus = (status: string) => status === 'executed' || status === 'mapped' ? statuses[1] : status === 'compatibility_only' ? statuses[2] : status === 'non_applicable' ? statuses[3] : statuses[0]
const parityFallbackNote = (group: string, concept: string, path?: string, label?: string) => {
 const subject = label || concept
 const effect = path && runtimeSettingEffects[path]
 if (group === statuses[1]) return `The importer mapped source ${concept} to ${subject}. ${effect || 'This setting affects replay behavior.'} Verify the draft value against the source before replay.`
 if (group === statuses[2]) return `Source ${concept} is retained for reference but is not applied to ${subject} during replay. ${effect || 'Review whether the draft needs an equivalent setting.'}`
 if (group === statuses[3]) return `Source ${concept} was marked not applicable to this replay. ${effect ? `The draft ${subject} still ${effect.charAt(0).toLowerCase()}${effect.slice(1)}` : 'Confirm this source behavior is not needed.'}`
 return `The importer could not confirm a replay mapping for source ${concept}${path ? ` to ${subject}` : ''}. ${effect || 'The source behavior may need an explicit replacement.'} Review the source behavior and adjust the draft before replay.`
}
const sourceFallbackNote = (file: string, section: string, key: string, group: string) => {
 const source = `${key} in ${file} / ${section}`
 if (group === statuses[3]) return `${source} was marked not applicable to replay; its value is retained for inspection.`
 if (group === statuses[2]) return `${source} is preserved as source evidence, but no editable replay destination was identified. Check whether its behavior is needed.`
 if (group === statuses[1]) return `${source} is reported as mapped, but the importer did not identify an editable destination here. Verify the mapped behavior in the draft.`
 return `${source} has no confirmed replay mapping or editable destination. Check whether the draft needs an equivalent behavior before replay.`
}
export type RuntimeSettingRow = { id: string; category: string; label: string; path?: string; imported: string; importedSummary?: string; note: string; field?: Field }
const sourceAliases: Record<string, string[]> = {
 'config.results.message_detail.mode': ['EnableSnapshotsInReplay'],
 'config.results.message_detail.sample_rate': ['EnableSnapshotsInReplay'],
 'config.results.message_detail.include_resources': ['SaveSnapshotResources'],
 'config.http.receive_buffer_bytes': ['NetBufSize'],
 'config.http.max_meta_refresh': ['MaxSelfMetaRefreshCount'],
 'config.http.content_check_error_limit': ['MaxErrorMatchesAsERRORS'],
 'config.log.error_buffer_bytes': ['AutoLogBufferSize'],
 'config.http.http_errors_as_warnings': ['HttpErrorsAsWarnings'],
 'config.http.fail_noncritical_resources': ['FailNonCriticalItem'],
 'config.http.resource_timeout_as_warning': ['ResourcePageTimeoutIsWarning'],
 'config.http.legacy_html_checks': ['EnableChecks'],
 'config.http.dns_cache': ['EnableIPCache'],
 'config.http.challenge_delay_ms': ['Retry401ThinkTime'],
 'config.error_handling.fail_transaction_on_error_message': ['FailTransOnErrorMsg'],
 'config.browser_emulation.cache.clear_cache_for_new_user': ['ClearCacheForSimulateNewUser'],
 'config.log.max_response_bytes': ['LogMaxResponseSize'],

 'config.http.accept_language': ['AcceptLanguage', 'BrowserAcceptLanguage', 'Language'],
 'config.http.accept_encoding': ['AcceptEncoding', 'BrowserAcceptEncoding', 'UseGzip'],
 'config.browser_emulation.custom_user_agent': ['UseCustomAgent', 'CustomUserAgent', 'UserAgent', 'BrowserUserAgent', 'UseBrowserAgent'],
 'config.browser_emulation.language': ['BrowserAcceptLanguage', 'BrowserLanguageCode', 'Language', 'AcceptLanguage'],
 'config.http.proxy_mode': ['ProxyMode', 'ProxyUseProxy', 'ProxyUseProxyServer', 'ProxyUseBrowser', 'ProxyUseAutoConfigScript'],
 'config.http.proxy.url': ['ProxyURL', 'ProxyHTTPHost', 'ProxyHTTPPort', 'ProxyHTTPSHost', 'ProxyHTTPSPort', 'ProxyUseSame'],
 'config.http.version': ['HTTPVersion', 'HTTPVer', 'EnableHTTP2', 'HTTP2Enabled'],
 'config.http.connect_timeout_ms': ['ConnectTimeout'],
 'config.http.request_timeout_ms': ['ReceiveTimeout', 'StepDownloadTimeout'],
 'config.http.idle_timeout_ms': ['KeepAliveTimeout'],
 'config.http.page_timeout_ms': ['PageDownloadTimeout'],
 'config.http.ip_version': ['IPVersionPolicy'],
 'config.http.cache_max_unused_iterations': ['DeleteCacheUnreferencedIterations'],
 'config.http.html_parse_mode': ['ParseHtmlContentType'],
 'config.http.max_redirects': ['MaxRedirections', 'MaxRedirectionDepth'],
 'config.http.keep_alive': ['KeepAlive'],
 'config.http.max_conns_per_host': ['MaxConnectionsPerHost', 'MaxConnections', 'MaxConnectionsPerHostHTTP'],
 'config.log.level': ['Level', 'LogOptions', 'LogLevel'],
 'config.log.on_error_only': ['OnErrorOnly', 'LogOnError', 'LogOnErrorOnly', 'AutoLog'],
 'config.log.extended_options': ['ExtendedOptions', 'ExtendedLogOptions', 'ParameterSubstitution', 'DataReturned', 'AdvancedTrace', 'MsgClassParameters', 'MsgClassData', 'MsgClassFull'],
 'config.think_time.type': ['Options'],
 'config.think_time.ignore': ['Options', 'Ignore', 'IgnoreThinkTime'],
 'config.think_time.multiplier': ['Options', 'Factor', 'Multiplier', 'MultiplyRecordedThinkTimeBy'],
 'config.think_time.limit_ms': ['LimitFlag', 'Limit', 'LimitMs', 'LimitThinkTimeMs'],
 'config.think_time.min_percent': ['ThinkTimeRandomLow'],
 'config.think_time.max_percent': ['ThinkTimeRandomHigh'],
 'config.pacing.type': ['Mode', 'PacingMode', 'IterationPace', 'RunLogicPaceType'],
 'config.pacing.interval_ms': ['IntervalMs', 'Interval', 'StartEvery', 'RunLogicPaceConstTime'],
 'config.pacing.delay_ms': ['DelayMs', 'Delay', 'StartEvery', 'RunLogicPaceConstAfterTime'],
 'config.pacing.min_interval_ms': ['MinIntervalMs', 'MinInterval', 'RandomMin', 'RunLogicRandomPaceMin'],
 'config.pacing.max_interval_ms': ['MaxIntervalMs', 'MaxInterval', 'RandomMax', 'RunLogicRandomPaceMax'],
 'config.pacing.min_delay_ms': ['MinDelayMs', 'MinDelay', 'RandomMin', 'RunLogicAfterPaceMin'],
 'config.pacing.max_delay_ms': ['MaxDelayMs', 'MaxDelay', 'RandomMax', 'RunLogicAfterPaceMax'],
}
export function runtimeSettingRows(script: ScriptDefinition): Record<string, RuntimeSettingRow[]> {
 const metadata = script.metadata?.import
 const parity = getImportSummary(metadata).runtimeSettingsParity
 const sourceFiles = readImportedRuntimeFiles(metadata)
 const dispositions = readImportedRuntimeSettingDispositions(metadata)
 const groups: Record<string, RuntimeSettingRow[]> = Object.fromEntries(statuses.map(s => [s, []]))
 const covered = new Set<string>()
 for (const entry of parity) {
  if (['runtime_unmapped', 'runtime_non_applicable'].includes(entry.id) && Object.keys(dispositions).length) continue
  const group = groupForStatus(entry.status)
  const paths = entry.fields?.length ? [...new Set(entry.fields.flatMap(path => {
   const descendants = importRuntimeFields.filter(f => f.path.startsWith(`${path}.`))
   return descendants.length ? descendants.map(f => f.path) : [path]
  }))] : [undefined]
  for (const path of paths) {
   if (path && !settingIsRelevant(script, path)) continue
   const field = importRuntimeFields.find(f => f.path === path)
   const source: string[] = []
   const values: string[] = []
   for (const [file, sections] of Object.entries(dispositions)) for (const [section, keys] of Object.entries(sections)) for (const [key, disposition] of Object.entries(keys)) {
    if (disposition.status === 'non_applicable' && entry.status !== 'non_applicable') continue
    const aliases = path ? sourceAliases[path] : undefined
    const destinationMatch = path && disposition.fields?.some(destination => path === destination || path.startsWith(`${destination}.`))
    const aliasMatch = aliases?.some(alias => alias.toLowerCase() === key.toLowerCase())
    if (disposition.parity_id === entry.id && (!path || aliasMatch || (!aliases && destinationMatch))) {
     const value = sourceFiles[file]?.[section]?.[key]
     if (value === undefined) continue
     const protectedValue = isSensitiveCredentialPreviewKey(key) || /\[REDACTED\]|\[MASKED\]|<redacted>/i.test(value)
     source.push(`${key}: ${protectedValue ? 'Protected — manage in Credentials' : value === '' ? '(empty)' : value} (${file} / ${section})`)
     const displayed = protectedValue ? 'Protected — manage in Credentials' : value === '' ? '(empty)' : field?.type === 'boolean' && ['0', '1', 'true', 'false'].includes(value.toLowerCase()) ? ['1', 'true'].includes(value.toLowerCase()) ? 'Enabled' : 'Disabled' : value
     // These LoadRunner timeout keys are explicitly seconds; retain raw evidence below.
     values.push(path?.endsWith('_ms') && ['connecttimeout', 'receivetimeout', 'stepdownloadtimeout', 'keepalivetimeout', 'pagedownloadtimeout'].includes(key.toLowerCase()) && /^\d+(\.\d+)?$/.test(value) ? `${Number(value) * 1000} ms` : displayed)
     covered.add(`${file}/${section}/${key}`)
    }
   }
   if (!source.length && path) continue
   const existing = path && groups[group].find(row => row.path === path)
   if (existing) {
    // Several source concepts can map to one destination. Deduplicate the
    // setting control without dropping any of their immutable source evidence.
    existing.imported = [...new Set([...existing.imported.split('\n'), ...source])].join('\n') || 'Source evidence not supplied by this importer'
    existing.importedSummary = [...new Set([existing.importedSummary, ...values])].filter(Boolean).join(' · ')
    if (entry.notes && !existing.note.includes(entry.notes)) existing.note += `\n${entry.notes}`
    continue
   }
   groups[group].push({ id: `${entry.id}:${path || ''}`, path, category: field?.category || entry.area, label: field?.label || path?.replace(/^config\./, '').replace(/_/g, ' ') || entry.concept, importedSummary: [...new Set(values)].join(' · '), imported: source.join('\n') || 'Source evidence not supplied by this importer', note: entry.notes || parityFallbackNote(group, entry.concept, path, field?.label), field: group === statuses[2] || group === statuses[3] ? undefined : field })
  }
 }
 for (const [file, sections] of Object.entries(sourceFiles)) for (const [section, keys] of Object.entries(sections)) for (const [key, value] of Object.entries(keys)) {
  if (covered.has(`${file}/${section}/${key}`)) continue
  const disposition = dispositions[file]?.[section]?.[key]
  const parityEntry = parity.find(entry => entry.id === disposition?.parity_id)
  const group = dispositions[file] ? groupForStatus(parityEntry && disposition?.status === 'mapped' ? parityEntry.status : disposition?.status || '') : statuses[2]
  const protectedValue = isSensitiveCredentialPreviewKey(key) || /\[REDACTED\]|\[MASKED\]|<redacted>/i.test(value)
  groups[group].push({ id: `${file}/${section}/${key}`, category: section, label: key, importedSummary: protectedValue ? 'Protected — manage in Credentials' : value || '(empty)', imported: `${protectedValue ? 'Protected — manage in Credentials' : value === '' ? '(empty)' : value} (${file})`, note: [disposition?.reason?.replace(/_/g, ' '), disposition?.note || sourceFallbackNote(file, section, key, group)].filter(Boolean).join(' — ') })
 }
 // Importers without parity show actual settings, never invented mapping evidence.
 const known = new Set(Object.values(groups).flat().map(r => r.path).filter(Boolean))
 const defaults = ['Think time', 'Pacing', 'Error handling', ...(['http', 'http/1.1', 'http/2', 'browser', 'soap', 'java_http'].includes(script.protocol) ? ['HTTP'] : []), ...(script.protocol === 'kafka' ? ['Kafka'] : []), ...(script.protocol?.startsWith('jms') ? ['JMS'] : [])]
 const current = importRuntimeFields.filter(f => !known.has(f.path) && (readSetting(script, f.path) !== undefined || defaults.includes(f.category)) && settingIsRelevant(script, f.path))
 groups['Additional APEX settings'] = current.map(f => ({ id: f.path, category: f.category, label: f.label, path: f.path, imported: 'No source mapping reported', note: draftRuntimeSettingNote(f.path, f.label), field: f }))
 return groups
}

function settingIsRelevant(script: ScriptDefinition, path: string): boolean {
 const nativeReplayPolicies = ['http_errors_as_warnings', 'fail_noncritical_resources', 'resource_timeout_as_warning', 'legacy_html_checks', 'dns_cache', 'challenge_delay_ms']
 if (nativeReplayPolicies.some(key => path === `config.http.${key}`) && !['http', 'http/1.1', 'http/2', 'soap', 'java_http'].includes(script.protocol)) return false
 const think = script.config?.think_time?.type || 'fixed'
 const pacing = script.config?.pacing?.type || 'none'
 const pacingModes: Record<string, string[]> = { delay_ms: ['fixed_delay'], interval_ms: ['fixed_interval'], min_delay_ms: ['random_delay'], max_delay_ms: ['random_delay'], min_interval_ms: ['random_interval'], max_interval_ms: ['random_interval'], tps: ['tps'] }
 if (path.startsWith('config.pacing.')) return !pacingModes[path.split('.').pop()!] || pacingModes[path.split('.').pop()!].includes(pacing)
 if (path === 'config.think_time.min_percent' || path === 'config.think_time.max_percent') return think === 'random_percentage'
 if (path === 'config.think_time.mean_ms') return ['fixed', 'gaussian', 'normal'].includes(think)
 if (path === 'config.think_time.std_ms') return ['gaussian', 'normal'].includes(think)
 if (path === 'config.think_time.min_ms' || path === 'config.think_time.max_ms') return ['uniform', 'gaussian', 'normal'].includes(think)
 return true
}
