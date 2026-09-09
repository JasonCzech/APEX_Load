import { describe, it, expect } from 'vitest'
import { orchestrationReturn } from './orchestrationReturn'
const search = (url: string, run = 'run-1') => new URLSearchParams({ orchestrationReturn: url, orchestrationRun: run }).toString()
describe('Orchestration handoff', () => {
  it('preserves exact analysis and connection context', () => {
    expect(orchestrationReturn(search('https://orchestration.example/runs/thread-1'), 'run-1')?.pathname).toBe('/runs/thread-1')
    expect(orchestrationReturn(search('http://127.0.0.1:3000/apex-load?connection=c1&run=run-1'), 'run-1')?.search).toBe('?connection=c1&run=run-1')
  })
  it('does not carry the return action onto another execution', () => {
    expect(orchestrationReturn(search('https://orchestration.example/runs/thread-1'), 'other')).toBeNull()
  })
  it.each(['javascript:alert(1)', 'https://user:password@example.com/runs/a', 'https://example.com/admin', 'http://example.com/runs/a', 'https://example.com/runs/a?token=secret', 'https://example.com/runs/a#other'])('rejects unsafe or unrelated destination %s', url => {
    expect(orchestrationReturn(search(url), 'run-1')).toBeNull()
  })
})
