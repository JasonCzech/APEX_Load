import './Access.css'
import { useEffect, useState } from 'react'
import { useApi } from '../hooks/useApi'
import type { Domain, Project, DashboardAuditEventFilter, DashboardUserActivityResponse } from '../types'
import { downloadBlob } from '../utils/fileUtils'
import { getErrorMessage } from '../utils/errorUtils'
import { csvBlob, activityDateError } from './accessReporting'
import { formatDurationSeconds, formatNumber, formatTimestamp } from './accessUtils'

type Filter = { project: string; domain: string; actor: string; after: string; before: string }
/** The parent keys this report by workspace so a scope change discards pending results and filters. */
export default function AccessUsagePanel({
  projects,
  domains,
  activeProject,
  activeDomain,
}: {
  projects: Project[]
  domains: Domain[]
  activeProject?: Project | null
  activeDomain?: Domain | null
}) {
  const api = useApi()
  const initial = {
    project: activeProject?.id || '',
    domain: activeDomain?.id || '',
    actor: '',
    after: '',
    before: '',
  }
  const [draft, setDraft] = useState<Filter>(initial)
  const [filter, setFilter] = useState<Filter>(initial)
  const [page, setPage] = useState(0)
  const [data, setData] = useState<DashboardUserActivityResponse | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [revision, setRevision] = useState(0)
  const [exporting, setExporting] = useState(false)
  const [sort, setSort] = useState<'user' | 'runs' | 'vusers' | 'duration' | 'seconds'>('seconds')
  const [ascending, setAscending] = useState(false)
  const validation = activityDateError(draft.after, draft.before)
  useEffect(() => {
    let cancelled = false
    setData(null)
    setError('')
    const query: DashboardAuditEventFilter = {
      report: 'usage',
      project_id: filter.project || undefined,
      domain_id: filter.domain || undefined,
      actor_subject: filter.actor.trim() || undefined,
      created_after: filter.after ? new Date(filter.after).toISOString() : undefined,
      created_before: filter.before ? new Date(filter.before).toISOString() : undefined,
      limit: 25,
      offset: page * 25,
    }
    void api
      .getAuthUserActivity(query)
      .then((result) => {
        if (!cancelled) setData(result)
      })
      .catch((error) => {
        if (!cancelled) setError(getErrorMessage(error, 'Unable to load usage'))
      })
    return () => {
      cancelled = true
    }
  }, [api, filter, page, revision])
  const rows = [...(data?.usage.by_user || [])].sort((a, b) => {
    const values = {
      user: (a.key || '').localeCompare(b.key || ''),
      runs: a.run_count - b.run_count,
      vusers: a.total_vusers - b.total_vusers,
      duration: a.duration_seconds - b.duration_seconds,
      seconds: a.vuser_seconds - b.vuser_seconds,
    }
    return values[sort] * (ascending ? 1 : -1)
  })
  const exportUsage = async () => {
    if (!data || exporting) return
    setExporting(true)
    setNotice('')
    try {
      const outcome = await downloadBlob(
        csvBlob([
          [
            'user',
            'runs',
            'total_vusers',
            'duration_seconds',
            'vuser_seconds',
            'project',
            'domain',
            'run_created_after',
            'run_created_before',
            'generated_at',
          ],
          ...rows.map((row) => [
            row.key,
            row.run_count,
            row.total_vusers,
            row.duration_seconds,
            row.vuser_seconds,
            filter.project,
            filter.domain,
            filter.after ? new Date(filter.after).toISOString() : '',
            filter.before ? new Date(filter.before).toISOString() : '',
            data.usage.generated_at,
          ]),
        ]),
        'apexload-usage-by-user.csv',
      )
      if (outcome !== 'cancelled') setNotice('Usage export downloaded.')
    } catch (error) {
      setNotice(getErrorMessage(error, 'Unable to export usage'))
    } finally {
      setExporting(false)
    }
  }
  const apply = (next: Filter) => {
    setFilter(next)
    setPage(0)
    setRevision((value) => value + 1)
  }
  return (
    <section aria-label="Usage report">
      <p>
        Usage covers retained runs, selected by <strong>run creation time</strong> (local dates). Duration is
        active elapsed time; VUser seconds is configured VUsers × active duration, not billed usage. A run is
        attributed to its starter, then creator; otherwise it is unattributed.
      </p>
      <div className="access-table-toolbar">
        <label>
          <span>Usage domain</span>
          <select
            value={draft.domain}
            onChange={(event) => setDraft({ ...draft, domain: event.target.value, project: '' })}
          >
            <option value="">All authorized domains</option>
            {domains.map((domain) => (
              <option key={domain.id} value={domain.id}>
                {domain.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          <span>Usage project</span>
          <select
            value={draft.project}
            onChange={(event) => setDraft({ ...draft, project: event.target.value })}
          >
            <option value="">All authorized projects</option>
            {projects
              .filter((project) => !draft.domain || project.domain_id === draft.domain)
              .map((project) => (
                <option key={project.id} value={project.id}>
                  {project.name}
                </option>
              ))}
          </select>
        </label>
        <label>
          <span>Usage actor subject</span>
          <input
            value={draft.actor}
            onChange={(event) => setDraft({ ...draft, actor: event.target.value })}
            placeholder="Subject or unattributed"
          />
        </label>
        <label>
          <span>Run created after (local time)</span>
          <input
            type="datetime-local"
            value={draft.after}
            onChange={(event) => setDraft({ ...draft, after: event.target.value })}
          />
        </label>
        <label>
          <span>Run created before (local time)</span>
          <input
            type="datetime-local"
            value={draft.before}
            onChange={(event) => setDraft({ ...draft, before: event.target.value })}
          />
        </label>
        <button className="btn btn-primary" disabled={!!validation} onClick={() => apply(draft)}>
          Apply usage filters
        </button>
      </div>
      {validation && <p role="alert">{validation}</p>}
      <p>
        Applied scope:{' '}
        {domains.find((domain) => domain.id === filter.domain)?.name || 'All authorized domains'} /{' '}
        {projects.find((project) => project.id === filter.project)?.name || 'All authorized projects'} ·
        Actor: {filter.actor || 'All'} · Created: {filter.after || 'Any start'} to{' '}
        {filter.before || 'Any end'}.
      </p>
      {error ? (
        <p role="alert">
          {error} <button onClick={() => setRevision((value) => value + 1)}>Retry usage</button>
        </p>
      ) : !data ? (
        <p role="status">Loading usage…</p>
      ) : (
        <>
          <div className="access-panel-header">
            <p>
              {data.usage.totals.run_count} matching runs · As of {formatTimestamp(data.usage.generated_at)}
            </p>
            <button className="btn btn-secondary" disabled={exporting} onClick={() => void exportUsage()}>
              Export usage CSV
            </button>
          </div>
          <div className="access-table-wrap">
            <table className="access-data-table access-usage-table">
              <caption className="sr-only">Usage by user</caption>
              <thead>
                <tr>
                  {(
                    [
                      ['user', 'User'],
                      ['runs', 'Runs'],
                      ['vusers', 'Total VUsers'],
                      ['duration', 'Duration'],
                      ['seconds', 'VUser seconds'],
                    ] as const
                  ).map(([key, label]) => (
                    <th
                      key={key}
                      aria-sort={sort === key ? (ascending ? 'ascending' : 'descending') : 'none'}
                    >
                      <button
                        onClick={() => {
                          setSort(key)
                          setAscending(sort === key ? !ascending : key === 'user')
                        }}
                      >
                        {label}
                      </button>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => (
                  <tr key={row.key || 'unattributed'}>
                    <td>
                      <button
                        className="access-row-primary"
                        aria-label={`Show runs for ${row.key || 'unattributed'}`}
                        onClick={() => {
                          const next = { ...filter, actor: row.key || 'unattributed' }
                          setDraft(next)
                          apply(next)
                        }}
                      >
                        {row.key || 'unattributed'}
                      </button>
                    </td>
                    <td>{formatNumber(row.run_count)}</td>
                    <td>{formatNumber(row.total_vusers)}</td>
                    <td>{formatDurationSeconds(row.duration_seconds)}</td>
                    <td>{formatNumber(row.vuser_seconds)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {!rows.length && (
            <p role="status">No runs match these filters. Clear the actor or broaden the creation dates.</p>
          )}
          <h4>Matching runs</h4>
          <div className="access-table-wrap" data-table-pagination="off">
            <table className="access-data-table">
              <thead>
                <tr>
                  <th>Run</th>
                  <th>Actor</th>
                  <th>Created</th>
                  <th>Status</th>
                </tr>
              </thead>
              <tbody>
                {(data.usage_runs || []).map((run) => (
                  <tr key={run.id}>
                    <td>
                      <a
                        href={`#/test/${encodeURIComponent(run.id)}?projectId=${encodeURIComponent(run.project_id)}`}
                      >
                        {run.name}
                      </a>
                    </td>
                    <td>{run.actor}</td>
                    <td>{formatTimestamp(run.created_at)}</td>
                    <td>{run.status}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <nav aria-label="Usage run pagination" className="access-pagination">
            <button className="btn btn-secondary" disabled={page === 0} onClick={() => setPage(page - 1)}>
              Previous runs
            </button>
            <span>Page {page + 1}</span>
            <button className="btn btn-secondary" disabled={!data.has_more} onClick={() => setPage(page + 1)}>
              Next runs
            </button>
          </nav>
        </>
      )}
      {notice && <p role="status">{notice}</p>}
    </section>
  )
}
