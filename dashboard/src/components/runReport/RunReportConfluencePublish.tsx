import { useCallback, useEffect, useState } from 'react'

import { useConfluenceApi, type ConfluenceConnection, type ConfluencePublication } from '../../hooks/api/useConfluenceApi'
import { getErrorMessage } from '../../utils/errorUtils'

export default function RunReportConfluencePublish({testId,canPublish = false}:{testId:string;canPublish?:boolean}) {
  const api=useConfluenceApi()
  const [connections,setConnections]=useState<ConfluenceConnection[]>([])
  const [publications,setPublications]=useState<ConfluencePublication[]>([])
  const [selected,setSelected]=useState('')
  const [busy,setBusy]=useState(false)
  const [error,setError]=useState('')
  const [loading,setLoading]=useState(true)
  const refresh=useCallback(async () => {
    try {const result=await api.getConfluencePublishStatus(testId);setConnections(result.connections||[]);setPublications(result.publications||[]);setSelected(current => current || result.connections?.[0]?.id || '')}
    catch(cause){setError(getErrorMessage(cause,'Failed to load Confluence publishing'))}
    finally{setLoading(false)}
  },[api,testId])
  useEffect(() => {void refresh()},[refresh])
  if (loading) return <p role="status">Loading Confluence publishing…</p>
  return <section className="run-report-panel run-report-export" aria-label="Confluence publishing">
    <h2>Confluence</h2><p>Publish this run’s outcome, key metrics, and transaction summary to a page beneath the configured parent page.</p>
    {connections.length===0 && <p>No enabled Confluence connection is available for this project.</p>}
    {connections.length>0 && !canPublish && <p>Project editor access is required to publish.</p>}
    {connections.length>0 && canPublish && <div className="run-report-export-actions"><label>Connection <select aria-label="Confluence connection" value={selected} onChange={event => setSelected(event.target.value)}>{connections.map(connection => <option key={connection.id} value={connection.id}>{connection.name}</option>)}</select></label><button type="button" className="btn btn-sm btn-secondary" disabled={busy || !selected} onClick={async () => {setBusy(true);setError('');try {await api.publishRunToConfluence(testId,selected);await refresh()} catch(cause) {setError(getErrorMessage(cause,'Confluence publish failed'));await refresh()} finally {setBusy(false)}}}>{busy?'Publishing…':publications.some(p => p.connection_id===selected && p.status==='complete')?'Update Confluence page':'Publish to Confluence'}</button></div>}
    {publications.map(publication => <p key={publication.connection_id} className="run-report-muted">{connections.find(connection => connection.id===publication.connection_id)?.name || 'Confluence connection'}: {publication.status}{publication.error ? ` — ${publication.error}` : ''}{publication.page_url && <> · <a href={publication.page_url} target="_blank" rel="noreferrer">Open page</a></>}</p>)}
    {error && <p role="alert" className="run-report-error">{error}</p>}
  </section>
}
