import { useState } from 'react'

import { ApiError } from '../hooks/apiClient'
import { readArchiveBrowserHAREntry, type BrowserHAREntryResponse } from '../hooks/api/useBrowserHARApi'
import { getErrorMessage } from '../utils/errorUtils'
import Notice from './ux/Notice'

// BrowserHAREntryView shows one retained request as its HAR entry: the last
// step of the browser drill-down (finding → document → resources → HAR entry).
// A request whose browser context recorded a native HAR is in that HAR, which
// the view names instead; one the synthetic HAR leaves out says why.
export default function BrowserHAREntryView({ testID, requestID, onDownload }: { testID: string; requestID: string; onDownload?: (artifact: string) => void }) {
  const [state, setState] = useState<{ loading: boolean; response: BrowserHAREntryResponse | null; error: string }>({ loading: false, response: null, error: '' })
  const load = async () => {
    setState({ loading: true, response: null, error: '' })
    try {
      setState({ loading: false, response: await readArchiveBrowserHAREntry(testID, requestID), error: '' })
    } catch (err) {
      // The controller reads one browser archive at a time, within a time
      // limit: a 503 is busy or slow, not broken.
      const busy = err instanceof ApiError && err.status === 503
      setState({ loading: false, response: null, error: busy ? 'The controller is reading another browser archive, or ran out of time reading this one. Retry in a moment.' : getErrorMessage(err, 'Unable to read the HAR entry') })
    }
  }
  const { loading, response, error } = state
  return <div className="browser-har-entry">
    {!response && <button type="button" className="btn btn-secondary btn-sm" disabled={loading} onClick={() => void load()}>{loading ? 'Reading…' : 'Show this request\'s HAR entry'}</button>}
    {error && <Notice tone="error" title="The HAR entry could not be read" details={error} action={<button type="button" className="btn btn-secondary btn-sm" onClick={() => void load()}>Retry</button>} />}
    {response?.fidelity === 'native' && <p>This request's browser context recorded a native HAR{response.artifact ? <>, <code>{response.artifact}</code>, which holds it</> : ', which holds it'}; the synthetic HAR leaves it out.
      {response.artifact && onDownload && <> <button type="button" className="btn btn-ghost btn-sm" onClick={() => onDownload(response.artifact!)}>Download that HAR</button></>}</p>}
    {response?.fidelity === 'omitted' && <p>The synthetic HAR leaves this request out ({(response.omitted_reason || 'unknown').replace(/_/g, ' ')}), as a downloaded synthetic HAR does.</p>}
    {response && response.fidelity !== 'native' && response.fidelity !== 'omitted' && <>
      <p>Synthetic HAR entry derived from the retained network evidence{response.headers_omitted ? ' (this archive keeps no headers)' : ''}.</p>
      <pre className="browser-har-entry-json" aria-label="HAR entry">{JSON.stringify(response.entry, null, 2)}</pre>
    </>}
    {response && response.fidelity !== 'native' && Boolean(response.native_har_coverage_unknown) && <p className="run-report-muted">This archive also has {response.native_har_coverage_unknown} native HAR{response.native_har_coverage_unknown === 1 ? '' : 's'} that name no browser context; this request may be in one of them.</p>}
  </div>
}
