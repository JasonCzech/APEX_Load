import { useMemo } from 'react'
import type { BrowserReplayReport, ScriptDebugSession } from '../types/replay'
import type { ScriptDefinition, ScriptExtractorDefinition } from '../types/scripts'
import type { useApi } from '../hooks/useApi'
import type { ScriptRecordingStatus } from '../hooks/api/useScriptApi'
import { CorrelationDiagnosePanel, type RecordingStatusState, type ScriptStepRef } from './CorrelationDiagnosePanel'
import type { StepPosition } from './referenceScopeModel'
import type { CorrelationApplyResult, CorrelationExtractorRepair } from './scriptCorrelationRepair'
import { correlationDiagnosisKey, isReplayTerminal } from './scriptEditorReplayModel'

type RecordingDiffApi = Pick<ReturnType<typeof useApi>, 'diagnoseScriptReplay' | 'diagnoseCorrelations'>

export interface RecordingDiffPanelProps {
  script?: Pick<ScriptDefinition, 'init' | 'actions' | 'teardown'> | null
  scriptId?: string | null
  projectId?: string
  report: BrowserReplayReport
  activeReplaySessionId: string | null
  session: ScriptDebugSession | null
  replaying: boolean
  replayEditableActionsKey: string | null
  currentEditableActionsKey: string | null
  recordingStatus: ScriptRecordingStatus | null
  recordingStatusState: RecordingStatusState
  onRetryRecordingStatus: () => void
  actionCount: number
  api: RecordingDiffApi
  onAddExtractor: (
    actionIndex: number,
    extractor: ScriptExtractorDefinition,
    repair?: CorrelationExtractorRepair | Partial<StepPosition> | null,
    step?: Partial<StepPosition> | null,
  ) => CorrelationApplyResult
  onSelectAction?: (actionIndex: number, step?: ScriptStepRef) => void
}

/** The debug run compared with the script's recording (or an uploaded HAR): the debug Summary's Recording diff. */
export default function RecordingDiffPanel({
  script,
  scriptId,
  projectId,
  report,
  activeReplaySessionId,
  session,
  replaying,
  replayEditableActionsKey,
  currentEditableActionsKey,
  recordingStatus,
  recordingStatusState,
  onRetryRecordingStatus,
  actionCount,
  api,
  onAddExtractor,
  onSelectAction,
}: RecordingDiffPanelProps) {
  // Memoized: the event list can be large and grows on every replay progress event.
  const diagnosisKey = useMemo(() => correlationDiagnosisKey({ scriptId, report, activeReplaySessionId, replayEditableActionsKey }),
    [scriptId, report, activeReplaySessionId, replayEditableActionsKey])
  const replayMatchesCurrentScript = replayEditableActionsKey !== null &&
    currentEditableActionsKey !== null &&
    replayEditableActionsKey === currentEditableActionsKey
  return (
    <CorrelationDiagnosePanel
      script={script}
      scriptId={scriptId ?? null}
      replaySessionId={report.replay_session_id || activeReplaySessionId || undefined}
      replayTerminal={isReplayTerminal(report, replaying, session)}
      replayMatchesCurrentScript={replayMatchesCurrentScript}
      projectId={projectId}
      events={report.events}
      recordingStatus={recordingStatus}
      recordingStatusState={recordingStatusState}
      onRetryRecordingStatus={onRetryRecordingStatus}
      diagnosisKey={diagnosisKey}
      currentEditableActionsKey={currentEditableActionsKey}
      actionCount={actionCount}
      diagnoseWithBaseline={(id, replay, replaySessionId) => api.diagnoseScriptReplay(id, replay, replaySessionId)}
      diagnoseWithUpload={(recorded, replay) => api.diagnoseCorrelations(recorded, replay)}
      onAddExtractor={onAddExtractor}
      onSelectAction={onSelectAction}
    />
  )
}
