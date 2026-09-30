import { describe, expect, it } from 'vitest'

import {
  RepositoryCatalogFilters,
  buildRepositoryCatalogRows,
  filterRepositoryRows,
  sortRepositoryRows,
  summarizeRepositoryRows,
} from './repositoryCatalogUtils'

const domain = { id: 'domain-1', name: 'Retail', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }
const category = { id: 'category-1', domain_id: domain.id, repository_id: 'repo-1', name: 'Checkout', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }
const project = { id: 'project-1', domain_id: domain.id, category_id: category.id, name: 'Checkout API', created_at: '2026-01-02T00:00:00Z', updated_at: '2026-01-03T00:00:00Z' }

const defaultFilters: RepositoryCatalogFilters = {
  query: '',
  projectId: 'all',
  releaseId: 'all',
  status: 'all',
  protocol: 'all',
  timeframe: 'all',
  customStartDate: '',
  customEndDate: '',
}

function buildRows(stoppedBySubject?: string) {
  return buildRepositoryCatalogRows({
    activeDomain: domain,
    categories: [category],
    projects: [project],
    releasesByProject: {
      [project.id]: [
        { id: 'release-1', application_id: 'app-1', project_id: project.id, name: '2026.05', created_at: '2026-01-04T00:00:00Z', updated_at: '2026-01-05T00:00:00Z' },
        { id: 'release-2', application_id: 'app-1', project_id: project.id, name: '2025.12', created_at: '2025-12-01T00:00:00Z', updated_at: '2025-12-02T00:00:00Z', archived_at: '2026-01-06T00:00:00Z' },
      ],
    },
    scriptsByProject: {
      [project.id]: [{
        id: 'script-1',
        name: 'Checkout Smoke',
        protocol: 'http',
        version: '1.0.0',
        project_id: project.id,
        created_at: '2026-01-07T00:00:00Z',
        scenario_count: 2,
        associated_releases: [{ id: 'release-1', application_id: 'app-1', name: '2026.05' }],
        authoring_health: { status: 'ready', score: 95, inputs: [] },
      }],
    },
    scenariosByProject: {
      [project.id]: [{
        id: 'scenario-1',
        name: 'Checkout Peak',
        duration: '30m',
        scenario_mode: 'goal_oriented',
        scripts: [{ order: 1, script_id: 'script-1', script_name: 'Checkout Smoke', protocol: 'http', vusers: 25 }],
        data_pools: [{ alias: 'users', pool_id: 'pool-1', pool_name: 'Checkout Users', access_mode: 'read_only', selection_strategy: 'sequential' }],
        created_at: '2026-01-08T00:00:00Z',
        updated_at: '2026-01-08T00:00:00Z',
        script_count: 1,
        result_count: 3,
        associated_releases: [{ id: 'release-1', application_id: 'app-1', name: '2026.05' }],
      }],
    },
    testsByProject: {
      [project.id]: [{
        id: 'test-1',
        name: 'Checkout Run 001',
        status: 'COMPLETE',
        stopped_by_subject: stoppedBySubject,
        project_id: project.id,
        release_id: 'release-1',
        scenario_id: 'scenario-1',
        groups: [{ name: 'Checkout', script_id: 'script-1', script_name: 'Checkout Smoke', protocol: 'http', vusers: 25 }],
        total_vusers: 25,
        duration: '30m',
        created_at: '2026-01-10T00:00:00Z',
        started_at: '2026-01-10T00:05:00Z',
        ended_at: '2026-01-10T00:35:00Z',
      }],
    },
    dataPoolsByProject: {
      [project.id]: [{
        id: 'pool-1',
        name: 'Checkout Users',
        project_id: project.id,
        schema: { username: 'string', password: 'string' },
        created_at: '2026-01-11T00:00:00Z',
        updated_at: '2026-01-12T00:00:00Z',
        archived_at: '2026-01-13T00:00:00Z',
      }],
    },
  })
}

