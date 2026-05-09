package config

import "time"

type GeneratorConfig struct {
	ListenAddr     string        `json:"listen_addr"`
	MaxVUsers      int           `json:"max_vusers"`
	MetricsBuffer  int           `json:"metrics_buffer"`
	ControllerAddr string        `json:"controller_addr"`
	HeartbeatInterval time.Duration `json:"heartbeat_interval"`
}

type ControllerConfig struct {
	ListenAddr     string `json:"listen_addr"`
	APIAddr        string `json:"api_addr"`
	PostgresDSN    string `json:"postgres_dsn"`
	RedisAddr      string `json:"redis_addr"`
	MaxConcurrent  int    `json:"max_concurrent_tests"`
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

func DefaultControllerConfig() ControllerConfig {
	return ControllerConfig{
		ListenAddr:    ":9091",
		APIAddr:       ":8080",
		MaxConcurrent: 10,
	}
}
