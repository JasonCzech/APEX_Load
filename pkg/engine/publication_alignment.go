package engine

import "time"

// PublishOnCollection calls publish once per interval, each time right after
// a metrics collection tick (ticked returns the channel closed by the next
// one), until stop is closed.
//
// A publisher on its own ticker reads the latest snapshot at an unrelated
// phase: the snapshot is then up to one collection interval old when it is
// published, and that age was reported as generator processing. Following the
// collection keeps the publication cadence while the reported age measures the
// work between a collection and its publication. A tick that comes too soon
// after the previous publication (interval less a quarter, at most 250 ms) is
// skipped, so a cadence longer than the collection interval publishes on
// every n-th collection. Without ticks (no engine yet, or a stopped one), a
// watchdog publishes every interval and a half.
func PublishOnCollection(stop <-chan struct{}, interval time.Duration, ticked func() <-chan struct{}, publish func()) {
	PublishOnCollectionEvery(stop, func() time.Duration { return interval }, ticked, publish)
}

// PublishOnCollectionEvery is PublishOnCollection with a cadence that may
// change while it runs (a run's telemetry policy segment): interval is read
// before each wait.
func PublishOnCollectionEvery(stop <-chan struct{}, interval func() time.Duration, ticked func() <-chan struct{}, publish func()) {
	current := func() time.Duration {
		if value := interval(); value > 0 {
			return value
		}
		return time.Second
	}
	cadence := current()
	watchdog := time.NewTimer(cadence + cadence/2)
	defer watchdog.Stop()
	var last time.Time
	for {
		var tick <-chan struct{}
		if ticked != nil {
			tick = ticked()
		}
		select {
		case <-stop:
			return
		case <-tick:
			cadence = current()
			if !last.IsZero() && time.Since(last) < cadence-min(cadence/4, 250*time.Millisecond) {
				continue
			}
		case <-watchdog.C:
		}
		// Stamped when the publication starts, so a slow publication does
		// not push the next due tick past the cadence.
		last = time.Now()
		publish()
		cadence = current()
		if !watchdog.Stop() {
			select {
			case <-watchdog.C:
			default:
			}
		}
		watchdog.Reset(cadence + cadence/2)
	}
}
