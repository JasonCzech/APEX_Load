// Full runs have an explicit inventory so accidental deletion/renaming cannot
// silently reduce coverage. Targeted runs still prohibit skips and retries.
export function validateRecordingReport(report, inventory, profiles, targeted = false) {
  if (!report.stats.expected || report.stats.skipped || report.stats.unexpected || report.stats.flaky) throw new Error('Recording suite must execute tests with no skips, failures, or retries')
  const observed = new Map()
  const visit = suites => {
    for (const suite of suites || []) {
      for (const spec of suite.specs || []) for (const test of spec.tests || []) {
        const key = `${test.projectName}\0${spec.title}`
        if (observed.has(key)) throw new Error(`Duplicate recording scenario: ${spec.title}`)
        if (test.results.length !== 1 || test.results[0].status !== 'passed') throw new Error(`Scenario did not pass exactly once: ${spec.title}`)
        observed.set(key, true)
      }
      visit(suite.suites)
    }
  }
  visit(report.suites)
  if (!observed.size) throw new Error('No recording scenarios found')
  if (!targeted) {
    const expected = new Set(profiles.flatMap(profile => inventory.map(title => `${profile}\0${title}`)))
    for (const key of expected) if (!observed.has(key)) throw new Error(`Missing recording scenario: ${key.replace('\0', ': ')}`)
    for (const key of observed.keys()) if (!expected.has(key)) throw new Error(`Scenario missing from coverage inventory: ${key.replace('\0', ': ')}`)
  }
}

export function validateRecordingSelection(args) {
  for (let index=0;index<args.length;index++) {
    const arg=args[index]
    if (['--grep','--grep-invert'].includes(arg)) {
      if (!args[++index] || args[index].startsWith('--')) throw new Error(`${arg} requires a pattern`)
    } else if (arg.startsWith('--grep=') || arg.startsWith('--grep-invert=')) {
      if (!arg.split('=').slice(1).join('=')) throw new Error('Empty recording selection pattern')
    } else if (arg.startsWith('-')) {
      throw new Error(`Unsupported recording option ${arg}; use file filters, --grep, --grep-invert and APEXLOAD_E2E_PROFILE. Retry, worker and evidence policies cannot be overridden.`)
    }
  }
}
