package api

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultReplaySessionTerminalTTL = 2 * time.Hour
	defaultReplaySessionIdleCancel  = 30 * time.Minute
	defaultReplaySessionMaxSessions = 250
)

type replaySessionRetentionConfig struct {
	terminalTTL time.Duration
	idleCancel  time.Duration
	maxSessions int
	now         func() time.Time
}

func defaultReplaySessionRetentionConfig() replaySessionRetentionConfig {
	return replaySessionRetentionConfig{
		terminalTTL: firstConfiguredDuration(defaultReplaySessionTerminalTTL,
			"APEXLOAD_REPLAY_SESSION_TERMINAL_TTL",
			"APEXLOAD_REPLAY_SESSION_TERMINAL_TTL_SECONDS",
		),
		idleCancel: firstConfiguredDuration(defaultReplaySessionIdleCancel,
			"APEXLOAD_REPLAY_SESSION_IDLE_CANCEL",
			"APEXLOAD_REPLAY_SESSION_IDLE_CANCEL_SECONDS",
		),
		maxSessions: firstConfiguredPositiveInt(defaultReplaySessionMaxSessions,
			"APEXLOAD_REPLAY_SESSION_MAX",
			"APEXLOAD_REPLAY_SESSION_MAX_SESSIONS",
		),
		now: time.Now,
	}
}

func (c replaySessionRetentionConfig) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func firstConfiguredPositiveInt(fallback int, names ...string) int {
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

func replaySessionJanitorInterval(config replaySessionRetentionConfig) time.Duration {
	interval := time.Minute
	for _, candidate := range []time.Duration{config.terminalTTL / 2, config.idleCancel / 2} {
		if candidate > 0 && candidate < interval {
			interval = candidate
		}
	}
	if interval < time.Second {
		return time.Second
	}
	return interval
}

func replaySessionStatusTerminal(status string) bool {
	switch status {
	case "completed", "failed", "stopped":
		return true
	default:
		return false
	}
}
