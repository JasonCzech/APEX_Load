# Engine snapshot synchronization review — 2026-10-03

The first repository attempt failed positive snapshot assertions that assumed asynchronous aggregation and resource sampling would complete within fixed 150 ms and 90 ms sleeps. The tests now wait up to five seconds for a published snapshot. The raw-action fixture queues all ten samples before Start, ensuring they drain into one aggregation window even when the test goroutine is delayed.

All transaction/error/action counts, active VUser gauges, resource presence and timestamp assertions remain. The negative idle-window case retains its original 90 ms observation period and absence assertion. No production code, collection interval or production deadline changed.

The protocol owner independently reviewed the test-only diff and found no additional issues. Focused verification passed with `-count=1`: default engine fixtures in 0.604 seconds and race fixtures in 1.765 seconds, without a race warning. Retained output: [default](engine-snapshot-default.txt), [race](engine-snapshot-race.txt).

```sh
GOCACHE=/private/tmp/apexload-feature-completion-go-cache GOMAXPROCS=2 GOMEMLIMIT=2GiB \
  go test -p 1 -count=1 -timeout 5m ./pkg/engine \
  -run '^(TestMetricsCollector_AggregateProduces(Snapshot|IdleSnapshot)|TestMetricsCollector_EmptyWindowSnapshotEmission)$'
```

The same command with `-race` passed. These focused checks supplement the final repository gate; they do not replace it.

The first targeted API race attempt failed the existing HTTP preparation supervision RPC deadline and cleanup wait (49.690 seconds); no data-race warning appeared. Its [original output](api-resource-race-r0-first-attempt.txt) is preserved as a failed attempt, not acceptance. The serialized current-source race passed in 20.828 seconds with the placement correction and unchanged fixture/production deadlines: [final output](api-resource-race-r0-final.txt). The earlier deadline failure did not reproduce; the evidence does not establish its underlying scheduling cause.
