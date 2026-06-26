package config

import (
	"strconv"
	"strings"
	"time"
)

type GeneratorConfig struct {
	ListenAddr        string        `json:"listen_addr"`
	MaxVUsers         int           `json:"max_vusers"`
	MetricsBuffer     int           `json:"metrics_buffer"`
	ControllerAddr    string        `json:"controller_addr"`
	HeartbeatInterval time.Duration `json:"heartbeat_interval"`
}

type ControllerConfig struct {
	ListenAddr    string `json:"listen_addr"`
	APIAddr       string `json:"api_addr"`
	PostgresDSN   string `json:"postgres_dsn"`
	RedisAddr     string `json:"redis_addr"`
	MaxConcurrent int    `json:"max_concurrent_tests"`
}

func DefaultGeneratorConfig() GeneratorConfig {
	return GeneratorConfig{
		ListenAddr:        ":9090",
		MaxVUsers:         10000,
		MetricsBuffer:     10000,
		ControllerAddr:    "controller:9091",
		HeartbeatInterval: 5 * time.Second,
	}
}

func LoadGeneratorConfigFromEnv(getenv func(string) string) GeneratorConfig {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	cfg := DefaultGeneratorConfig()
	if value := strings.TrimSpace(getenv("APEXLOAD_GENERATOR_LISTEN_ADDR")); value != "" {
		cfg.ListenAddr = value
	} else if value := strings.TrimSpace(getenv("APEXLOAD_GENERATOR_ADDR")); value != "" {
		cfg.ListenAddr = value
	}
	if value := strings.TrimSpace(getenv("APEXLOAD_GENERATOR_MAX_VUSERS")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			cfg.MaxVUsers = parsed
		}
	}
	if value := strings.TrimSpace(getenv("APEXLOAD_GENERATOR_HEARTBEAT_INTERVAL")); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			cfg.HeartbeatInterval = parsed
		}
	}
	if value := strings.TrimSpace(getenv("APEXLOAD_CONTROLLER_ADDR")); value != "" {
		cfg.ControllerAddr = value
	}
	return cfg
}

func DefaultControllerConfig() ControllerConfig {
	return ControllerConfig{
		ListenAddr:    ":9091",
		APIAddr:       ":8080",
		MaxConcurrent: 10,
	}
}
