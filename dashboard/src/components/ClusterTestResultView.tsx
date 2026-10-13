import type { ClusterTestResult } from '../types/cluster'
import { clusterProbeLabel, describeProbe } from './clusterConnectionModel'

/** A connection test's outcome and next step, with each check's raw result under Details. */
export default function ClusterTestResultView({ result }: { result: ClusterTestResult }) {
  const outcome = describeProbe(result)
  return (
    <div data-testid="cluster-test-result" className={`cluster-test-result ${outcome.tone}`} role="status">
      <strong>{outcome.text}</strong>
      {outcome.nextStep && <p>{outcome.nextStep}</p>}
      {result.probes.length > 0 && (
        <details>
          <summary>Details</summary>
          <ul>
            {result.probes.map(probe => (
              <li key={probe.probe}>
                <span>{probe.ok ? 'Passed' : probe.optional ? 'Unavailable' : 'Failed'}</span> {clusterProbeLabel(probe.probe)}{probe.message ? `: ${probe.message}` : ''}
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
  )
}
