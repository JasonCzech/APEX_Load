package config

import (
	"testing"
	"time"
)

func TestLoadGeneratorConfigFromEnv(t *testing.T) {
	env := map[string]string{
		"APEXLOAD_GENERATOR_LISTEN_ADDR":        ":19090",
		"APEXLOAD_GENERATOR_MAX_VUSERS":         "2500",
		"APEXLOAD_GENERATOR_HEARTBEAT_INTERVAL": "750ms",
		"APEXLOAD_CONTROLLER_ADDR":              "controller.internal:9091",
	}
	cfg := LoadGeneratorConfigFromEnv(func(key string) string { return env[key] })
	if cfg.ListenAddr != ":19090" {
		t.Fatalf("ListenAddr = %q, want :19090", cfg.ListenAddr)
	}
	if cfg.MaxVUsers != 2500 {
		t.Fatalf("MaxVUsers = %d, want 2500", cfg.MaxVUsers)
	}
	if cfg.HeartbeatInterval != 750*time.Millisecond {
		t.Fatalf("HeartbeatInterval = %s, want 750ms", cfg.HeartbeatInterval)
	}
	if cfg.ControllerAddr != "controller.internal:9091" {
		t.Fatalf("ControllerAddr = %q, want controller.internal:9091", cfg.ControllerAddr)
	}
}

func TestLoadGeneratorConfigFromEnvIgnoresInvalidCapacity(t *testing.T) {
	cfg := LoadGeneratorConfigFromEnv(func(key string) string {
		if key == "APEXLOAD_GENERATOR_MAX_VUSERS" {
			return "-1"
		}
		return ""
	})
	if cfg.MaxVUsers != DefaultGeneratorConfig().MaxVUsers {
		t.Fatalf("MaxVUsers = %d, want default %d", cfg.MaxVUsers, DefaultGeneratorConfig().MaxVUsers)
	}
}
