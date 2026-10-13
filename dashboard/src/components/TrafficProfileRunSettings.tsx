import type { GoalConfig, TrafficProfile } from '../types'
import { parseDurationToSeconds } from '../utils/timeUtils'
import CreateTestGoalSettings from './CreateTestGoalSettings'
import { formatTrafficProfileAxisTime, trafficProfilePeak } from './trafficProfilePreviewModel'

interface Props {
  profile: TrafficProfile
  /** The goal controller's limits and guardrails; omitted when the profile sets VUsers directly. */
  goalConfig?: GoalConfig
  onGoalConfigChange: <K extends keyof GoalConfig>(field: K, value: GoalConfig[K]) => void
  protocols?: readonly string[]
  onChangeProfile: () => void
}

const formatNumber = (value: number) => value.toLocaleString(undefined, { maximumFractionDigits: 2 })

function sourceLabel(source: TrafficProfile['source']): string {
  if (source.kind === 'multiple') return `${source.sources?.length ?? 0} files, one per group`
  if (source.kind === 'prometheus') return 'Prometheus query'
  if (source.kind === 'opensearch') return source.opensearch?.index ? `OpenSearch index ${source.opensearch.index}` : 'OpenSearch query'
  return source.file?.filename || source.name || 'Uploaded file'
}

function scaleLabel(profile: TrafficProfile, unit: string): string {
  const amplitude = profile.amplitude
  if (amplitude?.mode === 'peak' && amplitude.value !== undefined) return `Peak scaled to ${formatNumber(amplitude.value)} ${unit}`
  return `${formatNumber(amplitude?.value ?? 1)} × the source`
}

/**
 * Run settings for a scenario a traffic profile drives: what the profile
 * asks for, and (for a TPS profile) the VUser limits and guardrails the goal
 * controller keeps while following it. There is no throughput target to
 * enter; the profile is the target.
 */
export default function TrafficProfileRunSettings({ profile, goalConfig, onGoalConfigChange, protocols, onChangeProfile }: Props) {
  const unit = profile.runtime_target === 'tps' ? 'TPS' : 'VUsers'
  const sizing = profile.sizing?.complete ? profile.sizing : undefined
  const replaySeconds = parseDurationToSeconds(profile.target_duration)
  return (
    <div className="scenario-subpanel">
      <div className="scenario-assignment-header">
        <div>
          <h4>Traffic profile</h4>
          <p>{profile.runtime_target === 'tps'
            ? 'The profile is the throughput target: the controller adds or removes VUsers to follow it. The preview shows its targets over time.'
            : 'The profile sets each mapped group’s VUsers over time. The preview shows them.'}</p>
        </div>
        <button type="button" className="btn btn-secondary" onClick={onChangeProfile}>Change profile</button>
      </div>
      <dl className="scenario-profile-summary">
        <div><dt>Peak</dt><dd>{formatNumber(trafficProfilePeak(profile))} {unit}</dd></div>
        <div><dt>Replay length</dt><dd>{replaySeconds > 0 ? formatTrafficProfileAxisTime(replaySeconds) : profile.target_duration}</dd></div>
        <div><dt>Scale</dt><dd>{scaleLabel(profile, unit)}</dd></div>
        <div><dt>Groups</dt><dd>{profile.mappings.map(mapping => mapping.group_name).join(', ')}</dd></div>
        <div><dt>Source</dt><dd>{sourceLabel(profile.source)}</dd></div>
        {sizing && <div><dt>Estimated peak VUsers</dt><dd>{(sizing.estimated_peak_vusers ?? 0).toLocaleString()} (suggested limits {(sizing.suggested_min_vusers ?? 0).toLocaleString()}–{(sizing.suggested_max_vusers ?? 0).toLocaleString()})</dd></div>}
      </dl>
      {goalConfig && <CreateTestGoalSettings profileManaged protocols={protocols} goalConfig={goalConfig} onGoalConfigChange={onGoalConfigChange} />}
    </div>
  )
}
