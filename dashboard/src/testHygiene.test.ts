import { describe, expect, it } from 'vitest'
import ts from 'typescript'

// A test that looks something up and returns early when it is missing passes
// without checking anything. So every name a guard returns or continues on is
// asserted truthy first, and a missing lookup fails the test:
//
//   expect(button).toBeTruthy()
//   if (!button) return
//
// The assertion is a statement of its own in the guard's block, after the
// name was last assigned there, or a statement inside the guard's own block.
// One in a callback, a condition or a comment may never run, so it does not
// count. Neither toBeDefined() nor not.toBeNull() counts either: the first
// accepts querySelector's null, the second find()'s undefined. A guard that
// returns early on purpose (an optional branch, an effect not mounted yet)
// says why in a trailing comment.

// Every file vitest runs: its default include from the dashboard root, dot
// directories too (exhaustive), minus its default and vite.config.ts's
// exclusions. Vite leaves out this file itself.
const tests = import.meta.glob<string>([
  '/**/*.{test,spec}.{ts,tsx,js,jsx,mts,cts,mjs,cjs,mtsx,ctsx,mjsx,cjsx}',
  '!**/node_modules/**',
  '!**/.git/**',
  '!/e2e/**',
  '!/e2e-recording/**/*.spec.ts',
], { query: '?raw', import: 'default', eager: true, exhaustive: true })

// The names a guard condition tests for absence: `!a`, `!a.b`, `!a || !b[0]`.
function guardedNames(condition: ts.Expression, source: ts.SourceFile): string[] | null {
  if (ts.isParenthesizedExpression(condition)) return guardedNames(condition.expression, source)
  if (ts.isBinaryExpression(condition) && condition.operatorToken.kind === ts.SyntaxKind.BarBarToken) {
    const left = guardedNames(condition.left, source)
    const right = guardedNames(condition.right, source)
    return left && right ? [...left, ...right] : null
  }
  if (ts.isPrefixUnaryExpression(condition) && condition.operator === ts.SyntaxKind.ExclamationToken) {
    let operand: ts.Expression = condition.operand
    while (ts.isParenthesizedExpression(operand)) operand = operand.expression
    if (ts.isIdentifier(operand) || ts.isPropertyAccessExpression(operand) || ts.isElementAccessExpression(operand)) {
      return [operand.getText(source)]
    }
  }
  return null
}

function endsEarly(statement: ts.Statement): boolean {
  return (ts.isReturnStatement(statement) && !statement.expression) || ts.isContinueStatement(statement)
}

// `expect(name).toBeTruthy()` (a message argument allowed) as a statement.
function assertsTruthy(statement: ts.Statement, name: string, source: ts.SourceFile): boolean {
  if (!ts.isExpressionStatement(statement)) return false
  const call = statement.expression
  if (!ts.isCallExpression(call) || !ts.isPropertyAccessExpression(call.expression) || call.expression.name.text !== 'toBeTruthy') return false
  const subject = call.expression.expression
  return ts.isCallExpression(subject) && ts.isIdentifier(subject.expression) && subject.expression.text === 'expect'
    && subject.arguments.length >= 1 && subject.arguments[0].getText(source) === name
}

function bindsName(target: ts.Node, root: string): boolean {
  if (ts.isIdentifier(target)) return target.text === root
  if (ts.isPropertyAccessExpression(target) || ts.isElementAccessExpression(target)) return false
  let found = false
  ts.forEachChild(target, child => { if (!found && bindsName(child, root)) found = true })
  return found
}

// Whether a statement may give the variable a new value: a declaration of it,
// or an assignment (`=`, `&&=`, …) to it, outside any function it defines.
function assigns(statement: ts.Statement, root: string): boolean {
  let found = false
  const visit = (node: ts.Node): void => {
    if (found || ts.isFunctionLike(node)) return
    if (ts.isVariableDeclaration(node) && bindsName(node.name, root)) found = true
    if (ts.isBinaryExpression(node) && node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment
      && node.operatorToken.kind <= ts.SyntaxKind.LastAssignment && bindsName(node.left, root)) found = true
    ts.forEachChild(node, visit)
  }
  visit(statement)
  return found
}

function rootOf(name: string): string {
  return name.match(/^[\w$]+/)?.[0] ?? name
}

