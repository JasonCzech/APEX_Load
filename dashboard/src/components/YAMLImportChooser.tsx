import { useEffect, useState } from 'react'

import { type ScriptDefinition } from '../types/scripts'
import { type DraftEditorFocus, type ImportedSaveResult } from './useScriptsAuthoringState'
import BrunoImport from './BrunoImport'
import ProtocolAssetImport from './ProtocolAssetImport'

interface Props {
  initialFiles?: File[] | null
  projectId?: string
  folderId?: string | null
  onFolderChange?: (folderId: string) => void
  onImported?: (result: ImportedSaveResult) => void
  onEdit?: (script: ScriptDefinition, focus?: DraftEditorFocus) => void
  onClose?: () => void
  onOperationBusyChange?: (busy: boolean) => void
}

export default function YAMLImportChooser({ initialFiles, projectId, folderId, onFolderChange, onImported, onEdit, onClose, onOperationBusyChange }: Props) {
  const [selection, setSelection] = useState<{
    source: File[] | null | undefined
    choice: 'bruno' | 'openapi' | null
  }>(() => ({ source: initialFiles, choice: null }))
  const choice = selection.source === initialFiles ? selection.choice : null
  const setChoice = (nextChoice: 'bruno' | 'openapi' | null) => {
    setSelection({ source: initialFiles, choice: nextChoice })
  }
  useEffect(() => {
    if (selection.source !== initialFiles) setSelection({ source: initialFiles, choice: null })
  }, [initialFiles, selection.source])
  if (choice === 'bruno') {
    return <BrunoImport initialFiles={initialFiles} projectId={projectId} folderId={folderId} onFolderChange={onFolderChange} destinationFolderManagedExternally onImported={onImported} onClose={() => setChoice(null)} onOperationBusyChange={onOperationBusyChange} />
  }
  if (choice === 'openapi') {
    return <ProtocolAssetImport kind="openapi" initialFile={initialFiles?.[0] || null} projectId={projectId} folderId={folderId} onImported={onImported} onEdit={onEdit} onClose={() => setChoice(null)} onOperationBusyChange={onOperationBusyChange} />
  }
  return (
    <div className="bruno-import">
      <header className="bruno-import-header">
        <div>
          <span className="bruno-kicker">Ambiguous YAML source</span>
          <h2>Choose an importer</h2>
          <p>The file does not contain a definitive Bruno or OpenAPI marker. Choose the source format; neither importer will silently reinterpret unsupported content.</p>
        </div>
        {onClose && <button className="btn btn-secondary" onClick={onClose}>Back</button>}
      </header>
      <section className="bruno-summary-grid">
        <article className="bruno-card">
          <span className="bruno-kicker">Native HTTP conversion</span>
          <h3>Bruno collection or request</h3>
          <p>Convert supported Bruno HTTP requests into native APEX scripts and review conversion requirements before replay.</p>
          <button className="btn btn-primary" onClick={() => setChoice('bruno')}>Use Bruno importer</button>
        </article>
        <article className="bruno-card">
          <span className="bruno-kicker">Native HTTP generation</span>
          <h3>OpenAPI / Swagger contract</h3>
          <p>Generate a native APEX HTTP draft from an OpenAPI contract with operations, examples, authentication placeholders, and schema review.</p>
          <button className="btn btn-secondary" onClick={() => setChoice('openapi')}>Use OpenAPI importer</button>
        </article>
      </section>
    </div>
  )
}
