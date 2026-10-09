import { useState } from 'react'

import { downloadBrowserReplayHAR } from '../hooks/api/useBrowserHARApi'

/** A finished replay's HAR (native when it was recorded, otherwise synthetic
 * from its network entries) and its evidence ZIP. Editors download them. */
export default function BrowserReplayHARDownloads({ sessionId }: { sessionId: string }) {
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const download = async (evidence: boolean) => {
    setBusy(evidence ? 'evidence' : 'har'); setError('')
    try { await downloadBrowserReplayHAR(sessionId, evidence) } catch (reason) { setError(reason instanceof Error ? reason.message : 'Download failed.') } finally { setBusy('') }
  }
  return <div className="browser-debugger-har-downloads" aria-label="Replay HAR downloads">
    <button type="button" className="btn btn-secondary btn-sm" disabled={busy !== ''} onClick={() => void download(false)}>{busy === 'har' ? 'Downloading…' : 'Download replay HAR'}</button>
    <button type="button" className="btn btn-secondary btn-sm" disabled={busy !== ''} onClick={() => void download(true)}>{busy === 'evidence' ? 'Downloading…' : 'Download replay evidence'}</button>
    {error && <p role="alert">{error}</p>}
  </div>
}
