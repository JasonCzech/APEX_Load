package api

import "time"

// Notifications only invalidate a view. The ordinary run stream must not
// disclose shared-data aliases, values, lease capabilities, or table counts:
// subscribers fetch an authoritative view through the separately scoped API.
// At most one hint per run per second can enter the bounded telemetry hub.
func (tr *TestRunner) notifyLiveTableChange(run *runLiveTables, now time.Time) {
	if tr.hub == nil || run == nil {
		return
	}
	current := now.UnixNano()
	previous := run.lastNotification.Load()
	if previous != 0 && current-previous < int64(time.Second) {
		return
	}
	if !run.lastNotification.CompareAndSwap(previous, current) {
		return
	}
	tr.hub.BroadcastScopedJSON(run.testID, run.projectID, map[string]any{
		"type": "live_tables_changed", "test_id": run.testID,
		"event_index": run.notificationSeq.Add(1), "timestamp": now.UTC(),
	})
}
