import type { ScriptDefinition } from '../types/scripts'
import type { ScriptAuthoringBaseline } from '../types/storedContent'
import { authoringChangedPaths } from '../utils/scriptAuthoringRevision'
import AuthoringRevisionConflict from './AuthoringRevisionConflict'

interface Props {
  reason: string
  current: ScriptAuthoringBaseline | null
  draft: ScriptDefinition
  loading: boolean
  onLoad: () => void
  onReload: () => void
  onReapply: () => void
}

export default function ScriptRevisionConflict({ reason, current, draft, loading, onLoad, onReload, onReapply }: Props) {
  return <AuthoringRevisionConflict kind="script" reason={reason} hasCurrent={Boolean(current)} loading={loading}
    diff={() => [{ paths: current ? authoringChangedPaths(current.projection.script, draft) : [], empty: 'No document-field differences. Placement or another save may still have changed the revision.' }]}
    reapplyNote="Reapply carries local changes onto this reviewed baseline and retains its current placement. Locally changed fields or action arrays take precedence; review them before saving."
    onLoad={onLoad} onReload={onReload} onReapply={onReapply} />
}
