import { useState } from 'react'

import { readArchiveBrowserHAREntry, type BrowserHAREntryResponse } from '../hooks/api/useBrowserHARApi'
import { getErrorMessage } from '../utils/errorUtils'
import Notice from './ux/Notice'

// BrowserHAREntryView shows one retained request as its HAR entry: the last
// step of the browser drill-down (finding → document → resources → HAR entry).
// A request whose browser context recorded a native HAR is in that HAR, which
// the view names instead.
export default function BrowserHAREntryView({ testID, requestID, onDownload }: { testID: string; requestID: string; onDownload?: (artifact: string) => void }) {
  const [state, setState] = useState<{ loading: boolean; response: BrowserHAREntryResponse | null; error: string }>({ loading: false, response: null, error: '' })
  const load = async () => {
    setState({ loading: true, response: null, error: '' })
    try {
      setState({ loading: false, response: await readArchiveBrowserHAREntry(testID, requestID), error: '' })
    } catch (err) {
      setState({ loading: false, response: null, error: getErrorMessage(err, 'Unable to read the HAR entry') })
    }
  }
  const { loading, response, error } = state
  return <div className="browser-har-entry">
    {!response && <button type="button" className="btn btn-secondary btn-sm" disabled={loading} onClick={() => void load()}>{loading ? 'Reading…' : 'Show this request\'s HAR entry'}</button>}
    {error && <Notice tone="error" title="The HAR entry could not be read" details={error} action={<button type="button" className="btn btn-secondary btn-sm" onClick={() => void load()}>Retry</button>} />}
    {response?.fidelity === 'native' && <p>This request's browser context recorded a native HAR{response.artifact ? <>, <code>{response.artifact}</code>, which holds it</> : ', which holds it'}; the synthetic HAR leaves it out.
      {response.artifact && onDownload && <> <button type="button" className="btn btn-ghost btn-sm" onClick={() => onDownload(response.artifact!)}>Download that HAR</button></>}</p>}
    {response && response.fidelity !== 'native' && <>
      <p>Synthetic HAR entry derived from the retained network evidence{response.headers_omitted ? ' (this archive keeps no headers)' : ''}.</p>
      <pre className="browser-har-entry-json" aria-label="HAR entry">{JSON.stringify(response.entry, null, 2)}</pre>
    </>}
  </div>
}
