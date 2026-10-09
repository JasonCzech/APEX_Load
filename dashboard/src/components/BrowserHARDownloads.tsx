import { useState } from 'react'
import Notice from './ux/Notice'
import { downloadArchiveBrowserHAR, downloadArchiveWithHARs, type BrowserHARArchiveList } from '../hooks/api/useBrowserHARApi'

interface Props {
  testID: string
  list: BrowserHARArchiveList | null
  listError?: string
  archiveMissing?: boolean
  onRetryList?: () => void
}

const bytes = (value?: number) => value === undefined ? 'Derived on download' : `${value.toLocaleString('en-US')} B`

/** A run archive's HARs: native recordings and the synthetic HAR derived from
 * retained network entries. Anyone who can view the run sees the list; only
 * editors download (native HARs carry scrubbed response content). */
export default function BrowserHARDownloads({ testID, list, listError = '', archiveMissing = false, onRetryList }: Props) {
  // downloadError names which download failed: one HAR, or the run archive.
  const [downloadError, setError] = useState<{ key: string; message: string } | null>(null)
  const [busy, setBusy] = useState('')
  const run = async (key: string, action: () => Promise<void>) => {
    setBusy(key); setError(null)
    try { await action() } catch (reason) { setError({ key, message: reason instanceof Error ? reason.message : 'Download failed.' }) } finally { setBusy('') }
  }
  return <section className="tx-section archive-summary-section" aria-label="Browser HARs">
    <h3 className="chart-title">Browser HARs</h3>
    <p>Native HARs are the browser&apos;s own recordings. The synthetic HAR is built from retained network entries for contexts without one; it never invents timings or content.</p>
    {listError && <Notice tone="error" title="Browser HARs could not be listed" details={listError} action={onRetryList && <button type="button" className="btn btn-secondary btn-sm" onClick={onRetryList}>Retry browser HARs</button>} />}
    {downloadError && <Notice tone="error" title={downloadError.key === 'zip' ? 'The run archive could not be downloaded' : 'The HAR could not be downloaded'} details={downloadError.message} />}
    {!list ? archiveMissing ? <p role="note">This run's saved archive is not available yet (it may still be publishing), so its browser HARs cannot be listed yet.{onRetryList && <> <button type="button" className="btn btn-secondary btn-sm" onClick={onRetryList}>Check again</button></>}</p> : !listError && <p role="status">Loading browser HARs…</p> : <>
      {!list.can_download && <p role="status">Downloading a HAR needs {list.download_role} access to this project.</p>}
      {list.incomplete && <p>Some native HARs were not retained{list.omitted ? `: ${Object.entries(list.omitted).map(([reason, count]) => `${count} ${reason.replace(/_/g, ' ')}`).join(', ')}` : ''}.</p>}
      <table className="tx-table"><thead><tr><th scope="col">HAR</th><th scope="col">Fidelity</th><th scope="col">Size</th><th scope="col">Download</th></tr></thead>
        <tbody>{list.artifacts.map(item => <tr key={item.name}>
          <td>{item.name}</td><td>{item.fidelity}</td><td>{item.available ? bytes(item.bytes) : `Unavailable (${(item.unavailable_reason || 'unknown').replace(/_/g, ' ')})`}</td>
          <td><button type="button" className="btn btn-secondary btn-sm" disabled={!list.can_download || !item.available || busy !== ''} onClick={() => void run(item.name, () => downloadArchiveBrowserHAR(testID, item.name))}>{busy === item.name ? 'Downloading…' : `Download ${item.fidelity} HAR`}</button></td>
        </tr>)}</tbody></table>
      {list.can_download && list.artifacts.some(item => item.fidelity === 'native') && <button type="button" className="btn btn-secondary btn-sm" disabled={busy !== ''} onClick={() => void run('zip', () => downloadArchiveWithHARs(testID))}>{busy === 'zip' ? 'Preparing archive…' : 'Download archive with native HARs'}</button>}
    </>}
  </section>
}
