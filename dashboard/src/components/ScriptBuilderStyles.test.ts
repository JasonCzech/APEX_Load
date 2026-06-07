import { describe, expect, it } from 'vitest'

async function readStylesheet(): Promise<string> {
  // @ts-expect-error node:fs is available in the Vitest runtime.
  const { readFileSync } = await import('node:fs')
  return readFileSync(new URL('./Pages.css', import.meta.url), 'utf8')
}

function ruleBody(stylesheet: string, pattern: RegExp): string {
  return stylesheet.match(pattern)?.[1] || ''
}

describe('Script Builder styles', () => {
  it('keeps the workflow cards solid while using theme tokens for selected state', async () => {
    const stylesheet = await readStylesheet()
    const workflowCard = ruleBody(stylesheet, /\.script-workflow-card \{([\s\S]*?)\n\}/)
    const workflowIndexBadge = ruleBody(stylesheet, /\.script-workflow-card span \{([\s\S]*?)\n\}/)

    expect(workflowCard).toContain('background: var(--bg-card);')
    expect(stylesheet).toContain('.script-workflow-card.active {\n  background: var(--accent-soft);\n}')
    expect(workflowIndexBadge).toContain('background: var(--accent-soft);')
  })

  it('uses one theme-aware gradient treatment for every source-driven import option', async () => {
    const stylesheet = await readStylesheet()
    const globalPrimaryIndex = stylesheet.indexOf('.script-create-option.primary {')
    const importOptionIndex = stylesheet.indexOf('.script-create-lane.import .script-create-option,')
    const importOption = ruleBody(
      stylesheet,
      /\.script-create-lane\.import \.script-create-option,\n\.script-create-lane\.import \.script-create-option\.primary \{([\s\S]*?)\n\}/,
    )

    expect(importOptionIndex).toBeGreaterThan(globalPrimaryIndex)
    expect(importOption).toContain('border-color: var(--accent-border);')
    expect(importOption).toContain('linear-gradient(180deg, var(--accent-soft) 0%')
    expect(importOption).toContain('rgba(var(--accent-rgb), 0.04)')
    expect(importOption).toContain('var(--bg-card);')
  })
})
