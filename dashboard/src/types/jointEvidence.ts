// Joint evidence v4 (NA-B): a run counts every sample of one population under
// the exact tuple of up to three dimensions, so a grouped or filtered query
// reads a measured intersection rather than combining separate totals. A
// scenario chooses the projection for every run created from it.
export interface JointEvidenceProjection {
  population: 'action' | 'transaction'
  /** One to three distinct dimensions; at most one "property:<category>". */
  dimensions: string[]
}
