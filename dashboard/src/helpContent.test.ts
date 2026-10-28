import { settingsDestination, settingsOwner } from './components/settingsNavigation'
import { isSharedAssetsSection } from './components/sharedAssetsNavigation'
import { describe, expect, it } from 'vitest'
import { articleBlocks, articleForSection, helpArticles, helpArticleHash, helpSectionID, isBlockedHelpLink, loadHelpSource, resolveHelpLink, searchHelp } from './helpContent'
import { parseMarkdown } from './components/repositoryGuideMarkdown'
import { apiGuideHashForToggle, apiGuideToggleOptions, helpAreaForPage } from './apiGuideNavigation'

const first = helpArticles.find(a => a.id === 'first-test')!
describe('Help catalogue and content contracts', () => {
  it('keeps consolidated browser interpretation searchable and links capture contracts in app', async () => {
    const article = helpArticles.find(value => value.id === 'browser-intelligence')!
    const source = await loadHelpSource(article)
    expect(searchHelp([{ article, source }], 'weighted test-journey risk')).toHaveLength(1)
    expect(source).not.toContain("run report's Diagnostics")
    expect(resolveHelpLink('#the-browser-area-of-a-run-report', article, source)).toBe(helpArticleHash(article, 'the-browser-area-of-a-run-report'))
    expect(source).toContain('remaining metrics are never renormalized')
    expect(source).toContain('never revenue or conversion impact')
    for (const section of ['score', 'journey-accounting', 'patterns-and-heatmaps']) {
      expect(articleForSection(article.path, section, source)?.id).toBe(article.id)
      expect(resolveHelpLink(`#${section}`, article, source)).toBe(helpArticleHash(article, section))
    }
    for (const id of ['browser-document-evidence', 'browser-interaction-evidence', 'browser-har-ownership', 'retained-resource-inspection']) {
      const target = helpArticles.find(value => value.id === id)!
      expect(await loadHelpSource(target)).not.toBe('')
      expect(resolveHelpLink(target.path.split('/').pop()!, article, source)).toBe(helpArticleHash(target))
    }
    const capture = helpArticles.find(value => value.id === 'browser-interaction-evidence')!
    const captureSource = await loadHelpSource(capture)
    expect(captureSource).not.toContain('| `scripted_repetition` |')
    expect(resolveHelpLink('BROWSER_INTELLIGENCE_WORKFLOWS.md#patterns-and-heatmaps', capture, captureSource)).toBe(helpArticleHash(article, 'patterns-and-heatmaps'))
  })
  it('bundles retained resource inspection with searchable bounded-page guidance and honest coverage', async () => {
    const article = helpArticles.find(value => value.id === 'retained-resource-inspection')!
    expect(article.path).toBe('docs/guides/RETAINED_RESOURCE_INSPECTION.md')
    const source = await loadHelpSource(article)
    const results = searchHelp([{ article, source }], 'opaque continuation')
    expect(results).toHaveLength(1)
    expect(results[0].article.id).toBe(article.id)
    expect(resolveHelpLink('#filter-and-page', article, source)).toBe(helpArticleHash(article, 'filter-and-page'))
    expect(articleForSection(article.path, 'filter-and-page', source)?.id).toBe(article.id)
    expect(source).toContain('A zero filter match differs from an unavailable projection')
    expect(source).toContain('not a complete browser network census')
    expect(source).toContain('logical allocation bound, not a process-RSS attestation')
    const document = helpArticles.find(value => value.id === 'browser-document-evidence')!
    expect(resolveHelpLink('BROWSER_DOCUMENT_EVIDENCE.md', article, source)).toBe(helpArticleHash(document))
    const documentSource = await loadHelpSource(document)
    expect(documentSource).toContain('No-interaction INP is unavailable.')
  })
  it('bundles project table recovery and links its runtime and native service guides', async () => {
    const article = helpArticles.find(value => value.id === 'project-live-tables')!
    const source = await loadHelpSource(article)
    expect(searchHelp([{ article, source }], 'reset archive retry')).toHaveLength(1)
    for (const path of ['LIVE_DATA_TABLES_RUNTIME.md', 'NATIVE_SCRIPT_DATA_SERVICES.md']) {
      const target = helpArticles.find(value => value.path === `docs/${path}`)!
      expect(await loadHelpSource(target)).not.toBe('')
      expect(resolveHelpLink(path, article, source)).toBe(helpArticleHash(target))
    }
    const apiGuide = helpArticles.find(value => value.id === 'api-workflows')!
    expect(resolveHelpLink('PROJECT_LIVE_TABLES.md', apiGuide, await loadHelpSource(apiGuide))).toBe(helpArticleHash(article))
  })
  it('keeps repository-style fragments navigable across every bundled help source', async () => {
    for (const article of helpArticles) {
      const source = await loadHelpSource(article)
      const withoutCode = source.replace(/```[\s\S]*?```/g, '')
      for (const match of withoutCode.matchAll(/\[[^\]]+\]\(([^)]+)\)/g)) {
        const href = match[1]
        expect(isBlockedHelpLink(href), `${article.path}: ${href}`).toBe(false)
        if (/^(https?:|mailto:|#\/)/i.test(href)) continue
        const destination = resolveHelpLink(href, article, source)
        expect(destination, `${article.path}: ${href}`).not.toBe('')
        if (!destination.startsWith('#/docs?')) continue
        const params = new URLSearchParams(destination.split('?')[1])
        const target = helpArticles.find(candidate => candidate.id === params.get('article'))!
        expect(target, `${article.path}: ${href}`).toBeDefined()
        const section = params.get('section')
        if (!section) continue // a link to a whole article has no section to resolve
        expect(articleForSection(target.path, section, await loadHelpSource(target))?.id, `${article.path}: ${href}`).toBe(target.id)
      }
    }
  })
  it('blocks every GitHub host and LoadRunner documentation by hostname while allowing monitoring tool references', () => {
    for (const href of [
      'https://github.com/JasonCzech/APEX_Load/blob/main/docs/LOADRUNNER_COVERAGE.md',
      'https://GitHub.com./owner/repo',
      'https://docs.github.com/en/actions/security-for-github-actions/security-hardening-your-deployments/about-security-hardening-with-openid-connect',
      'https://raw.githubusercontent.com/owner/repo/main/README.md',
      'https://gist.github.com/owner/abc',
      'HTTPS://GITHUB.COM/owner/repo',
      'https://github.com../owner/repo',
      'https://user@github.com:443/owner/repo',
      // Same-scheme hrefs without `//` still open github.com in the renderer.
      'https:github.com/owner/repo',
      'HTTPS:github.com/owner/repo',
      'https:/github.com/owner/repo',
      'https:\\github.com/owner/repo',
      'https://admhelp.microfocus.com/lr/en/latest/help/WebHelp/Content/VuGen/t_HowToCreateJavaScript.htm',
      'https://admhelp.opentext.com/lr/en/latest/help/WebHelp/Content/Controller/c_scenario.htm',
    ]) {
      expect(isBlockedHelpLink(href), href).toBe(true)
      expect(resolveHelpLink(href, first, ''), href).toBe('')
    }
    // A protocol-relative link is reported as blocked and never becomes a link.
    expect(isBlockedHelpLink('//github.com/owner/repo')).toBe(true)
    expect(resolveHelpLink('//github.com/owner/repo', first, '')).toBe('')
    // Malformed and relative hrefs are not blocked hosts and do not throw.
    expect(isBlockedHelpLink('https://[bad')).toBe(false)
    expect(isBlockedHelpLink('PROTOCOL_SUPPORT.md#java-vuser-execution')).toBe(false)
    for (const href of [
      'https://docs.dynatrace.com/docs/platform/grail/dynatrace-query-language/commands/smartscape-commands',
      'https://example.com/search?ref=github.com',
      'https://notgithub.com/page',
      'https://learn.microsoft.com/en-us/azure/aks/',
      'mailto:perf-team@example.com',
    ]) {
      expect(isBlockedHelpLink(href), href).toBe(false)
      expect(resolveHelpLink(href, first, ''), href).toBe(href)
    }
  })
  it('routes non-admin links to shared sources to user-guide articles and bundles EKS and Java Vuser coverage', async () => {
    const webApi = helpArticles.find(a => a.id === 'web-api-support')!
    const webApiSource = await loadHelpSource(webApi)
    expect(resolveHelpLink('guides/HTTP3_AND_QUIC.md', webApi, webApiSource)).toBe('#/docs?guide=user-guide&article=http3-quic')
    const dynatrace = helpArticles.find(a => a.id === 'dynatrace-monitoring')!
    const dynatraceSource = await loadHelpSource(dynatrace)
    expect(resolveHelpLink('OBSERVABILITY_INTEGRATIONS.md#governed-provider-registry', dynatrace, dynatraceSource)).toBe(
      '#/docs?guide=user-guide&article=monitoring-governance&section=governed-provider-registry',
    )
    for (const id of ['install-eks', 'install-aws-terraform', 'lr-java-vuser']) {
      const article = helpArticles.find(a => a.id === id)!
      expect(article, id).toBeDefined()
      expect(articleBlocks(article, await loadHelpSource(article)).length, id).toBeGreaterThan(0)
    }
  })
  it('resolves punctuation and duplicate repository headings without changing existing help IDs', () => {
    const source = '# Guide\n\n## Web/API\n\n## Web/API\n\n## A & B\n\n## Café\n'
    expect(helpSectionID(source, 'webapi')).toBe('web-api')
    expect(helpSectionID(source, 'webapi-1')).toBe('web-api-1')
    expect(helpSectionID(source, 'web-api')).toBe('web-api')
    expect(helpSectionID(source, 'a--b')).toBe('a-b')
    expect(helpSectionID(source, 'café')).toBe('caf')
    expect(helpSectionID(source, 'unknown')).toBe('unknown')
  })
  it('loads every catalogue source and every selected task section', async () => {
    expect(new Set(helpArticles.map(a => a.id)).size).toBe(helpArticles.length)
    for (const article of helpArticles) {
      const source = await loadHelpSource(article)
      const headings = parseMarkdown(source).filter(block => block.kind === 'heading').map(block => block.text)
      for (const section of article.sections || []) expect(headings, `${article.id}: ${section}`).toContain(section)
      expect(articleBlocks(article, source).length, article.id).toBeGreaterThan(0)
    }
  })
  it('bundles distributed rendezvous and resolves its native data guide links', async () => {
    const article = helpArticles.find(value => value.id === 'distributed-rendezvous')!
    const source = await loadHelpSource(article)
    expect(searchHelp([{ article, source }], 'fixed timeout')).toHaveLength(1)
    expect(resolveHelpLink('#admission-and-lifecycle', article, source)).toBe(helpArticleHash(article, 'admission-and-lifecycle'))
    expect(articleForSection(article.path, 'admission-and-lifecycle', source)?.id).toBe(article.id)
    for (const path of ['LIVE_DATA_TABLES_RUNTIME.md', 'NATIVE_SCRIPT_DATA_SERVICES.md']) {
      const target = helpArticles.find(value => value.path === `docs/${path}`)!
      expect(source).toContain(`](${path})`)
      expect(resolveHelpLink(path, article, source)).toBe(helpArticleHash(target))
    }
  })
  it('bundles qualified workload-shape guardrail ownership without claiming the remaining AUTH work', async () => {
    const article = helpArticles.find(item => item.id === 'authoring-scenario-fidelity')!
    const source = await loadHelpSource(article)
    expect(source).toContain('Existing SLA, error, transaction and monitor guardrails survive unless the selected shape or template explicitly replaces that field.')
    expect(source).toContain('An explicitly empty rule array clears those rules.')
    expect(source).toContain('Scenario revision/CAS, request replay retirement and shared authoring-owner changes remain open')
  })
  it('links every core help document reference to a catalogue article or app route', async () => {
    for (const article of helpArticles.filter(a => a.path.includes('/HELP_'))) {
      const source = await loadHelpSource(article)
      const withoutCode = source.replace(/```[\s\S]*?```/g, '')
      for (const match of withoutCode.matchAll(/\[[^\]]+\]\(([^)]+)\)/g)) {
        const link = resolveHelpLink(match[1], article, source)
        expect(link, `${article.path}: ${match[1]}`).not.toBe('')
        if (link.startsWith('#/docs?')) {
          const params = new URLSearchParams(link.split('?')[1]); const target = helpArticles.find(a => a.id === params.get('article'))!
          expect(target).toBeDefined()
          if (params.get('section')) expect(articleForSection(target.path, params.get('section')!, await loadHelpSource(target)), match[1]).toBeDefined()
        }
      }
    }
  })
  it('keeps task links synchronized with the current Settings, Access and Shared assets owners', async () => {
    const paths = new Set<string>()
    for (const article of helpArticles.filter(a => a.path.includes('/HELP_') || ['docs/APPLICATION_USER_GUIDE.md', 'docs/ADMIN_USER_GUIDE.md', 'docs/API_USER_GUIDE.md'].includes(a.path))) {
      if (paths.has(article.path)) continue
      paths.add(article.path)
      const source = await loadHelpSource(article)
      for (const match of source.matchAll(/\]\(#\/(settings|access|repository)\?setting=([^&)]+)[^)]*\)/g)) {
        expect(settingsDestination(match[2]), match[0]).toBeDefined()
        expect(settingsOwner(match[2]), match[0]).toBe(match[1])
      }
      // What scripts share links to its Shared assets section.
      for (const match of source.matchAll(/\]\(#\/shared-assets(?:\?section=([^&)]+))?[^)]*\)/g)) {
        if (match[1]) expect(isSharedAssetsSection(match[1]), match[0]).toBe(true)
      }
    }
  })
  it('keeps examples complete and the key capability corrections intact', async () => {
    const source = await loadHelpSource(first)
    const example = parseMarkdown(source).find(block => block.kind === 'code' && block.language === 'json')
    expect(example?.kind).toBe('code')
    if (example?.kind !== 'code') return
    const script = JSON.parse(example.code).script
    expect(script.protocol).toBe('http')
    expect(script.config.base_url).toBe('http://127.0.0.1:8099')
    expect(script.actions[0].assertions).toContainEqual({ type: 'status_code', expected: 200 })
    const userSource = await loadHelpSource(helpArticles.find(a => a.id === 'authoring')!)
    expect(userSource).not.toContain('500 response counts as a success')
    expect(userSource).not.toContain('Trace and metric export')
    expect(userSource).not.toContain('will not be rejected at launch')
    const apiSource = await loadHelpSource(helpArticles.find(a => a.id === 'api-workflows')!)
    expect(apiSource).not.toContain('does not yet wait for run N')
    expect(apiSource).not.toContain('| Method |')
  })
  it('finds text in separate articles and returns shareable section destinations', async () => {
    const article = helpArticles.find(a => a.id === 'scenarios')!
    const docs = [{ article, source: await loadHelpSource(article) }, { article: first, source: await loadHelpSource(first) }]
    expect(searchHelp(docs, 'pacing 250').map(r => r.article.id)).toEqual(['scenarios'])
    expect(searchHelp(docs, 'zzzzzz')).toEqual([])
    expect(searchHelp(docs, '   ')).toEqual([])
    const result = searchHelp(docs, '270')[0]
    expect(helpArticleHash(result.article, result.section)).toContain('section=sizing-from-a-goal')
  })
  it.each(['goroutine', 'correlation', 'each_occurrence', 'fixed_delay', 'JMeter'])('finds operator guidance for %s with a valid destination', async query => {
    const article = helpArticles.find(a => a.id === 'dsl-operator-guide')!
    const source = await loadHelpSource(article)
    const results = searchHelp([{ article, source }], query)
    expect(results).toHaveLength(1)
    expect(resolveHelpLink('HELP_DSL_OPERATOR_GUIDE.md', first, '')).toBe(helpArticleHash(article))
    if (results[0].section) expect(articleForSection(article.path, results[0].section, source)?.id).toBe(article.id)
  })
  it.each(['//evil.example/x', '/etc/passwd', 'javascript:alert(1)', 'file:///etc/passwd', '../../../secret.md', 'https://user:pass@example.com'])('does not turn an unapproved relative source into a document route: %s', href => {
    const result = resolveHelpLink(href, first, '')
    expect(result.startsWith('#/docs')).toBe(false)
  })
  it('resolves source-relative and section links through the explicit catalogue', async () => {
    const source = await loadHelpSource(first)
    expect(resolveHelpLink('HELP_CENTER.md#prepare-a-support-request', first, source)).toContain('article=support')
    expect(resolveHelpLink('HELP_CENTER.md#prepare-a-support-request', first, source)).toContain('section=prepare-a-support-request')
    expect(resolveHelpLink('missing.md', first, source)).toBe('')
    expect(resolveHelpLink('#create-and-prove-the-script', first, source)).toContain('section=create-and-prove-the-script')
    expect(resolveHelpLink('dsl.schema.json', first, source)).toContain('article=dsl')
  })
  it.each([
    ['browser-document-evidence', 'provisional history', 'Browser document evidence'],
    ['telemetry-policy-receipts', 'semantic SHA-256', 'Telemetry budget policy receipts'],
    ['authoring-scenario-fidelity', 'sub-millisecond thresholds', 'Scenario and queued-run authoring fidelity'],
  ])('bundles searchable evidence guidance with a shareable destination: %s', async (id, query, title) => {
    const article = helpArticles.find(candidate => candidate.id === id)!
    expect(article).toMatchObject({ area: 'user-guide', title })
    const source = await loadHelpSource(article)
    const results = searchHelp([{ article, source }], query)
    expect(results).toHaveLength(1)
    expect(results[0].article).toBe(article)
    expect(resolveHelpLink(article.path.split('/').slice(1).join('/'), first, '')).toBe(helpArticleHash(article))
    if (results[0].section) expect(articleForSection(article.path, results[0].section, source)?.id).toBe(id)
    expect(source).toContain('remain open')
  })
  it('retains old URLs and groups their navigation under automation', () => {
    for (const alias of ['mcp', 'authentication', 'quick-start', 'swagger', 'repo-guide'] as const) expect(helpAreaForPage(alias)).toBe('general')
    expect(apiGuideHashForToggle('getting-started')).toBe('#/docs')
    expect(apiGuideHashForToggle('user-guide')).toBe('#/docs?guide=user-guide')
    expect(apiGuideToggleOptions({ includeAdminGuide: true })).toHaveLength(5)
    expect(apiGuideToggleOptions({ includeAdminGuide: false })).toHaveLength(4)
  })
  it.each([
    ['DYNATRACE.md#dynatrace-lifecycle-webhooks', 'dynatrace-monitoring', 'apexload.idempotency_key'],
    ['ELASTIC.md#bucketed-elasticsearch-capture', 'elastic-monitoring', 'never reinterpreted as intervals'],
    ['IMPORT_FIDELITY_AND_REPAIRS.md#shared-http-import-safety-contract', 'import-fidelity', 'executable_bounded'],
  ])('resolves consolidated contracts and search destinations: %s', async (href, id, query) => {
    const article = helpArticles.find(candidate => candidate.id === id)!
    const source = await loadHelpSource(article)
    expect(resolveHelpLink(href, first, '')).toContain(`article=${id}`)
    const results = searchHelp([{ article, source }], query)
    expect(results).toHaveLength(1)
    expect(articleForSection(article.path, results[0].section!, source)?.id).toBe(id)
  })

})
