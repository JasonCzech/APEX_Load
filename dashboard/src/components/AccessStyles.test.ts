import { describe, expect, it } from 'vitest'
import { list, parse, type Root, type Rule } from 'postcss'
import ts from 'typescript'

async function files() {
  // @ts-expect-error Style contracts run in Node; application sources omit Node types.
  return await import('node:fs')
}
async function stylesheet(name: string) {
  return parse((await files()).readFileSync(new URL(name, import.meta.url), 'utf8') as string)
}
function ancestors(rule: Rule): string[][] {
  const values: string[][] = []
  for (let parent = rule.parent; parent && parent.type !== 'root'; parent = parent.parent) {
    if (parent.type === 'atrule') values.unshift([parent.name, parent.params])
  }
  return values
}
function ruleReceipt(tree: Root) {
  const rules: unknown[] = []
  tree.walkRules(rule => { rules.push([
    list.comma(rule.selector).map(value => value.trim()).join(','),
    ancestors(rule),
    rule.nodes.filter(node => node.type === 'decl').map(node => [node.prop, node.value, Boolean(node.important)]),
  ]) })
  return rules
}

const consumers = ['AccessPage', 'AccessNavigation', 'AccessIssuedKeyModal', 'AccessUsagePanel', 'AuditEventTable', 'CapacityCommitments', 'FeatureAccessPanel']

describe('access stylesheet ownership and preservation', () => {
  it('preserves the original scoped declarations, media contexts and rule order', async () => {
    const css = await stylesheet('./Access.css')
    // This fingerprint was frozen from the pre-split Pages.css AST, including
    // the access-only clone of the unchanged mixed 980px responsive rule. It was
    // re-frozen for the shared-theme pass: label case/tracking became the
    // --label-case/--label-tracking tokens, the pill-style .access-navigation
    // rules gave way to the shared section list, and .access-load-older styles
    // the end of server-batched event tables. Re-frozen again when accent-coloured
    // text moved to the contrast-safe --accent-text token (UXH-004), and again
    // when keyboard focus outlines moved to the shared --focus-ring token.
    // @ts-expect-error Style contracts run in Node; application sources omit Node types.
    const { createHash } = await import('node:crypto')
    expect(createHash('sha256').update(JSON.stringify(ruleReceipt(css))).digest('hex')).toBe('99ead86f9bb95678dfa5a970070cb8b2aa9fc913986a79a41773076718407614')
    css.walkRules(rule => { expect(rule.selectors.every(selector => /^\.access-[a-z0-9-]+(?:$|[^a-z0-9-])/i.test(selector))).toBe(true) })
  })

  it('keeps mixed global styles and the later responsive access override', async () => {
    const global = await stylesheet('./Pages.css')
    const access = await stylesheet('./Access.css')
    const mixed: Rule[] = [], cloned: Rule[] = []
    global.walkRules(rule => {
      expect(rule.selectors.every(selector => selector.startsWith('.access-'))).toBe(false)
      if (rule.selectors.includes('.api-guide-hero') && rule.selectors.includes('.access-workspace')) mixed.push(rule)
    })
    access.walkRules(rule => {
      if (rule.selectors.join(',') === '.access-workspace,.access-user-management,.access-preset-grid') cloned.push(rule)
    })
    expect(mixed).toHaveLength(1)
    expect(cloned).toHaveLength(1)
    expect(ancestors(mixed[0])).toEqual([['media', '(max-width: 980px)']])
    expect(ancestors(cloned[0])).toEqual(ancestors(mixed[0]))
    expect(cloned[0].nodes.map(node => node.toString())).toEqual(mixed[0].nodes.map(node => node.toString()))
    // The responsive clone must follow the desktop definitions even though its
    // stylesheet arrives after the shared page styles.
    expect(access.nodes.findIndex(node => node === cloned[0].parent)).toBeGreaterThan(access.nodes.findIndex(node => node.type === 'rule' && node.selector === '.access-workspace'))
  })

  it('loads access styles from every actual TSX class consumer, including shared widgets', async () => {
    const fs = await files()
    const css = await stylesheet('./Access.css')
    const classNames = new Set<string>()
    css.walkRules(rule => { for (const name of rule.selector.match(/access-[a-z0-9-]+/gi) || []) classNames.add(name) })
    const found: string[] = []
    for (const file of fs.readdirSync(new URL('.', import.meta.url)) as string[]) {
      if (!file.endsWith('.tsx') || file.includes('.test.')) continue
      const source = ts.createSourceFile(file, fs.readFileSync(new URL(file, import.meta.url), 'utf8') as string, ts.ScriptTarget.Latest, true)
      let usesAccessClass = false
      function visit(node: ts.Node) {
        if (ts.isStringLiteralLike(node) || node.kind === ts.SyntaxKind.TemplateHead || node.kind === ts.SyntaxKind.TemplateMiddle || node.kind === ts.SyntaxKind.TemplateTail) {
          const text = (node as ts.StringLiteralLike).text
          if (Array.from(text.matchAll(/(?<![a-z0-9_-])(access-[a-z0-9-]+)(?![a-z0-9_-])/gi)).some(match => classNames.has(match[1]))) usesAccessClass = true
        }
        ts.forEachChild(node, visit)
      }
      visit(source)
      if (!usesAccessClass) continue // only files that use an access class must import Access.css
      found.push(file.replace(/\.tsx$/, ''))
      expect(source.statements.filter(statement => ts.isImportDeclaration(statement) && ts.isStringLiteral(statement.moduleSpecifier) && statement.moduleSpecifier.text === './Access.css'), file).toHaveLength(1)
    }
    expect(found.sort()).toEqual([...consumers].sort())
    expect(fs.readFileSync(new URL('../App.tsx', import.meta.url), 'utf8')).not.toContain("import './components/Access.css'")
  })
})