describe('repositoryCatalogUtils', () => {
  it('makes operator-stopped runs searchable and filterable separately from completed runs', () => {
    const rows = buildRows('operator')
    expect(rows.find(row => row.id === 'test-1')?.status).toBe('STOPPED')
    expect(rows.find(row => row.id === 'test-1')?.searchText).toContain('stopped')
    expect(filterRepositoryRows(rows, 'test', { ...defaultFilters, status: 'COMPLETE' }).some(row => row.id === 'test-1')).toBe(false)
    expect(filterRepositoryRows(rows, 'test', { ...defaultFilters, status: 'STOPPED' }).some(row => row.id === 'test-1')).toBe(true)
  })

  it('normalizes all repository asset types and preserves archived assets', () => {
    const rows = buildRows()
    const summary = summarizeRepositoryRows(rows)

    expect(summary).toEqual({
      project: 1,
      release: 2,
      script: 1,
      scenario: 1,
      test: 1,
      data_pool: 1,
    })
    expect(rows.find(row => row.id === 'release-2')?.status).toBe('Archived')
    expect(rows.find(row => row.id === 'pool-1')?.archived).toBe(true)
    expect(rows.find(row => row.id === 'scenario-1')?.summary).toContain('1 script')
  })

  it('maps canonical release families to every application member id', () => {
    const rows = buildRepositoryCatalogRows({
      activeDomain: domain,
      activeApplication: { id: 'app-2', project_id: project.id, name: 'Billing', created_at: '', updated_at: '' },
      categories: [category],
      projects: [project],
      releasesByProject: {
        [project.id]: [{
          id: 'family-2608',
          group_id: 'family-2608',
          application_id: 'app-1',
          application_release_id: 'release-app-1',
          member_ids: { 'app-1': 'release-app-1', 'app-2': 'release-app-2' },
          project_id: project.id,
          name: '2608',
          created_at: '2026-01-04T00:00:00Z',
          updated_at: '2026-01-05T00:00:00Z',
        }],
      },
      scriptsByProject: {},
      scenariosByProject: {},
      testsByProject: {
        [project.id]: [{
          id: 'test-app-2',
          name: 'Billing Run',
          status: 'COMPLETE',
          project_id: project.id,
          release_id: 'release-app-2',
          groups: [],
          total_vusers: 1,
          duration: '1m',
          created_at: '2026-01-06T00:00:00Z',
        }],
      },
      dataPoolsByProject: {},
    })

    expect(rows.find(row => row.kind === 'release')?.id).toBe('family-2608')
    expect(rows.find(row => row.id === 'test-app-2')?.releaseName).toBe('2608')
  })

  it('scopes application-linked rows to the selected application', () => {
    const rows = buildRepositoryCatalogRows({
      activeDomain: domain,
      activeApplication: { id: 'app-1', project_id: project.id, name: 'ACC', created_at: '', updated_at: '' },
      categories: [category],
      projects: [project],
      releasesByProject: {
        [project.id]: [
          { id: 'release-1', application_id: 'app-1', project_id: project.id, name: '2026.05', created_at: '2026-01-04T00:00:00Z', updated_at: '2026-01-05T00:00:00Z' },
          { id: 'release-foreign', application_id: 'app-2', project_id: project.id, name: 'Foreign Release', created_at: '2026-01-04T00:00:00Z', updated_at: '2026-01-05T00:00:00Z' },
        ],
      },
      scriptsByProject: {
        [project.id]: [
          {
            id: 'script-1',
            name: 'Checkout Smoke',
            protocol: 'http',
            version: '1.0.0',
            project_id: project.id,
            created_at: '2026-01-07T00:00:00Z',
            associated_releases: [{ id: 'release-1', application_id: 'app-1', name: '2026.05' }],
          },
          {
            id: 'script-foreign',
            name: 'Billing Smoke',
            protocol: 'http',
            version: '1.0.0',
            project_id: project.id,
            created_at: '2026-01-07T00:00:00Z',
            associated_releases: [{ id: 'release-foreign', application_id: 'app-2', name: 'Foreign Release' }],
          },
        ],
      },
      scenariosByProject: {
        [project.id]: [
          {
            id: 'scenario-1',
            name: 'Checkout Peak',
            duration: '30m',
            scripts: [{ order: 1, script_id: 'script-1', script_name: 'Checkout Smoke', protocol: 'http', vusers: 25 }],
            created_at: '2026-01-08T00:00:00Z',
            updated_at: '2026-01-09T00:00:00Z',
            script_count: 1,
            associated_releases: [{ id: 'release-1', application_id: 'app-1', name: '2026.05' }],
          },
          {
            id: 'scenario-foreign',
            name: 'Billing Peak',
            duration: '30m',
            scripts: [{ order: 1, script_id: 'script-foreign', script_name: 'Billing Smoke', protocol: 'http', vusers: 25 }],
            created_at: '2026-01-08T00:00:00Z',
            updated_at: '2026-01-09T00:00:00Z',
            script_count: 1,
            associated_releases: [{ id: 'release-foreign', application_id: 'app-2', name: 'Foreign Release' }],
          },
        ],
      },
      testsByProject: {
        [project.id]: [
          {
            id: 'test-1',
            name: 'Checkout Run 001',
            status: 'COMPLETE',
            application_id: 'app-1',
            project_id: project.id,
            release_id: 'release-1',
            scenario_id: 'scenario-1',
            groups: [{ name: 'Checkout', script_id: 'script-1', script_name: 'Checkout Smoke', protocol: 'http', vusers: 25 }],
            total_vusers: 25,
            duration: '30m',
            created_at: '2026-01-10T00:00:00Z',
          },
          {
            id: 'test-foreign',
            name: 'Billing Run 001',
            status: 'COMPLETE',
            application_id: 'app-2',
            project_id: project.id,
            release_id: 'release-foreign',
            scenario_id: 'scenario-foreign',
            groups: [{ name: 'Billing', script_id: 'script-foreign', script_name: 'Billing Smoke', protocol: 'http', vusers: 25 }],
            total_vusers: 25,
            duration: '30m',
            created_at: '2026-01-10T00:00:00Z',
          },
        ],
      },
      dataPoolsByProject: {},
    })

    const ids = rows.map(row => row.id)
    expect(ids).toEqual(expect.arrayContaining(['release-1', 'script-1', 'scenario-1', 'test-1']))
    expect(ids).not.toContain('release-foreign')
    expect(ids).not.toContain('script-foreign')
    expect(ids).not.toContain('scenario-foreign')
    expect(ids).not.toContain('test-foreign')
  })

  it('searches and filters mixed rows by tab, status, protocol, and project', () => {
    const rows = buildRows()

    expect(filterRepositoryRows(rows, 'script', { ...defaultFilters, query: 'smoke' }).map(row => row.id)).toEqual(['script-1'])
    expect(filterRepositoryRows(rows, 'all', { ...defaultFilters, protocol: 'http' }).map(row => row.id)).toEqual(['script-1', 'scenario-1', 'test-1'])
    expect(filterRepositoryRows(rows, 'all', { ...defaultFilters, status: 'Archived' }).map(row => row.id)).toEqual(['release-2', 'pool-1'])
    expect(filterRepositoryRows(rows, 'all', { ...defaultFilters, projectId: 'missing-project' })).toEqual([])
  })

  it('filters rows by linked release and custom activity window', () => {
    const rows = buildRows()

    expect(filterRepositoryRows(rows, 'all', { ...defaultFilters, releaseId: 'release-1' }).map(row => row.id)).toEqual([
      'release-1',
      'script-1',
      'scenario-1',
      'test-1',
    ])
    expect(filterRepositoryRows(rows, 'all', {
      ...defaultFilters,
      timeframe: 'custom',
      customStartDate: '2026-01-09',
      customEndDate: '2026-01-10',
    }).map(row => row.id)).toEqual(['test-1'])
  })

  it('sorts rows by name and last activity', () => {
    const rows = buildRows()

    expect(sortRepositoryRows(rows, { key: 'name', direction: 'asc' })[0].name).toBe('2025.12')
    expect(sortRepositoryRows(rows, { key: 'last_activity', direction: 'desc' })[0].id).toBe('pool-1')
  })
})

