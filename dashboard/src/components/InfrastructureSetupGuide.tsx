import { useEffect, useState } from 'react'
import { useComputeApi } from '../hooks/api/useComputeApi'
import { useClustersApi } from '../hooks/api/useClustersApi'
import { useFleetApi } from '../hooks/api/useFleetApi'
import { listAllFleetPools } from './fleetCatalogModel'
import { settingsHref, workspaceHref } from './settingsNavigation'

export function InfrastructureSetupGuide() {
  const compute = useComputeApi(), clusters = useClustersApi(), fleet = useFleetApi()
  const [mode, setMode] = useState(() => {
    try { const saved = sessionStorage.getItem('apexload.infrastructureSetup.environment'); return saved && ['kubernetes', 'cloud', 'static'].includes(saved) ? saved : 'kubernetes' }
    catch { return 'kubernetes' }
  })
  const [counts, setCounts] = useState<{ cloud: number; targets: number; clusters: number; pools: number } | null>(null)
  const [error, setError] = useState('')
  const [refresh, setRefresh] = useState(0)
  useEffect(() => {
    let active = true
    setCounts(null); setError('')
    void Promise.all([compute.listCloudConnections(), compute.listExecutionTargets(), clusters.listClusters(), listAllFleetPools(fleet.listFleetPools)]).then(([connections, targets, clusterList, pools]) => {
      if (active) setCounts({ cloud: connections.cloud_connections.length, targets: targets.execution_targets.length, clusters: clusterList.clusters.length, pools: pools.length })
    }).catch(() => { if (active) setError('Infrastructure inventory could not be loaded. Check access and retry.') })
    return () => { active = false }
  }, [compute, clusters, fleet, refresh])
  const steps = [
    ...(mode === 'cloud' ? [{ id: 'cloud', title: 'Connect the cloud account', detail: 'Save credentials and test the connection.', count: counts?.cloud }, { id: 'cloud', title: 'Create an execution target', detail: 'Select the connected account, region, runtime and network; test the target.', count: counts?.targets }]
      : mode === 'kubernetes' ? [{ id: 'clusters', title: 'Connect the Kubernetes cluster', detail: 'Register the cluster and test namespace access.', count: counts?.clusters }] : []),
    { id: 'pools', title: 'Create and assign a generator pool', detail: mode === 'static' ? 'Assign registered static generators and their capacity.' : 'Bind the pool to the selected cluster or execution target, then configure its lifecycle.', count: counts?.pools },
    ...(mode === 'kubernetes' ? [{ id: 'allocation', title: 'Allocate host resources', detail: 'Choose the pool and review CPU, memory, node placement and quota headroom.', count: undefined }] : []),
    { id: 'capacity', title: 'Review admission capacity', detail: 'Choose configured or live capacity and verify the planning profile.', count: undefined },
  ]
  return <details className="settings-subsection"><summary>Infrastructure setup guide</summary>
    <label>Execution environment<select value={mode} onChange={e => {
      setMode(e.target.value)
      try { sessionStorage.setItem('apexload.infrastructureSetup.environment', e.target.value) } catch { /* Browser storage may be unavailable. */ }
    }}><option value="kubernetes">Kubernetes</option><option value="cloud">Cloud execution target</option><option value="static">Static generators</option></select></label>
    <p>Follow these steps in order. Inventory counts show saved resources across the deployment; they do not establish that the selected execution path is ready.</p>
    {error && <p role="alert">{error}</p>}<button type="button" className="btn btn-secondary" onClick={() => setRefresh(value => value + 1)}>Refresh setup inventory</button>
    <ol>{steps.map((step, index) => <li key={index}><a href={settingsHref(step.id)}>{step.title}</a> — {step.detail}{step.count !== undefined && <span> ({step.count} configured)</span>}</li>)}<li><a href={workspaceHref('fleet')}>Verify generators in Fleet</a> and run a small validation scenario before reserving capacity.</li></ol>
  </details>
}
