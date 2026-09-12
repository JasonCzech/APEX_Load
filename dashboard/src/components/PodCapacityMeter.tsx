import type { PodCapacitySnapshot } from '../types/capacity'

export default function PodCapacityMeter({ capacity }: { capacity?: PodCapacitySnapshot }) {
  if (!capacity) return null
  if (capacity.state !== 'ready') return <span role="status">Peak capacity unavailable ({capacity.state})</span>
  return (
    <span style={{ display: 'inline-flex', flexDirection: 'column', gap: 4, maxWidth: '100%' }}>
      <span>{capacity.reserved_pct.toFixed(0)}% reserved / {capacity.remaining_pct.toFixed(0)}% remaining</span>
      <progress aria-label="Reserved peak capacity" max={100} value={Math.min(100, capacity.reserved_pct)} style={{ width: '100%', minWidth: 100 }} />
      <small>{capacity.headroom_pct}% headroom excluded{capacity.dedicated ? ' · Dedicated' : ''}{capacity.bottleneck ? ` · ${capacity.bottleneck}` : ''}</small>
    </span>
  )
}
