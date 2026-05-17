import { type StoredScript } from '../types'
import { type ImportedScript } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import SampleScriptImport from './SampleScriptImport'
import { type ImportedSaveResult } from './useScriptsAuthoringState'

interface Props {
  onImported?: (result: ImportedSaveResult) => void
  onEdit?: (script: ScriptDefinition) => void
  onClose?: () => void
}

export default function JavaHttpImport({ onImported, onEdit, onClose }: Props) {
  return <SampleScriptImport preset="java-http" onImported={onImported} onEdit={onEdit} onClose={onClose} />
}
