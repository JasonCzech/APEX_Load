import { describe, expect, it } from 'vitest'

// Recharts animates every series by default: each data update replays the
// entry animation, so a live-updating dashboard chart re-draws from zero on
// every tick and a printed/exported report can be captured mid-animation.
// The dashboard therefore disables animation on every chart series. This guard
// keeps that convention whole -- a new chart that forgets the prop, or that
// passes anything other than a literal `false`, is caught here instead of
// shipping a jittering series.
const SERIES_PRIMITIVES = ['Area', 'Line', 'Bar', 'Pie', 'Scatter', 'Radar', 'RadialBar', 'Funnel'] as const

/**
 * Returns the full opening tag text for every `<Tag ...>` / `<Tag ... />` in
 * `source`, tracking `{}` nesting so a `>` inside an attribute expression
 * (arrow functions, comparisons) does not terminate the tag early.
 */
function openingTags(source: string, tag: string): string[] {
  const found: string[] = []
  const pattern = new RegExp(`<${tag}(?=[\\s/>])`, 'g')
  let match: RegExpExecArray | null
  while ((match = pattern.exec(source)) !== null) {
    let depth = 0
    let index = match.index + tag.length + 1
    for (; index < source.length; index += 1) {
      const character = source[index]
      if (character === '{') depth += 1
      else if (character === '}') depth -= 1
      else if (character === '>' && depth === 0) break
    }
    found.push(source.slice(match.index, index + 1))
  }
  return found
}

async function collectSourceFiles(root: URL): Promise<URL[]> {
  // @ts-expect-error node:fs is available in the Vitest runtime.
  const { readdirSync } = await import('node:fs')
  const files: URL[] = []
  const walk = (directory: URL) => {
    for (const entry of readdirSync(directory, { withFileTypes: true }) as Array<{ name: string; isDirectory(): boolean }>) {
      if (entry.isDirectory()) {
        walk(new URL(`${entry.name}/`, directory))
        continue
      }
      if (!entry.name.endsWith('.tsx')) continue
      if (/\.test\.tsx$/.test(entry.name)) continue
      files.push(new URL(entry.name, directory))
    }
  }
  walk(root)
  return files
}

describe('dashboard chart animation convention', () => {
  it('disables recharts series animation in every chart component', async () => {
    // @ts-expect-error node:fs is available in the Vitest runtime.
    const { readFileSync } = await import('node:fs')
    const files = await collectSourceFiles(new URL('../', import.meta.url))
    const offenders: string[] = []
    let inspectedSeries = 0

    for (const file of files) {
      const source = readFileSync(file, 'utf8') as string
      // `[^}]` keeps the capture inside a single import statement: a greedy or
      // lazy `[\s\S]*?` would start at the file's first `import {` and swallow
      // every earlier import, which silently loses the recharts names.
      const rechartsImports = [...source.matchAll(/import\s+(?:type\s+)?\{([^}]*)\}\s*from\s*['"]recharts['"]/g)]
      if (rechartsImports.length === 0) continue
      const imported = new Set(
        rechartsImports
          .flatMap(match => match[1].split(','))
          .map(name => name.trim().replace(/^type\s+/, '').split(/\s+as\s+/)[0].trim())
          .filter(Boolean),
      )
      for (const primitive of SERIES_PRIMITIVES) {
        if (!imported.has(primitive)) continue
        for (const tag of openingTags(source, primitive)) {
          inspectedSeries += 1
          // The value is what matters, not the mention: in JSX a bare
          // `isAnimationActive` means `true`, and `={true}` / `={someFlag}`
          // both still contain the prop name. Only a literal `{false}` turns
          // the animation off, so that is what has to be matched.
          if (/isAnimationActive\s*=\s*\{\s*false\s*\}/.test(tag)) continue
          const relative = file.pathname.slice(file.pathname.indexOf('/src/') + 1)
          const reason = /\bisAnimationActive\b/.test(tag)
            ? 'with isAnimationActive not set to {false}'
            : 'without isAnimationActive'
          offenders.push(`${relative}: <${primitive}> ${reason}`)
        }
      }
    }

    // Guards the scanner itself: if the walk or the import parsing silently
    // stops matching, the offender list would be trivially empty.
    expect(inspectedSeries).toBeGreaterThan(30)
    expect(offenders).toEqual([])
  })
})
