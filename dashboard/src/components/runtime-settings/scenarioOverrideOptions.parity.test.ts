import { describe, expect, it } from 'vitest'
import catalogSource from '../../../../pkg/dsl/config_catalog.go?raw'
import validateSource from '../../../../pkg/dsl/validate.go?raw'
import {
  LOG_MAX_RESPONSE_BYTES,
  MAX_ARRIVAL_BURSTINESS,
  RETRY_ON_OPTIONS,
  SCENARIO_OVERRIDE_SELECT_OPTIONS,
} from './scenarioOverrideOptions'

// The scenario/group override editor's select values must be the ones the
// backend accepts: the Go catalog's Options (guarded against the validators by
// TestConfigCatalogOptionsMatchValidators) and, for retry_on (a string list
// the catalog leaves open), the validator message itself.

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function catalogOptions(path: string): string[] | undefined {
  const start = catalogSource.search(new RegExp(`Path:\\s*"${escapeRegExp(path)}"`))
  if (start < 0) return undefined
  const end = catalogSource.indexOf('\n\t},', start)
  const block = catalogSource.slice(start, end)
  const match = block.match(/Options:\s*\[\]string\{([^}]*)\}/)
  if (!match) return undefined
  return match[1].split(',').map(item => item.trim()).filter(Boolean).map(item => JSON.parse(item) as string)
}

// Accepted spellings the editor does not offer: the legacy alias for gaussian,
// and the empty "default" the editor writes as Inherit.
const NOT_OFFERED: Record<string, string[]> = {
  'config.think_time.type': ['normal'],
  'config.run_logic.termination_mode': [''],
}

describe('scenario override options parity with pkg/dsl', () => {
  it('offers exactly the catalog options for every select', () => {
    const problems: string[] = []
    for (const [path, options] of Object.entries(SCENARIO_OVERRIDE_SELECT_OPTIONS)) {
      if (path === 'config.error_handling.retry_on') continue
      const accepted = catalogOptions(path)
      if (!accepted) { // reported through problems, so the expect below fails
        problems.push(`${path}: no catalog Options`)
        continue
      }
      const offered = options.map(option => option.value)
      const rejected = offered.filter(value => !accepted.includes(value))
      const missing = accepted.filter(value => !offered.includes(value) && !(NOT_OFFERED[path] ?? []).includes(value))
      if (rejected.length) problems.push(`${path}: offers ${rejected.join(', ')} the catalog rejects`)
      if (missing.length) problems.push(`${path}: misses ${missing.join(', ')}`)
      if (offered.includes('')) problems.push(`${path}: offers a blank option (Inherit is the editor's own)`)
    }
    expect(problems).toEqual([])
  })

  it('offers exactly the retry_on conditions validation accepts', () => {
    const match = validateSource.match(/retry_on\[%d\] must be ([^"]+)"/)
    expect(match).toBeTruthy()
    const accepted = (match?.[1] ?? '').replace(/, or /, ', ').split(',').map(value => value.trim()).filter(Boolean)
    expect(RETRY_ON_OPTIONS.map(option => option.value).sort()).toEqual([...accepted].sort())
  })

  it('bounds numeric overrides as validation does', () => {
    expect(validateSource).toContain(`config.log.max_response_bytes must be between 0 and ${LOG_MAX_RESPONSE_BYTES}`)
    expect(validateSource).toMatch(new RegExp(`pacing\\.Burstiness > ${MAX_ARRIVAL_BURSTINESS}\\b`))
  })
})
