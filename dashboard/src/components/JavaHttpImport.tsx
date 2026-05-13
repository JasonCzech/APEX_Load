import { type ImportedScript } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import SampleScriptImport from './SampleScriptImport'

interface Props {
  onImported?: (script: ImportedScript) => void
  onEdit?: (script: ScriptDefinition) => void
  onClose?: () => void
}

export default function JavaHttpImport({ onImported, onEdit, onClose }: Props) {
  return <SampleScriptImport preset="java-http" onImported={onImported} onEdit={onEdit} onClose={onClose} />
}