it('shows assigned scripts and scenarios before their first run and labels history separately', () => {
  const assignment = { application: { id: 'app-1', name: 'Checkout App' }, releases: [{ id: 'release-1', name: 'Next Release', application_id: 'app-1' }], revision: 1 }
  const rows = buildRepositoryCatalogRows({
    activeDomain: domain, activeApplication: { id: 'app-1', name: 'Checkout App', project_id: project.id, created_at: '', updated_at: '' },
    categories: [category], projects: [project], releasesByProject: {}, testsByProject: {}, dataPoolsByProject: {},
    scriptsByProject: { [project.id]: [{ id: 'new-script', name: 'Never run', protocol: 'http', version: '1', created_at: '', library_assignment: assignment }] },
    scenariosByProject: { [project.id]: [{ id: 'new-scenario', name: 'Never run scenario', duration: '1m', scripts: [], script_count: 0, created_at: '', updated_at: '', library_assignment: { ...assignment, releases: [] } }] },
  })
  const script = rows.find(row => row.id === 'new-script')!
  const scenario = rows.find(row => row.id === 'new-scenario')!
  expect(script.releaseIds).toContain('release-1')
  expect(script.detail).toContain('Assigned releases: Next Release')
  expect(script.detail).toContain('Run releases: None')
  expect(scenario.detail).toContain('Assignment: Checkout App')
})
