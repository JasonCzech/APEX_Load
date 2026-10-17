package recordbridge

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultReplaySessionTerminalTTL        = 2 * time.Hour
	defaultReplaySessionIdleCancel         = 30 * time.Minute
	defaultReplaySessionMaxSessions        = 250
	replaySessionCloseDrainTimeout         = 5 * time.Second
	defaultReplaySessionWorkerAbandonGrace = 2 * time.Minute
)

type ReplayRetentionConfig struct {
	TerminalTTL time.Duration
	IdleCancel  time.Duration
	MaxSessions int
	// workerAbandonGrace bounds how long a cancelled replay worker may ignore
	// cancellation before its session slot and HA lease are force-released and
	// the session is marked failed/orphaned. Zero disables the backstop.
	WorkerAbandonGrace time.Duration
	Now                func() time.Time
}

func ReplaySessionProjectMatches(actualProjectID, expectedProjectID string) bool {
	expectedProjectID = strings.TrimSpace(expectedProjectID)
	return expectedProjectID == "" || normalizeProjectID(actualProjectID) == normalizeProjectID(expectedProjectID)
}
func WaitForReplaySessionWorkers(sessions []<-chan struct{}, timeout time.Duration) {
	if len(sessions) == 0 {
		return
	}
	if timeout <= 0 {
		timeout = replaySessionCloseDrainTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for _, done := range sessions {
		if done == nil {
			continue
		}
		select {
		case <-done:
		case <-timer.C:
			return
		}
	}
}
func ReplaySessionTerminalTTL(ttl time.Duration) time.Duration {
	if ttl > 0 {
		return ttl
	}
	return defaultReplaySessionTerminalTTL
}
func DefaultReplayRetentionConfig() ReplayRetentionConfig {
	return ReplayRetentionConfig{
		TerminalTTL: FirstConfiguredDuration(defaultReplaySessionTerminalTTL,
			"APEXLOAD_REPLAY_SESSION_TERMINAL_TTL",
			"APEXLOAD_REPLAY_SESSION_TERMINAL_TTL_SECONDS",
		),
		IdleCancel: FirstConfiguredDuration(defaultReplaySessionIdleCancel,
			"APEXLOAD_REPLAY_SESSION_IDLE_CANCEL",
			"APEXLOAD_REPLAY_SESSION_IDLE_CANCEL_SECONDS",
		),
		MaxSessions: FirstConfiguredPositiveInt(defaultReplaySessionMaxSessions,
			"APEXLOAD_REPLAY_SESSION_MAX",
			"APEXLOAD_REPLAY_SESSION_MAX_SESSIONS",
		),
		WorkerAbandonGrace: FirstConfiguredDuration(defaultReplaySessionWorkerAbandonGrace,
			"APEXLOAD_REPLAY_SESSION_WORKER_ABANDON_GRACE",
			"APEXLOAD_REPLAY_SESSION_WORKER_ABANDON_GRACE_SECONDS",
		),
		Now: time.Now,
	}
}
func FirstConfiguredPositiveInt(fallback int, names ...string) int {
	for _, name := range names {
		raw := strings.TrimSpace(os.Getenv(name))
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err == nil && value > 0 {
			return value
		}
	}
	return fallback
}
func ReplaySessionJanitorInterval(config ReplayRetentionConfig) time.Duration {
	interval := time.Minute
	for _, candidate := range []time.Duration{config.TerminalTTL / 2, config.IdleCancel / 2} {
		if candidate > 0 && candidate < interval {
			interval = candidate
		}
	}
	if interval < time.Second {
		return time.Second
	}
	return interval
}
func ReplaySessionStatusTerminal(status string) bool {
	switch status {
	case "completed", "failed", "stopped":
		return true
	default:
		return false
	}
}
func (c ReplayRetentionConfig) clock() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}
func FirstConfiguredDuration(fallback time.Duration, names ...string) time.Duration {
	for _, name := range names {
		raw := strings.TrimSpace(os.Getenv(name))
		if raw == "" {
			continue
		}
		if strings.HasSuffix(name, "_SECONDS") {
			seconds, err := strconv.ParseFloat(raw, 64)
			if err == nil && seconds > 0 {
				return time.Duration(seconds * float64(time.Second))
			}
			continue
		}
		parsed, err := time.ParseDuration(raw)
		if err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}
func SessionRetentionSortTime(startedAt *time.Time, terminalAt *time.Time, lastActivityAt time.Time) time.Time {
	if terminalAt != nil {
		return *terminalAt
	}
	if !lastActivityAt.IsZero() {
		return lastActivityAt
	}
	if startedAt != nil {
		return *startedAt
	}
	return time.Time{}
}
