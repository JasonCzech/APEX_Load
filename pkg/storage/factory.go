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
	// TimescaleDB enables TimescaleDB hypertables for time-series tables when the
	// backend is PostgreSQL. Disabled by default; has no effect on SQLite.
	TimescaleDB bool
}

type BackendInfoProvider interface {
	BackendInfo() StoreBackendInfo
}

func StoreConfigFromEnv() StoreConfig {
	backend := strings.TrimSpace(strings.ToLower(os.Getenv("APEXLOAD_STORE")))
	if backend == "" {
		backend = StoreBackendSQLite
	}
	sqlitePath := strings.TrimSpace(os.Getenv("APEXLOAD_DB"))
	if sqlitePath == "" {
		sqlitePath = "apexload.db"
	}
	return StoreConfig{
		Backend:     backend,
		SQLitePath:  sqlitePath,
		DatabaseURL: strings.TrimSpace(os.Getenv("APEXLOAD_DATABASE_URL")),
		Connections: StoreConnectionOptions{
			MaxOpenConns:    envInt("APEXLOAD_DATABASE_MAX_OPEN_CONNS"),
			MaxIdleConns:    envInt("APEXLOAD_DATABASE_MAX_IDLE_CONNS"),
			ConnMaxLifetime: envDuration("APEXLOAD_DATABASE_CONN_MAX_LIFETIME"),
		},
		TimescaleDB: envBool("APEXLOAD_TIMESCALEDB"),
	}
}

func NewConfiguredStore(config StoreConfig) (Store, error) {
	switch strings.TrimSpace(strings.ToLower(config.Backend)) {
	case "", StoreBackendSQLite:
		path := strings.TrimSpace(config.SQLitePath)
		if path == "" {
			path = "apexload.db"
		}
		return NewSQLiteStore(path)
	case StoreBackendPostgres, "postgresql":
		return NewPostgresStore(config.DatabaseURL, config.Connections, config.TimescaleDB)
	default:
		return nil, fmt.Errorf("unsupported apexload store backend %q", config.Backend)
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

func envBool(name string) bool {
	switch strings.TrimSpace(strings.ToLower(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
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
