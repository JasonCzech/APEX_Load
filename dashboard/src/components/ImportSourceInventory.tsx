import { useMemo, useState } from 'react'
import { copyTextToClipboard } from '../utils/clipboardUtils'
import type { ScriptDefinition } from '../types/scripts'
import { getImportSummary } from './scriptEditorImportModel'

export default function ImportSourceInventory({ script }: { script: ScriptDefinition }) {
  const metadata = script.metadata?.import
  const summary = useMemo(() => getImportSummary(metadata), [metadata])
  const metadataJson = useMemo(() => JSON.stringify(metadata, null, 2), [metadata])
  const [copyStatus, setCopyStatus] = useState('')
  if (!metadata) return null

  return (
    <details className="import-source-inventory">
      <summary>Archive details</summary>
      <h3>Source files & import metadata</h3>
      <dl>
        <dt>Imported source</dt><dd>{metadata.source_script_name || script.name} · {metadata.source || 'Not specified'}</dd>
        <dt>Runtime files</dt><dd>{summary.runtimeFiles.join(', ') || 'None captured'}</dd>
        <dt>Parameter files</dt><dd>{summary.parameterFiles.join(', ') || 'None captured'}</dd>
        <dt>Project sections</dt><dd>{summary.projectSections.join(', ') || 'None captured'}</dd>
      </dl>
      <details>
        <summary>View preserved metadata JSON</summary>
        <div className="import-metadata-copy-toolbar">
          <button type="button" className="btn btn-sm btn-secondary" onClick={async () => {
            setCopyStatus('')
            const copied = await copyTextToClipboard(metadataJson!)
            setCopyStatus(copied ? 'Copied to clipboard.' : 'Unable to copy. Select and copy the JSON below.')
          }}>Copy JSON</button>
          <span role="status">{copyStatus}</span>
        </div>
        <pre>{metadataJson}</pre>
      </details>
    </details>
  )
}
