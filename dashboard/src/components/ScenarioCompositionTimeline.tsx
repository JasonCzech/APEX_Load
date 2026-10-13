import type { ReactNode } from 'react'
import { parseDurationToSeconds } from '../utils/timeUtils'
import type { LoadStageDraft } from '../types'
import './ScenarioCompositionTimeline.css'

export type CompositionTimelineGroup = {
  name: string
  script_id: string
  start_offset?: string
  ramp_up?: string
  ramp_down?: string
  vusers: number
  stages?: LoadStageDraft[]
  role?: 'load' | 'observer'
  /** What sets the group's load when its VUsers do not, e.g. "peak 12 TPS" from a traffic profile. */
  loadLabel?: string
}

type Props = {
  duration: string
  groups: CompositionTimelineGroup[]
  onOffsetChange: (index: number, value: string) => void
  selectedGroupIndex?: number
  onGroupSelect?: (index: number) => void
  children?: ReactNode
}

function secondsText(seconds: number) {
  if (seconds === 0) return ''
  if (seconds % 3600 === 0) return `${seconds / 3600}h`
  if (seconds % 60 === 0) return `${seconds / 60}m`
  return `${seconds}s`
}

function stageWindowSeconds(group: CompositionTimelineGroup, remaining: number) {
  if (!group.stages?.length) return remaining
  const total = group.stages.reduce((sum, stage) => sum + (parseDurationToSeconds(stage.duration) || 0), 0)
  return total > 0 ? Math.min(total, remaining) : remaining
}

export function ScenarioCompositionTimeline({ duration, groups, onOffsetChange, selectedGroupIndex = 0, onGroupSelect, children }: Props) {
  const durationSeconds = Math.max(1, parseDurationToSeconds(duration) || 1)
  return (
    <section className="composition-timeline" aria-label="Scenario composition timeline">
      <div className="composition-timeline-heading">
        <div>
          <strong>Group load &amp; timeline</strong>
          <span>Select a group to edit its load. Drag its clip to change when it starts.</span>
        </div>
        <span>0 → {duration}</span>
      </div>
      <div className="composition-timeline-ruler" aria-hidden="true">
        <span>0</span><span>25%</span><span>50%</span><span>75%</span><span>{duration}</span>
      </div>
      {groups.map((group, index) => {
        const rawOffset = parseDurationToSeconds(group.start_offset || '') || 0
        const offset = Math.min(Math.max(0, rawOffset), durationSeconds)
        const remaining = Math.max(0, durationSeconds - offset)
        const windowSeconds = stageWindowSeconds(group, remaining)
        const left = (offset / durationSeconds) * 100
        const width = Math.max(1.5, (windowSeconds / durationSeconds) * 100)
        return (
          <div className={`composition-track ${selectedGroupIndex === index ? 'selected' : ''}`} key={`${group.script_id || 'group'}-${index}`}>
            <button type="button" className="composition-track-label" aria-pressed={selectedGroupIndex === index} aria-controls={children ? 'scenario-active-group-panel' : undefined} onClick={() => onGroupSelect?.(index)}>
              <strong>{group.name || `Group ${index + 1}`}</strong>
              <span>{group.loadLabel || `${group.vusers} VUsers`} · {group.role || 'load'}</span>
            </button>
            <div className="composition-track-lane">
              <div className="composition-clip" style={{ left: `${left}%`, width: `${Math.min(width, 100 - left)}%` }}>
                <span>{group.name || `Group ${index + 1}`}</span>
                <small>{group.start_offset || '0s'} start{group.ramp_up ? ` · ${group.ramp_up} ramp` : ''}</small>
              </div>
              <input
                className="composition-track-slider"
                type="range"
                min={0}
                max={durationSeconds}
                step={1}
                value={offset}
                aria-label={`Start offset for ${group.name || `group ${index + 1}`}`}
                onChange={event => { onGroupSelect?.(index); onOffsetChange(index, secondsText(Number(event.target.value))) }}
              />
            </div>
          </div>
        )
      })}
      {children && <div className="composition-group-editor" id="scenario-active-group-panel">{children}</div>}
    </section>
  )
}
