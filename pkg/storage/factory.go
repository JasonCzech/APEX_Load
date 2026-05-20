package storage

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	StoreBackendSQLite   = "sqlite"
	StoreBackendPostgres = "postgres"
)

type StoreConfig struct {
	Backend     string
	SQLitePath  string
	DatabaseURL string
	Connections StoreConnectionOptions
}

type BackendInfoProvider interface {
	BackendInfo() StoreBackendInfo
}

func StoreConfigFromEnv() StoreConfig {
	backend := strings.TrimSpace(strings.ToLower(os.Getenv("STORMRUNNER_STORE")))
	if backend == "" {
		backend = StoreBackendSQLite
	}
	sqlitePath := strings.TrimSpace(os.Getenv("STORMRUNNER_DB"))
	if sqlitePath == "" {
		sqlitePath = "stormrunner.db"
	}
	return StoreConfig{
		Backend:     backend,
		SQLitePath:  sqlitePath,
		DatabaseURL: strings.TrimSpace(os.Getenv("STORMRUNNER_DATABASE_URL")),
		Connections: StoreConnectionOptions{
			MaxOpenConns:    envInt("STORMRUNNER_DATABASE_MAX_OPEN_CONNS"),
			MaxIdleConns:    envInt("STORMRUNNER_DATABASE_MAX_IDLE_CONNS"),
			ConnMaxLifetime: envDuration("STORMRUNNER_DATABASE_CONN_MAX_LIFETIME"),
		},
	}
}

func NewConfiguredStore(config StoreConfig) (Store, error) {
	switch strings.TrimSpace(strings.ToLower(config.Backend)) {
	case "", StoreBackendSQLite:
		path := strings.TrimSpace(config.SQLitePath)
		if path == "" {
			path = "stormrunner.db"
		}
		return NewSQLiteStore(path)
	case StoreBackendPostgres, "postgresql":
		return NewPostgresStore(config.DatabaseURL, config.Connections)
	default:
		return nil, fmt.Errorf("unsupported stormrunner store backend %q", config.Backend)
	}
}

func NewStoreFromEnv() (Store, error) {
	return NewConfiguredStore(StoreConfigFromEnv())
}

func envInt(name string) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func envDuration(name string) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < 0 {
		return 0
	}
	return value
}
