import type { GeneratorShardSummary } from '../types/testConfiguration'

export default function RuntimeResourceReservations({ shards }: { shards?: GeneratorShardSummary[] }) {
  const reservations = shards?.filter(shard => shard.runtime_resource_claim) || []
  if (!reservations.length) return null
  return <section className="manifest-section" aria-label="Event Hubs processor reservations">
    <h4>Event Hubs processor reservations</h4>
    <p>Captured admission reserves the peak workload and each processor’s partition-worker ceiling.</p>
    {reservations.map(shard => {
      const request = shard.runtime_resource_claim?.requested
      const admission = shard.runtime_resource_admission
      return <div key={`${shard.group}-${shard.target}`}>
        <strong>{shard.group} · {shard.target}</strong>
        <dl>
          <dt>Requested processors / workers</dt><dd>{request?.processors ?? 'Unavailable'} / {request?.workers ?? 'Unavailable'}</dd>
          <dt>Admitted processors / workers</dt><dd>{admission?.admitted?.processors ?? 'Unavailable'} / {admission?.admitted?.workers ?? 'Unavailable'}</dd>
          <dt>Partition ceiling per processor</dt><dd>{request?.max_partitions_per_processor ?? 'Unavailable'}</dd>
          <dt>Admission</dt><dd>{admission?.current ? 'Confirmed at reservation' : 'Unconfirmed'}</dd>
        </dl>
      </div>
    })}
  </section>
}
