package main

import (
	"strconv"
	"sync"
)

// keepAwake holds an OS sleep/idle inhibitor while load tests are running, so a
// multi-hour run isn't silently corrupted by the machine sleeping or (on macOS)
// App Nap throttling the background timers that drive pacing/metrics.
//
// The platform backend is provided by startSleepInhibitor (build-tagged per OS).
// `starter` is overridable so the start/stop state machine is unit-testable
// without spawning a real inhibitor.
type keepAwake struct {
	mu      sync.Mutex
	active  bool
	stop    func()
	starter func() func()
}

// set turns the inhibitor on or off. Idempotent: repeated identical calls are
// no-ops, so it can be driven directly by the dashboard's active-run count.
func (k *keepAwake) set(on bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if on == k.active {
		return
	}
	if on {
		start := k.starter
		if start == nil {
			start = startSleepInhibitor
		}
		k.stop = start()
		k.active = true
		return
	}
	if k.stop != nil {
		k.stop()
		k.stop = nil
	}
	k.active = false
}

// inhibitorCommand returns the subprocess that holds a sleep/idle lock on
// macOS/Linux. Killing the process releases the lock. Pure + unit-testable;
// returns an empty name on platforms that use a non-subprocess mechanism.
func inhibitorCommand(goos string, parentPID int) (string, []string) {
	switch goos {
	case "darwin":
		// -i prevents idle system sleep while caffeinate runs; -w ties the
		// assertion to the app process so a crash/force-quit releases it.
		return "caffeinate", []string{"-i", "-w", strconv.Itoa(parentPID)}
	case "linux":
		// Hold an idle+sleep inhibitor lock for the lifetime of `sleep infinity`.
		return "systemd-inhibit", []string{
			"--what=idle:sleep",
			"--who=APEX Load",
			"--why=load test running",
			"--mode=block",
			"sleep", "infinity",
		}
	default:
		return "", nil
	}
}

// SetKeepAwake is the bound bridge method: the dashboard calls it with true while
// any run is active and false when none remain.
func (a *App) SetKeepAwake(active bool) {
	a.keepAwake.set(active)
}
