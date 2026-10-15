import { describe, expect, it } from 'vitest'

import { defaultImportSurfaces } from './ScriptCreateOptions'

// Canonical launcher ids. MUST match the backend authoringImportSurfaces() registry
// (pinned on the Go side by TestAuthoringCapabilitiesEndpoint). If the two diverge, the
// data-driven card grid and its offline fallback would disagree, so fail loudly here.
const CANONICAL_LAUNCHERS = [
  'openapi',
  'bruno',
  'postman',
  'wsdl',
  'asyncapi',
  'raml',
  'traffic_har',
  'recorder_export',
  'sample',
  'enterprise_archive',
  'jmeter',
  'devweb',
  'k6',
  'cloudtest',
  'eventhubs_capture',
  'broker_config',
  'oracle_ebusiness_review',
]

describe('defaultImportSurfaces fallback registry', () => {
  it('matches the canonical backend launcher set', () => {
    const launchers = defaultImportSurfaces.map(surface => surface.launcher).sort()
    expect(launchers).toEqual([...CANONICAL_LAUNCHERS].sort())
  })

  it('uses each launcher id exactly once and marks every surface visible', () => {
    const launchers = defaultImportSurfaces.map(surface => surface.launcher)
    expect(new Set(launchers).size).toBe(launchers.length)
    expect(defaultImportSurfaces.every(surface => surface.visible)).toBe(true)
  })

  it('names the protocols Bruno and Postman collections carry, so a SOAP search finds them', () => {
    for (const launcher of ['bruno', 'postman']) {
      expect(defaultImportSurfaces.find(surface => surface.launcher === launcher)?.protocols).toContain('SOAP')
    }
  })

  it('makes VuGen USZ and multi-project archive import discoverable', () => {
    const archive = defaultImportSurfaces.find(surface => surface.launcher === 'enterprise_archive')
    expect(archive?.description).toContain('ZIP or USZ')
    expect(archive?.description).toContain('several projects')
  })
})
