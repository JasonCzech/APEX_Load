package config

import (
	"fmt"
	"log/slog"
	"strings"
)

// ResolveLogLevel returns the structured-log threshold configured for the
// controller and generator. The APEXLOAD-prefixed setting is authoritative;
// LOG_LEVEL remains a compatibility alias for existing deployments.
func ResolveLogLevel(getenv func(string) string) (slog.Level, error) {
	if getenv == nil {
		return slog.LevelInfo, nil
	}
	raw := strings.TrimSpace(getenv("APEXLOAD_LOG_LEVEL"))
	if raw == "" {
		raw = strings.TrimSpace(getenv("LOG_LEVEL"))
	}
	if raw == "" {
		return slog.LevelInfo, nil
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(raw)); err != nil {
		return slog.LevelInfo, fmt.Errorf("invalid APEXLOAD_LOG_LEVEL %q (expected debug, info, warn, or error): %w", raw, err)
	}
	return level, nil
}
