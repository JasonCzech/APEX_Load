import catalog from '../../docs/help-content-catalog.json'
import { type HelpAreaID } from './apiGuideNavigation'
import { parseMarkdown, type MarkdownBlock } from './components/repositoryGuideMarkdown'

export type HelpArticle = { id: string; area: HelpAreaID; title: string; path: string; sections?: string[]; group?: string; owner: string }
export const helpArticles = catalog as HelpArticle[]
// Explicit catalogue membership is the trust boundary. Sources load in separate chunks.
const sources = import.meta.glob<string>([
  '../../docs/APPLICATION_USER_GUIDE.md',
  '../../docs/HELP_CENTER.md',
  '../../docs/PROTOCOL_SUPPORT.md',
  '../../docs/guides/BROWSER_DOCUMENT_EVIDENCE.md',
  '../../docs/guides/RETAINED_RESOURCE_INSPECTION.md',
  '../../docs/guides/HTTP3_AND_QUIC.md',
  '../../docs/guides/TELEMETRY_POLICY_RECEIPTS.md',
  '../../docs/guides/AUTHORING_SCENARIO_FIDELITY.md',
  '../../docs/OBSERVABILITY_INTEGRATIONS.md',
  '../../docs/CLUSTERS_AND_CLOUD.md',
  '../../docs/ENTERPRISE_PLATFORM_RUNBOOK.md',
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
  '../../deployments/helm/apexload/README.md',
  '../../deployments/docker/README.md',
  '../../docs/LOADRUNNER_COVERAGE.md',
], { query: '?raw', import: 'default' })
const pending = new Map<string, Promise<string>>()
export function loadHelpSource(article: HelpArticle): Promise<string> {
  const key = `../../${article.path}`
  let result = pending.get(key)
  if (!result) {
    const loader = sources[key]
    result = loader ? loader() : Promise.reject(new Error('This article is not bundled in this build.'))
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
export function articleForSection(path: string, section: string, source: string): HelpArticle | undefined {
  let parent = ''
  for (const block of parseMarkdown(source)) {
    if (block.kind !== 'heading') continue
    if (block.level === 2) parent = block.text
    if (block.id === section) return helpArticles.find(article => article.path === path && (!article.sections || article.sections.includes(parent)))
  }
  return undefined
}
function sectionSlug(value: string): string {
  return value.toLowerCase().replace(/&/g, '').replace(/[^a-z0-9 -]/g, '').trim().replace(/\s+/g, '-')
}
export function resolveHelpLink(href: string, current: HelpArticle, source: string): string {
  if (href.startsWith('#/')) return href
  if (/^(https?:|mailto:)/i.test(href)) return href // Renderer validates credentials/protocol again.
  const [rawPath, rawSection] = href.split('#')
  let section: string
  try { section = decodeURIComponent(rawSection || '') } catch { return '' }
  if (!rawPath) {
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
  const target = (section ? candidates.find(article => article.sections?.some(name => sectionSlug(name) === sectionSlug(section))) : undefined) || candidates[0]
  if (target) return helpArticleHash(target, section)
  if (path === 'docs/swagger.yaml' || path === 'docs/api-endpoint-catalog.json') return '#/swagger'
  if (path === 'docs/dsl.schema.json') return '#/docs?guide=user-guide&article=dsl'
  return ''
}
export function helpBlockText(blocks: MarkdownBlock[]): string {
  return blocks.map(block => {
    if (block.kind === 'heading' || block.kind === 'paragraph') return block.text
    if (block.kind === 'code') return block.code
    if (block.kind === 'table') return [block.headers, ...block.rows].flat().join(' ')
    const listText = (items: typeof block.items): string => items.map(item => item.text + (item.children ? ' ' + listText(item.children.items) : '')).join(' ')
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
