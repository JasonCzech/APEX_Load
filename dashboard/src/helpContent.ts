import catalog from '../../docs/help-content-catalog.json'
import { type HelpAreaID } from './apiGuideNavigation'
import { markdownPlainText, parseMarkdown, type MarkdownBlock } from './components/repositoryGuideMarkdown'

export type HelpArticle = { id: string; area: HelpAreaID; title: string; path: string; sections?: string[]; group?: string; owner: string }
export const helpArticles = catalog as HelpArticle[]
// Explicit catalogue membership is the trust boundary. Sources load in separate chunks.
const sources = import.meta.glob<string>([
  '../../docs/APPLICATION_USER_GUIDE.md',
  '../../docs/NATIVE_ANALYTICS.md',
  '../../docs/HELP_CENTER.md',
  '../../docs/PROTOCOL_SUPPORT.md',
  '../../docs/guides/BROWSER_DOCUMENT_EVIDENCE.md',
  '../../docs/guides/BROWSER_INTELLIGENCE_WORKFLOWS.md',
  '../../docs/guides/BROWSER_INTERACTION_EVIDENCE.md',
  '../../docs/guides/BROWSER_HAR_WORKING_OWNERSHIP.md',
  '../../docs/guides/RETAINED_RESOURCE_INSPECTION.md',
  '../../docs/guides/HTTP3_AND_QUIC.md',
  '../../docs/guides/TELEMETRY_POLICY_RECEIPTS.md',
  '../../docs/guides/AUTHORING_SCENARIO_FIDELITY.md',
  '../../docs/OBSERVABILITY_INTEGRATIONS.md',
  '../../docs/DYNATRACE.md',
  '../../docs/ELASTIC.md',
  '../../docs/IMPORT_FIDELITY_AND_REPAIRS.md',
  '../../docs/CLUSTERS_AND_CLOUD.md',
  '../../docs/ENTERPRISE_PLATFORM_RUNBOOK.md',
  '../../docs/DATA_MODEL.md',
  '../../docs/ADMIN_USER_GUIDE.md',
  '../../docs/AUTH_RBAC_OPERATIONS_RUNBOOK.md',
  '../../docs/API_USER_GUIDE.md',
  '../../docs/CICD_INTEGRATION.md',
  '../../mcp/README.md',
  '../../docs/HELP_DSL_OPERATOR_GUIDE.md',
  '../../docs/DSL_ACTION_REFERENCE.md',
  '../../docs/RUNTIME_SETTINGS_PARITY.md',
  '../../docs/SCRIPT_DEBUGGER_GUIDE.md',
  '../../docs/LIVE_DATA_TABLES_RUNTIME.md',
  '../../docs/PROJECT_LIVE_TABLES.md',
  '../../docs/NATIVE_SCRIPT_DATA_SERVICES.md',
  '../../docs/DISTRIBUTED_RENDEZVOUS.md',
  '../../docs/recording-agent.md',
  '../../docs/ACCESS_SURFACES.md',
  '../../deployments/README.md',
  '../../deployments/k8s/local/README.md',
  '../../deployments/k8s/onprem/README.md',
  '../../deployments/k8s/aks/README.md',
  '../../deployments/k8s/eks/README.md',
  '../../deployments/helm/apexload/README.md',
  '../../deployments/docker/README.md',
  '../../docs/LOADRUNNER_COVERAGE.md',
], { query: '?raw', import: 'default' })
const pending = new Map<string, Promise<string>>()
const loaded = new Map<string, string>()
export function loadHelpSource(article: HelpArticle): Promise<string> {
  const key = `../../${article.path}`
  let result = pending.get(key)
  if (!result) {
    const loader = sources[key]
    result = loader
      ? loader().then(text => { loaded.set(article.path, text); return text })
      : Promise.reject(new Error('This article is not bundled in this build.'))
    pending.set(key, result)
    result.catch(() => pending.delete(key))
  }
  return result
}
export function helpArticleHash(article: Pick<HelpArticle, 'area' | 'id'>, section?: string): string {
  const params = new URLSearchParams({ guide: article.area, article: article.id })
  if (section) params.set('section', section)
  return `#/docs?${params}`
}
export function articleBlocks(article: HelpArticle, source: string): MarkdownBlock[] {
  const blocks = parseMarkdown(source)
  if (!article.sections) return blocks.filter(block => block.kind !== 'heading' || block.level !== 1)
  let include = false
  return blocks.filter(block => {
    if (block.kind === 'heading' && block.level <= 2) include = block.level === 2 && article.sections!.includes(block.text)
    return include
  })
}
// Repository links use GitHub heading slugs; the help renderer keeps its existing IDs.
// Resolve aliases only against parsed headings, never arbitrary paths or HTML.
export function helpSectionID(source: string, section: string): string {
  const headings = parseMarkdown(source).filter(block => block.kind === 'heading')
  if (headings.some(heading => heading.id === section)) return section
  const counts = new Map<string, number>()
  for (const heading of headings) {
    const base = markdownPlainText(heading.text).toLowerCase().replace(/[^\p{L}\p{N}\p{M}_ -]/gu, '').replace(/ /g, '-')
    const count = counts.get(base) || 0
    counts.set(base, count + 1)
    if (`${base}${count ? `-${count}` : ''}` === section) return heading.id
  }
  return section
}
export function articleForSection(path: string, section: string, source: string): HelpArticle | undefined {
  section = helpSectionID(source, section)
  let parent = ''
  for (const block of parseMarkdown(source)) {
    if (block.kind !== 'heading') continue
    if (block.level === 2) parent = block.text
    if (block.id === section) return helpArticles.find(article => article.path === path && (!article.sections || article.sections.includes(parent)))
  }
  return undefined
}
function sectionSlug(value: string): string {
  return value.toLowerCase().replace(/&/g, '').replace(/[^a-z0-9 -]/g, '').trim().replace(/[\s-]+/g, '-')
}
function matchesSectionSlug(name: string, section: string): boolean {
  const target = sectionSlug(section)
  return sectionSlug(name) === target || sectionSlug(name.replace(/\//g, ' ')) === target
}
const CROSS_FILE_SECTION_OWNER: Record<string, string> = {
  'docs/CICD_INTEGRATION.md#root-cause-summary': 'github',
  'docs/CICD_INTEGRATION.md#run-validity-gate': 'github',
  'docs/CICD_INTEGRATION.md#telemetry-acceptance-gate': 'github',
  'docs/ENTERPRISE_PLATFORM_RUNBOOK.md#postgresql-backup-and-restore': 'operations',
  'docs/ENTERPRISE_PLATFORM_RUNBOOK.md#ha-controller-behavior': 'platform',
  'docs/OBSERVABILITY_INTEGRATIONS.md#provider-qualification': 'monitoring-governance',
  'docs/PROTOCOL_SUPPORT.md#shared-request-import-pipeline': 'web-api-support',
  'docs/PROTOCOL_SUPPORT.md#compiling-the-script-source': 'java',
  'docs/PROTOCOL_SUPPORT.md#tested-adopter-dispositions': 'web-api-support',
  'docs/PROTOCOL_SUPPORT.md#jmeter-messaging-fidelity': 'messaging',
  'docs/HELP_CENTER.md#authentication-or-permission-denied': 'troubleshooting',
  'docs/HELP_CENTER.md#create-and-prove-the-script': 'first-test',
}
const COMPANION_ARTICLE_MAP: Record<string, { id: string; section?: string }> = {
  'docs/ROOT_CAUSE_ANALYSIS.md': { id: 'root-cause', section: 'root-cause-analysis' },
  'docs/PROMETHEUS_MONITORING.md': { id: 'monitoring-governance', section: 'prometheus-target-system-test-monitoring' },
  'docs/PROMETHEUS_MONITORING_SPEC.md': { id: 'monitoring-governance', section: 'prometheus-target-system-test-monitoring' },
  'docs/DYNATRACE_SPEC.md': { id: 'dynatrace-monitoring' },
  'docs/ELASTIC_SPEC.md': { id: 'elastic-monitoring' },
  'docs/ELASTIC_QUALIFICATION.md': { id: 'elastic-monitoring' },
  'docs/HTTP3_IMPLEMENTATION.md': { id: 'http3-quic' },
  'docs/RETIRED_INTEGRATIONS.md': { id: 'monitoring-governance', section: 'retired-monitoring-integration' },
  'docs/ARCHIVE_SOURCE_RETENTION.md': { id: 'operations', section: 'enterprise-data-platform-operations-runbook' },
  'docs/ENTERPRISE_FLEET_AND_CAPACITY.md': { id: 'restart', section: 'kubernetes-restart-controls' },
  'docs/LIVE_DATA_TABLES_VTS_COMPATIBILITY.md': { id: 'native-script-data-services' },
  'docs/ANALYSIS_WORKFLOWS.md': { id: 'native-analytics' },
  'docs/BASELINE_COMPARISON.md': { id: 'results', section: 'reading-results-and-reports' },
  'docs/JAVA_VUSER_POOLING_IMPLEMENTATION.md': { id: 'java', section: 'java-vuser-execution' },
  'docs/ENTERPRISE_CONTRACTS.md': { id: 'runtime' },
  'docs/EVENT_HUBS_HOWTO.md': { id: 'messaging', section: 'broker-and-messaging-support' },
  'docs/MONITORING_AND_PREPARATION.md': { id: 'messaging', section: 'broker-and-messaging-support' },
  'docs/GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md': { id: 'github', section: 'github-enterprise-integration' },
  'docs/HA_RUN_CONTINUITY_PLAN.md': { id: 'platform', section: 'enterprise-platform-architecture-runbook' },
  'docs/NODE_STORAGE_RECOVERY.md': { id: 'operations', section: 'enterprise-data-platform-operations-runbook' },
  'docs/NODE_FAILURE_READINESS_PLAN.md': { id: 'platform', section: 'enterprise-platform-architecture-runbook' },
  'docs/PERFORMANCE_BENCHMARKS.md': { id: 'browser', section: 'browser-execution-and-mobile-emulation' },
}
export function resolveHelpLink(href: string, current: HelpArticle, source: string): string {
  if (href.startsWith('#/')) return href
  if (/^(https?:|mailto:)/i.test(href)) {
    if (/github\.com|admhelp\.microfocus\.com/i.test(href)) return ''
    return href
  }
  const [rawPath, rawSection] = href.split('#')
  let section: string
  try { section = decodeURIComponent(rawSection || '') } catch { return '' }
  if (!rawPath) {
    section = helpSectionID(source, section)
    const target = articleForSection(current.path, section, source) || current
    return helpArticleHash(target, section)
  }
  // Resolve only ordinary document-relative paths, never authority/root URLs or schemes.
  if (/^(\/|\\)|[:?\\]/.test(rawPath)) return ''
  const parts = current.path.split('/').slice(0, -1)
  for (const part of rawPath.split('/')) {
    if (part === '..') { if (!parts.length) return ''; parts.pop() }
    else if (part && part !== '.') parts.push(part)
  }
  const path = parts.join('/')
  const candidates = helpArticles.filter(article => article.path === path)
  const loadedTargetSource = loaded.get(path)
  const sectionOwnerId = section ? CROSS_FILE_SECTION_OWNER[`${path}#${section}`] : undefined
  const bySection = section
    ? candidates.find(article => article.sections?.some(name => matchesSectionSlug(name, section)))
      || (sectionOwnerId ? candidates.find(article => article.id === sectionOwnerId) : undefined)
    : undefined
  const fallback = current.area !== 'admin-guide'
    ? candidates.find(article => article.area === current.area) || candidates.find(article => article.area !== 'admin-guide') || candidates[0]
    : candidates[0]
  const target = bySection || fallback
  if (target) return helpArticleHash(target, section ? (loadedTargetSource ? helpSectionID(loadedTargetSource, section) : section) : undefined)
  const companion = COMPANION_ARTICLE_MAP[path]
  if (companion) {
    const companionArticle = helpArticles.find(article => article.id === companion.id)
    if (companionArticle) return helpArticleHash(companionArticle, companion.section)
  }
  if (path === 'docs/swagger.yaml' || path === 'docs/api-endpoint-catalog.json') return '#/swagger'
  if (path === 'docs/dsl.schema.json') return '#/docs?guide=user-guide&article=dsl'
  return ''
}
export function helpBlockText(blocks: MarkdownBlock[]): string {
  return blocks.map(block => {
    if (block.kind === 'hr') return ''
    if (block.kind === 'heading' || block.kind === 'paragraph' || block.kind === 'blockquote') return markdownPlainText(block.text)
    if (block.kind === 'code') return block.code
    if (block.kind === 'table') return [block.headers, ...block.rows].flat().map(markdownPlainText).join(' ')
    const listText = (items: typeof block.items): string => items.map(item => markdownPlainText(item.text) + (item.children ? ' ' + listText(item.children.items) : '')).join(' ')
    return listText(block.items)
  }).join(' ').replace(/[`*#]/g, '')
}
export function searchHelp(articles: { article: HelpArticle; source: string }[], query: string) {
  const terms = query.trim().toLowerCase().split(/\s+/).filter(Boolean)
  if (!terms.length) return []
  return articles.flatMap(({ article, source }) => {
    const blocks = articleBlocks(article, source)
    const text = helpBlockText(blocks)
    const haystack = `${article.title} ${text}`.toLowerCase()
    if (!terms.every(term => haystack.includes(term))) return []
    const index = Math.max(0, text.toLowerCase().indexOf(terms[0]))
    let section: string | undefined
    let heading: string | undefined
    for (const block of blocks) {
      if (block.kind === 'heading') heading = block.id
      if (helpBlockText([block]).toLowerCase().includes(terms[0])) { section = heading; break }
    }
    return [{ article, section, snippet: `${index > 50 ? '…' : ''}${text.slice(Math.max(0, index - 50), index + 160)}…`, rank: terms.filter(term => article.title.toLowerCase().includes(term)).length }]
  }).sort((a, b) => b.rank - a.rank || a.article.title.localeCompare(b.article.title))
}
