import { useEffect, useMemo, useState } from 'react'

import RepositorySectionTabs from './RepositorySectionTabs'
import { useApi } from '../hooks/useApi'
import { useTopbarContribution } from '../hooks/TopbarContributionProvider'
import type { SharedDataPool } from '../types/dataPools'
import { getErrorMessage } from '../utils/errorUtils'

/**
 * Repository › Shared test data for roles that cannot manage data pools.
 * They can bind pools to scenarios, so they see what exists instead of an
 * "Administrator settings" page with their personal preferences.
 */
export default function SharedTestDataReadOnly({ projectId, projectName }: { projectId?: string; projectName?: string }) {
  const api = useApi()
  const [state, setState] = useState<{ projectId?: string; pools: SharedDataPool[]; error: string; loading: boolean }>({ pools: [], error: '', loading: true })
  const contribution = useMemo(() => ({
    id: 'shared-test-data-read-only',
    title: 'Test Data',
    controls: <RepositorySectionTabs current="test-data" />,
    priority: 10,
  }), [])
  useTopbarContribution(contribution)

  // Archived pools cannot be bound to scenarios, so they are not listed.
  const pools = state.pools.filter(pool => !pool.archived_at)

  useEffect(() => {
    let cancelled = false
    setState({ projectId, pools: [], error: '', loading: true })
    if (!projectId) {
      setState({ projectId, pools: [], error: '', loading: false })
      return () => { cancelled = true }
    }
    api.listDataPools(projectId)
      .then(response => { if (!cancelled) setState({ projectId, pools: response.data_pools || [], error: '', loading: false }) })
      .catch((error: unknown) => { if (!cancelled) setState({ projectId, pools: [], error: getErrorMessage(error, 'Unable to load shared test data'), loading: false }) })
    return () => { cancelled = true }
  }, [api, projectId])

  return <div className="page settings-page shared-test-data-read-only">
    <section className="settings-subsection" role="note">
      <h2>Shared test data{projectName ? ` · ${projectName}` : ''}</h2>
      <p>You can use these data pools in scenarios. Creating, editing and importing pools requires the project admin role.</p>
    </section>
    {state.loading ? <p role="status">Loading shared test data…</p>
      : state.error ? <p className="settings-error-inline" role="alert">{state.error}</p>
      : !projectId ? <p>Select a project to see its shared test data.</p>
      : pools.length === 0 ? <p>No shared data pools exist in this project yet.</p>
      : <div className="data-table"><table aria-label="Shared data pools"><thead><tr><th>Pool</th><th>Columns</th><th>Description</th></tr></thead><tbody>
        {pools.map(pool => <tr key={pool.id}><td>{pool.name}</td><td>{Object.keys(pool.schema || {}).join(', ') || '—'}</td><td>{pool.description || '—'}</td></tr>)}
      </tbody></table></div>}
  </div>
}
