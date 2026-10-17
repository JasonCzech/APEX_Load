// percentile_quality on report rows, SLA rule results, load levels and live
// windows. A report percentile without one used every observation; a live
// one came from the merged duration sketch.
export type PercentileQuality = 'approximate' | 'approximate_tdigest' | 'averaged'

const DESCRIPTIONS: Record<PercentileQuality, string> = {
  approximate: 'Approximate: from a duration sketch, within 0.5% of the value at its rank.',
  approximate_tdigest: 'Approximate: merged from t-digests recorded before duration sketches, with no error bound.',
  averaged: 'Averaged: a source sent no mergeable distribution, so these percentiles average the sources’ own.',
}

/** The explanation for a percentile's quality, or undefined for an exact (or sketched live) one. */
export function percentileQualityDescription(quality?: string): string | undefined {
  return quality && quality in DESCRIPTIONS ? DESCRIPTIONS[quality as PercentileQuality] : undefined
}

/** Whether live percentiles fell back from the merged duration sketch. */
export function isDegradedLivePercentileQuality(quality?: string): boolean {
  return quality === 'approximate_tdigest' || quality === 'averaged'
}
