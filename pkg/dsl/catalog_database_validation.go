package dsl

import (
	"fmt"
	"strings"
)

func validateDatabaseScriptIssues(script *Script) []string {
	var issues []string
	cfg := script.Config.Database
	if strings.TrimSpace(cfg.Driver) == "" {
		issues = append(issues, "config.database.driver is required")
	} else if !isAllowedValue(strings.ToLower(strings.TrimSpace(cfg.Driver)), "sqlite", "sqlite3", "postgres", "mysql", "odbc", "oracle") {
		issues = append(issues, fmt.Sprintf("config.database.driver %q is not supported", cfg.Driver))
	}
	if strings.TrimSpace(cfg.DSN) == "" {
		issues = append(issues, "config.database.dsn is required")
	}
	if cfg.MaxOpenConns < 0 {
		issues = append(issues, "config.database.max_open_conns must be zero or greater")
	}
	if cfg.MaxIdleConns < 0 {
		issues = append(issues, "config.database.max_idle_conns must be zero or greater")
	}
	if cfg.ConnMaxLifetimeMs < 0 {
		issues = append(issues, "config.database.conn_max_lifetime_ms must be zero or greater")
	}
	if cfg.QueryTimeoutMs < 0 {
		issues = append(issues, "config.database.query_timeout_ms must be zero or greater")
	} else if cfg.QueryTimeoutMs > MaxConfiguredProtocolTimeoutMS {
		issues = append(issues, fmt.Sprintf("config.database.query_timeout_ms must not exceed %d", MaxConfiguredProtocolTimeoutMS))
	}
	if cfg.MaxRows < 0 {
		issues = append(issues, "config.database.max_rows must be zero or greater")
	}
	return issues
}
