import { AreaChart, Area, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } from 'recharts'

import { DraftCapacityEstimate } from '../types'
import { formatTimeCompact } from '../utils/timeUtils'
import { chartTooltipContentStyle, chartTooltipLabelStyle, preserveChartItemOrder } from './chartTooltipTheme'

interface PreviewGroup {
  name: string
}

interface Props {
  chartData: Array<Record<string, number>>
  groups: PreviewGroup[]
  draftEstimate: DraftCapacityEstimate | null
  estimateLoading: boolean
  estimateError: string
}

const GROUP_COLORS = ['#00a3ff', '#00ff94', '#ff006e', '#ffbe0b', '#8338ec', '#fb5607']

function formatDraftAvailabilitySource(source?: 'configured' | 'configured_plus_live', hasLive?: boolean): string {
  if (source === 'configured_plus_live') {
    return hasLive ? 'Live snapshot + configured ceiling' : 'Configured ceiling'
  }
  return 'Configured inventory'
}

function formatDraftAvailabilityDetail(estimate: DraftCapacityEstimate): string {
  const advisory = estimate.capacity_advisory
  if (!advisory) return 'Configured inventory only'
  if (advisory.availability_source === 'configured_plus_live') {
    if (advisory.live_available_generators !== undefined) {
      return `${advisory.live_available_generators} live / ${advisory.configured_available_generators || advisory.available_generators} configured`
    }
    return `${advisory.configured_available_generators || advisory.available_generators} configured ceiling`
  }
  return `${advisory.configured_available_generators || advisory.available_generators} configured`
}

export default function CreateTestCapacityPreview({ chartData, groups, draftEstimate, estimateLoading, estimateError }: Props) {
  return (
    <div className="load-profile-preview">
      <div className="chart-container">
        <ResponsiveContainer width="100%" height={160}>
          <AreaChart data={chartData} margin={{ top: 10, right: 10, left: 18, bottom: 8 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.1)" vertical={false} />
            <XAxis
              dataKey="time"
              tickFormatter={(value) => formatTimeCompact(value)}
              stroke="var(--text-muted)"
              fontSize={10}
              height={36}
              tickLine={false}
              label={{ value: 'Elapsed time', position: 'insideBottom', offset: -4, fontSize: 10, fill: 'var(--text-muted)' }}
            />
            <YAxis
              stroke="var(--text-muted)"
              fontSize={10}
              width={64}
              tickLine={false}
              label={{ value: 'VUsers', angle: -90, position: 'left', fontSize: 10, fill: 'var(--text-muted)' }}
            />
            <Tooltip
              itemSorter={preserveChartItemOrder}
              labelFormatter={(value) => `Time: ${formatTimeCompact(value)}`}
              contentStyle={{ ...chartTooltipContentStyle, fontSize: '12px' }}
              labelStyle={chartTooltipLabelStyle}
              itemStyle={{ padding: '2px 0' }}
            />
            {groups.map((group, index) => (
              <Area
                key={index}
                type="monotone"
                dataKey={`group${index}`}
                name={group.name || `Group ${index + 1}`}
                stackId="1"
                stroke={GROUP_COLORS[index % GROUP_COLORS.length]}
                fill={GROUP_COLORS[index % GROUP_COLORS.length]}
                fillOpacity={0.4}
                animationDuration={300}
              />
            ))}
          </AreaChart>
        </ResponsiveContainer>
        <div className="chart-label">Scenario Load Profile</div>
      </div>

      <div className={`draft-capacity-card ${draftEstimate?.capacity_advisory?.requires_override ? 'warning' : 'ready'}`}>
        <div className="draft-capacity-header">
          <h4>Draft Capacity Estimate</h4>
          {estimateLoading && <span className="draft-capacity-status">Refreshing...</span>}
        </div>
        {estimateError ? (
          <p className="draft-capacity-error">{estimateError}</p>
        ) : draftEstimate?.capacity_advisory ? (
          <>
            <div className="draft-capacity-metrics">
              <div className="draft-capacity-metric">
                <span className="draft-capacity-label">Recommended</span>
                <strong>{draftEstimate.capacity_advisory.recommended_generators}</strong>
                <span>{draftEstimate.capacity_advisory.recommended_generators === 1 ? 'generator' : 'generators'}</span>
              </div>
              <div className="draft-capacity-metric">
                <span className="draft-capacity-label">Available</span>
                <strong>{draftEstimate.capacity_advisory.available_generators}</strong>
                <span>{formatDraftAvailabilityDetail(draftEstimate)}</span>
              </div>
              <div className="draft-capacity-metric">
                <span className="draft-capacity-label">Bottleneck</span>
                <strong>{draftEstimate.capacity_advisory.bottleneck}</strong>
                <span>{draftEstimate.capacity_advisory.headroom_pct}% headroom</span>
              </div>
            </div>
            <p className="draft-capacity-copy secondary">
              Availability source: {formatDraftAvailabilitySource(draftEstimate.capacity_advisory.availability_source, draftEstimate.capacity_advisory.live_available_generators !== undefined)}. Snapshot: {formatDraftAvailabilityDetail(draftEstimate)}.
            </p>
            <p className="draft-capacity-copy">
              Estimated total load: {draftEstimate.capacity_advisory.estimated_total.cpu_cores.toFixed(2)} CPU, {Math.round(draftEstimate.capacity_advisory.estimated_total.memory_mb)} MB memory, {Math.round(draftEstimate.capacity_advisory.estimated_total.network_mbps)} Mbps network.
            </p>
            {draftEstimate.capacity_advisory.warnings && draftEstimate.capacity_advisory.warnings.length > 0 && (
              <div className="draft-capacity-warnings">
                {draftEstimate.capacity_advisory.warnings.map((warning) => (
                  <p key={warning}>{warning}</p>
                ))}
              </div>
            )}
          </>
        ) : (
          <p className="draft-capacity-copy">Enter a complete scenario to estimate the required load generators.</p>
        )}
      </div>
    </div>
  )
}
