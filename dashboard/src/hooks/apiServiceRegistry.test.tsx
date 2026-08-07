// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('./apiClient', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./apiClient')>()
  return {
    ...actual,
    apiFetch: vi.fn(),
    apiFetchFromOrigin: vi.fn(),
    request: vi.fn().mockResolvedValue({}),
    requestFromOrigin: vi.fn().mockResolvedValue({}),
  }
})

import {
  describeApiServiceCollisions,
  findApiServiceCollisions,
  KNOWN_DUPLICATE_SERVICE_NAMES,
  mergeApiServiceGroups,
  reportApiServiceCollisions,
  resetApiServiceCollisionReports,
  unexpectedApiServiceCollisions,
} from './apiServiceRegistry'
import { useApplicationApi } from './api/useApplicationApi'
import { useBrowserRecorderApi } from './api/useBrowserRecorderApi'
import { useCapacityApi } from './api/useCapacityApi'
import { useCorrelationRulesetApi } from './api/useCorrelationRulesetApi'
import { useEntitlementsApi } from './api/useEntitlementsApi'
import { useExecutionPlansApi } from './api/useExecutionPlansApi'
import { useFleetApi } from './api/useFleetApi'
import { useLibraryApi } from './api/useLibraryApi'
import { useMonitoringCollectorsApi } from './api/useMonitoringCollectorsApi'
import { useObjectRepositoryApi } from './api/useObjectRepositoryApi'
import { useProjectApi } from './api/useProjectApi'
import { useRecorderApi } from './api/useRecorderApi'
import { useRepositoryApi } from './api/useRepositoryApi'
import { useScriptApi } from './api/useScriptApi'
import { useSystemApi } from './api/useSystemApi'
import { useTestApi } from './api/useTestApi'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('findApiServiceCollisions', () => {
  it('returns nothing when every group exports unique names', () => {
    expect(findApiServiceCollisions({ a: { one: 1 }, b: { two: 2 } })).toEqual([])
  })

  it('reports the shadowed key with its owners in spread order', () => {
    const collisions = findApiServiceCollisions({
      a: { shared: 1 },
      b: { other: 2 },
      c: { shared: 3 },
    })
    expect(collisions).toEqual([{ key: 'shared', groups: ['a', 'c'] }])
    expect(describeApiServiceCollisions(collisions)).toBe('shared (a -> c; "c" wins)')
  })
})

describe('mergeApiServiceGroups', () => {
  it('flattens groups in spread order', () => {
    const merged = mergeApiServiceGroups({ a: { one: 1 }, b: { two: 2 } })
    expect(merged).toEqual({ one: 1, two: 2 })
  })

  it('reports a collision instead of silently shadowing', () => {
    const onCollision = vi.fn()
    const merged = mergeApiServiceGroups(
      { a: { shared: 'first' }, b: { shared: 'second' } },
      onCollision,
    )
    expect(onCollision).toHaveBeenCalledTimes(1)
    expect(onCollision.mock.calls[0][0]).toContain('shared')
    // The last spread still wins; the guard reports it rather than changing it.
    expect(merged).toEqual({ shared: 'second' })
  })

  it('does not report when there is nothing to report', () => {
    const onCollision = vi.fn()
    mergeApiServiceGroups({ a: { one: 1 } }, onCollision)
    expect(onCollision).not.toHaveBeenCalled()
  })

  it('stays silent on the audited duplicates but still reports anything else', () => {
    const allowlisted = vi.fn()
    mergeApiServiceGroups(
      {
        applications: { archiveRelease: 1, deleteRelease: 1 },
        repository: { archiveRelease: 2, deleteRelease: 2 },
      },
      allowlisted,
    )
    expect(allowlisted).not.toHaveBeenCalled()

    const mixed = vi.fn()
    mergeApiServiceGroups(
      {
        applications: { archiveRelease: 1, listReleases: 1 },
        repository: { archiveRelease: 2, listReleases: 2 },
      },
      mixed,
    )
    expect(mixed).toHaveBeenCalledTimes(1)
    // Only the un-audited name reaches the message; the allowlisted one does not.
    expect(mixed.mock.calls[0][0]).toContain('listReleases')
    expect(mixed.mock.calls[0][0]).not.toContain('archiveRelease')
  })

  it('warns through the default reporter, once per distinct message', () => {
    resetApiServiceCollisionReports()
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      mergeApiServiceGroups({ a: { shared: 1 }, b: { shared: 2 } })
      mergeApiServiceGroups({ a: { shared: 1 }, b: { shared: 2 } })
      expect(errors).toHaveBeenCalledTimes(1)
      expect(errors.mock.calls[0][0]).toContain('duplicate API service names')

      reportApiServiceCollisions('a different collision message')
      expect(errors).toHaveBeenCalledTimes(2)
    } finally {
      errors.mockRestore()
      resetApiServiceCollisionReports()
    }
  })
})

describe('domain hook name space', () => {
  let container: HTMLDivElement
  let root: Root
  let groups: Record<string, Record<string, unknown>>

  beforeEach(async () => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    function Capture() {
      groups = {
        tests: useTestApi(),
        applications: useApplicationApi(),
        projects: useProjectApi(),
        repository: useRepositoryApi(),
        scripts: useScriptApi(),
        system: useSystemApi(),
        recorder: useRecorderApi(),
        browserRecorder: useBrowserRecorderApi(),
        library: useLibraryApi(),
        capacity: useCapacityApi(),
        fleet: useFleetApi(),
        executionPlans: useExecutionPlansApi(),
        monitoringCollectors: useMonitoringCollectorsApi(),
        objectRepositories: useObjectRepositoryApi(),
        correlationRulesets: useCorrelationRulesetApi(),
        entitlements: useEntitlementsApi(),
      }
      return null
    }
    await act(async () => {
      root.render(<Capture />)
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  // The pinned baseline lives in apiServiceRegistry.ts so the runtime reporter
  // and this suite cannot disagree about which duplicates are audited.

  it('introduces no new name exported by more than one domain hook', () => {
    const unexpected = unexpectedApiServiceCollisions(findApiServiceCollisions(groups))
    expect(describeApiServiceCollisions(unexpected)).toBe('')
    expect(unexpected).toEqual([])
  })

  // The runtime half of the guard has to be quiet on a stock app boot, or the
  // message developers see when a real collision lands is a superset of one
  // they have been ignoring since day one.
  it('logs nothing through the default reporter for the real domain hooks', () => {
    resetApiServiceCollisionReports()
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      mergeApiServiceGroups(groups)
      expect(errors).not.toHaveBeenCalled()
    } finally {
      errors.mockRestore()
      resetApiServiceCollisionReports()
    }
  })

  it('keeps the known duplicates limited to useApplicationApi and useRepositoryApi', () => {
    const collisions = findApiServiceCollisions(groups)
    expect(collisions.map(({ key }) => key).sort()).toEqual([...KNOWN_DUPLICATE_SERVICE_NAMES].sort())
    collisions.forEach(({ groups: owners }) => {
      expect(owners).toEqual(['applications', 'repository'])
    })
  })

  it('exposes every distinct domain-hook name on the flattened ApiContext value', () => {
    const merged = mergeApiServiceGroups(groups, () => {}) as Record<string, unknown>
    const distinct = new Set(Object.values(groups).flatMap(group => Object.keys(group)))
    expect(Object.keys(merged).sort()).toEqual([...distinct].sort())
  })
})
