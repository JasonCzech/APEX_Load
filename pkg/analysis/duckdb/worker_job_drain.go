package duckdb

import "time"

// workerJobDrainWarnAfter is how long a reaped worker's containment may keep
// counting it before the wait is logged. Windows signals the process handle
// before the job object stops counting the process, so one check right after
// the wait can still see it.
const workerJobDrainWarnAfter = 2 * time.Second

const workerJobDrainMaxDelay = 250 * time.Millisecond

// workerAwaitDrained polls active until it reports no live process, or until a
// query fails. Only a successful count of zero proves the containment empty,
// and a close that gave up would leave the attempt charged for the life of the
// process, so the wait has no deadline: slow, if set, is called once when it
// has lasted warnAfter.
func workerAwaitDrained(active func() (uint32, error), warnAfter time.Duration, slow func(count uint32, waited time.Duration)) error {
	began := time.Now()
	delay := time.Millisecond
	warned := false
	for {
		count, err := active()
		if err != nil || count == 0 {
			return err
		}
		if waited := time.Since(began); !warned && waited >= warnAfter {
			warned = true
			if slow != nil {
				slow(count, waited)
			}
		}
		time.Sleep(delay)
		delay = min(2*delay, workerJobDrainMaxDelay)
	}
}
