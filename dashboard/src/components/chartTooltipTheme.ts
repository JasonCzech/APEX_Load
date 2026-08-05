import type { CSSProperties } from 'react'

export const chartTooltipContentStyle: CSSProperties = {
  background: 'var(--bg-elevated)',
  border: '1px solid var(--border)',
  borderRadius: 10,
  color: 'var(--text-primary)',
  boxShadow: 'var(--shadow-sm)',
}

export const chartTooltipLabelStyle: CSSProperties = {
  color: 'var(--text-primary)',
  fontWeight: 700,
}

// Recharts otherwise sorts legend/tooltip rows by their values, causing series
// labels to jump between samples. A stable equal key preserves declaration order.
export const preserveChartItemOrder = () => 0