// Returns "line:name" for each guarded name that is not asserted truthy.
function unassertedGuards(text: string): string[] {
  const source = ts.createSourceFile('test.tsx', text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const found: string[] = []
  const checkGuard = (guard: ts.IfStatement): void => {
    const names = guardedNames(guard.expression, source)
    if (!names) return
    const then = guard.thenStatement
    let inside: readonly ts.Statement[] = []
    if (ts.isBlock(then)) {
      // A block that throws fails the test; one that does not end by
      // returning or continuing does something else.
      const last = then.statements[then.statements.length - 1]
      if (!last || !endsEarly(last) || then.statements.some(ts.isThrowStatement)) return
      inside = then.statements
    } else if (!endsEarly(then)) {
      return // returns a value: a helper, not a test ending early
    }
    // A guard that says why it returns early does so on purpose.
    const commentAt = ts.isBlock(then) ? then.getStart(source) + 1 : guard.end
    if (ts.getTrailingCommentRanges(text, commentAt)?.some(range => range.kind === ts.SyntaxKind.SingleLineCommentTrivia)) return
    const parent = guard.parent
    const siblings: readonly ts.Statement[] = ts.isBlock(parent) || ts.isSourceFile(parent) || ts.isCaseClause(parent) || ts.isDefaultClause(parent)
      ? parent.statements
      : []
    const before = siblings.slice(0, siblings.indexOf(guard))
    const line = source.getLineAndCharacterOfPosition(guard.getStart(source)).line + 1
    for (const name of names) {
      if (inside.some(statement => assertsTruthy(statement, name, source))) continue
      let asserted = false
      for (let index = before.length - 1; index >= 0; index -= 1) {
        if (assertsTruthy(before[index], name, source)) { asserted = true; break }
        if (assigns(before[index], rootOf(name))) break
      }
      if (!asserted) found.push(`${line}:${name}`)
    }
  }
  const visit = (node: ts.Node): void => {
    if (ts.isIfStatement(node) && !node.elseStatement) checkGuard(node)
    ts.forEachChild(node, visit)
  }
  visit(source)
  return found
}

describe('test hygiene', () => {
  it('scans every test source vitest runs', () => {
    const paths = Object.keys(tests)
    expect(paths.length).toBeGreaterThan(100)
    expect(paths.some(path => path.endsWith('.test.js'))).toBe(true)
    expect(paths.some(path => path.startsWith('/e2e-recording/'))).toBe(true)
    expect(paths.some(path => path.includes('/node_modules/') || path.startsWith('/e2e/') || (path.startsWith('/e2e-recording/') && path.endsWith('.spec.ts')))).toBe(false)
  })

  it('never lets a missing lookup return a test early without failing it', () => {
    const offenders = Object.entries(tests)
      .flatMap(([path, source]) => unassertedGuards(source).map(site => `${path}:${site}`))
    expect(offenders).toEqual([])
  })

  // Each snippet is the body of a function, so line 2 is its first line.
  const flagged = (code: string) => unassertedGuards(`async function t() {\n${code}\n}\n`)

  it('recognizes the vacuous forms', () => {
    expect(flagged('const b = find()\nexpect(b).toBeDefined()\nif (!b) return')).toEqual(['4:b'])
    expect(flagged('const b = find()\nexpect(b).not.toBeNull()\nif (!b) return')).toEqual(['4:b'])
    expect(flagged('for (const b of items) {\nif(!b) continue\n}')).toEqual(['3:b'])
    expect(flagged('expect(a).toBeTruthy()\nif (!a || !b) return')).toEqual(['3:b'])
    expect(flagged('if (!a) {\n  return\n}')).toEqual(['2:a'])
    expect(flagged('if (!a)\n  return')).toEqual(['2:a'])
    expect(flagged('if (!a) { cleanup(); return }')).toEqual(['2:a'])
    expect(flagged('if (!a || !b) {\n  expect(a).not.toBeNull()\n  return\n}')).toEqual(['2:a', '2:b'])
    expect(flagged('expect(rows[0]).toBeTruthy()\nrows = q()\nif (!rows[0]) return')).toEqual(['4:rows[0]'])
    expect(flagged('expect(dialog).toBeTruthy()\n;({ dialog } = open())\nif (!dialog) return')).toEqual(['4:dialog'])
    expect(flagged("let el = q()\nexpect(el).toBeTruthy()\nel &&= el.querySelector('.x')\nif (!el) return")).toEqual(['5:el'])
    expect(flagged('if (!rows[0] || !a?.b) return')).toEqual(['2:rows[0]', '2:a?.b'])
  })

  it('does not count an assertion that may never run', () => {
    expect(flagged('const b = q()\nconst check = () => {\n  expect(b).toBeTruthy()\n}\nif (!b) return')).toEqual(['6:b'])
    expect(flagged('const b = q()\nif (strict) { expect(b).toBeTruthy() }\nif (!b) return')).toEqual(['4:b'])
    expect(flagged('const b = q()\nitems.forEach(() => expect(b).toBeTruthy())\nif (!b) return')).toEqual(['4:b'])
    expect(flagged('const b = q()\n// expect(b).toBeTruthy()\nif (!b) return')).toEqual(['4:b'])
    expect(flagged('expect(row).toBeTruthy()\nawait act(async () => {\nif (!row) return\nrow.click()\n})')).toEqual(['4:row'])
    expect(flagged('function clickA(el: Element | null) {\n  expect(el).toBeTruthy()\n  if (!el) return\n}\nfunction clickB(\n  container: Element,\n  el: Element | null,\n) {\n  if (!el) return\n}')).toEqual(['10:el'])
  })

  it('accepts asserted and deliberate guards', () => {
    expect(flagged('const b = find()\nexpect(b).toBeTruthy()\nif (!b) return')).toEqual([])
    expect(flagged("expect(b, 'the row').toBeTruthy()\nif (!b) return")).toEqual([])
    expect(flagged('if (!a || !b) {\n  expect(a).toBeTruthy()\n  expect(b).toBeTruthy()\n  return\n}')).toEqual([])
    expect(flagged('const b = f()\nexpect(b).toBeTruthy()\nitems.some(b => b)\nrender(<Field b={b} />)\nif (!b) return')).toEqual([])
    expect(flagged('if (!a) {\n  a = retry()\n}')).toEqual([])
    expect(flagged("if (!a) {\n  throw new Error('missing')\n}")).toEqual([])
    expect(flagged('if (!a) return null')).toEqual([])
    expect(flagged('if (!section) return // released: nothing to check')).toEqual([])
  })
})
