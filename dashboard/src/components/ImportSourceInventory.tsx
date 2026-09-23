import { useMemo } from 'react'
import type { ScriptDefinition } from '../types/scripts'
import { getImportSummary } from './scriptEditorImportModel'
export default function ImportSourceInventory({ script }: { script: ScriptDefinition }) {
 const metadata = script.metadata?.import
 const summary = useMemo(() => getImportSummary(metadata), [metadata])
 if (!metadata) return null
 return <section className="import-source-inventory"><h3>Source files & import metadata</h3><dl>
  <dt>Imported source</dt><dd>{metadata.source_script_name || script.name} · {metadata.source || 'Not specified'}</dd>
  <dt>Runtime files</dt><dd>{summary.runtimeFiles.join(', ') || 'None captured'}</dd>
  <dt>Parameter files</dt><dd>{summary.parameterFiles.join(', ') || 'None captured'}</dd>
  <dt>Project sections</dt><dd>{summary.projectSections.join(', ') || 'None captured'}</dd>
 </dl><details><summary>View preserved metadata JSON</summary><pre>{JSON.stringify(metadata, null, 2)}</pre></details></section>
}
