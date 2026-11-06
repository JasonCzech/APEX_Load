/**
 * A JMeter element's Comments as a read-only note: documentation the source
 * plan carries (business context such as "Billing System = TLG"), never
 * behavior. Nothing renders when the element has none.
 */
export default function JMeterSourceComments({ comment, label = 'Source comments' }: { comment: string; label?: string }) {
  if (!comment) return null
  return <small className="jmeter-source-comments" role="note" aria-label={label} style={{ whiteSpace: 'pre-wrap' }}>{label}: {comment}</small>
}
