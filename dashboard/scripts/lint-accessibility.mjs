// The complete jsx-a11y recommended ruleset currently contains substantial
// legacy interaction and labelling debt. Report that debt on demand without
// adding hundreds of warnings to the default lint output.
process.env.APEXLOAD_A11Y_LEGACY = '1'

const { ESLint } = await import('eslint')

// Existing interaction-heavy screens still carry legacy warnings. Keep that
// debt visible while preventing any rule category from growing. Lower these
// ceilings as warnings are fixed so the gate ratchets in one direction.
const legacyWarningBudgets = Object.freeze({
  'jsx-a11y/click-events-have-key-events': 64,
  'jsx-a11y/control-has-associated-label': 41,
  'jsx-a11y/interactive-supports-focus': 0,
  'jsx-a11y/label-has-associated-control': 69,
  'jsx-a11y/no-autofocus': 6,
  'jsx-a11y/no-interactive-element-to-noninteractive-role': 1,
  'jsx-a11y/no-noninteractive-element-interactions': 36,
  'jsx-a11y/no-noninteractive-element-to-interactive-role': 2,
  'jsx-a11y/no-noninteractive-tabindex': 8,
  'jsx-a11y/no-static-element-interactions': 39,
})

const eslint = new ESLint()
const results = await eslint.lintFiles(['src/**/*.{ts,tsx}'])
const accessibilityResults = results
  .map((result) => {
    const messages = result.messages.filter((message) => message.ruleId?.startsWith('jsx-a11y/'))
    return {
      ...result,
      messages,
      errorCount: messages.filter((message) => message.severity === 2).length,
      fatalErrorCount: messages.filter((message) => message.fatal).length,
      fixableErrorCount: messages.filter((message) => message.severity === 2 && message.fix).length,
      fixableWarningCount: messages.filter((message) => message.severity === 1 && message.fix).length,
      warningCount: messages.filter((message) => message.severity === 1).length,
    }
  })
  .filter((result) => result.messages.length > 0)

const formatter = await eslint.loadFormatter('stylish')
const output = await formatter.format(accessibilityResults)
if (output) process.stdout.write(output)

const errorCount = accessibilityResults.reduce(
  (total, result) => total + result.messages.filter((message) => message.severity === 2).length,
  0,
)
const warningCount = accessibilityResults.reduce(
  (total, result) => total + result.messages.filter((message) => message.severity === 1).length,
  0,
)
const warningCountsByRule = new Map()
for (const result of accessibilityResults) {
  for (const message of result.messages) {
    if (message.severity !== 1 || !message.ruleId) continue
    warningCountsByRule.set(message.ruleId, (warningCountsByRule.get(message.ruleId) || 0) + 1)
  }
}
const warningRegressions = Array.from(warningCountsByRule.entries())
  .filter(([ruleId, count]) => count > (legacyWarningBudgets[ruleId] ?? 0))
  .sort(([left], [right]) => left.localeCompare(right))

process.stdout.write(
  `Accessibility lint: ${errorCount} blocking error${errorCount === 1 ? '' : 's'}, ` +
    `${warningCount} legacy warning${warningCount === 1 ? '' : 's'}.\n`,
)
if (warningRegressions.length > 0) {
  process.stderr.write('Accessibility warning budget exceeded:\n')
  for (const [ruleId, count] of warningRegressions) {
    process.stderr.write(`  ${ruleId}: ${count} (budget ${legacyWarningBudgets[ruleId] ?? 0})\n`)
  }
}
process.exitCode = errorCount > 0 || warningRegressions.length > 0 ? 1 : 0
