import { renderToStaticMarkup } from 'react-dom/server'
import { describe, it, expect } from 'vitest'
import type { RunArchiveReport } from '../../types'
import RunArchiveCoverageNotice from './RunArchiveCoverageNotice'

function report(message?: string, sources?: Array<{ target: string; collected: boolean; generator_id?: string; region?: string; groups?: string[] }>): RunArchiveReport {
  return {
    completeness: { score_pct: message ? 70 : 100, status: message ? 'incomplete' : 'complete', findings: message ? [{ dimension: 'generator_sources', severity: 'critical', penalty: 30, message }] : [] },
    manifest: { source_coverage: sources ? { mode: 'best_effort', sources } : undefined },
  } as RunArchiveReport
}

describe('RunArchiveCoverageNotice', () => {
  it('shows the server finding and inspectable source inventory without interpreting missing data as collected', () => {
    const html = renderToStaticMarkup(<RunArchiveCoverageNotice report={report('1 of 2 known generator archive sources were not collected.', [
      { target: 'healthy', collected: true }, { target: '<missing>', collected: false },
    ])} />)
    expect(html).toContain('Archive evidence needs review')
    expect(html).toContain('1 of 2 known')
    expect(html).toContain('<td>healthy</td><td>Not recorded</td><td>Not recorded</td><td>Collected</td>')
    expect(html).toContain('<td>&lt;missing&gt;</td><td>Not recorded</td><td>Not recorded</td><td>Not collected</td>')
    expect(html).toContain('does not establish complete shard history')
  })
  it('names each generator, its region and the groups it ran', () => {
    const html = renderToStaticMarkup(<RunArchiveCoverageNotice report={report('1 of 2 known generator archive sources were not collected: gen-b (us-west; group Store_Cart).', [
      { target: '127.0.0.1:19091', collected: true, generator_id: 'gen-a', region: 'us-east', groups: ['Store_Browse'] },
      { target: '127.0.0.1:19092', collected: false, generator_id: 'gen-b', region: 'us-west', groups: ['Store_Cart'] },
    ])} />)
    expect(html).toContain('<td>gen-b<br/><small>127.0.0.1:19092</small></td><td>us-west</td><td>Store_Cart</td><td>Not collected</td>')
  })
  it.each([null, report()])('does not invent a source gap when no finding exists', value => {
    expect(renderToStaticMarkup(<RunArchiveCoverageNotice report={value} />)).toBe('')
  })
  it.each(['Archive collection was marked incomplete.', 'Generator archive source coverage is invalid.', 'All known sources were collected on a best-effort basis.'])('preserves uncertainty without requiring a source inventory: %s', message => {
    const html = renderToStaticMarkup(<RunArchiveCoverageNotice report={report(message)} />)
    expect(html).toContain(message)
    expect(html).not.toContain('Inspect generator sources')
  })
})
