package storage

import "fmt"

func liveBindingHasNoSeedPool(binding ScenarioDataPoolBinding) bool {
	if binding.PoolID != "" || binding.AccessMode != "live" {
		return false
	}
	if binding.Scope == "project" {
		return binding.ProjectTableID != ""
	}
	if binding.LiveOptions == nil {
		return false
	}
	return (binding.LiveOptions.SeedPolicy == "empty" && len(binding.LiveOptions.Columns) > 0) || (binding.LiveOptions.SeedPolicy == "original_seed" && binding.LiveOptions.SeedRunID != "")
}

func nullableLiveBindingPoolID(binding ScenarioDataPoolBinding) any {
	if liveBindingHasNoSeedPool(binding) {
		return nil
	}
	return binding.PoolID
}

// Existing installations made pool_id NOT NULL. Only explicit producer-first
// live bindings may omit it; all old rows and parent constraints are retained.
func (s *SQLiteStore) ensureLiveTableOptionalSeedBindings() error {
	if s.db.dialect == storeDialectPostgres {
		_, err := s.db.Exec(`ALTER TABLE scenario_data_pool_bindings ALTER COLUMN pool_id DROP NOT NULL`)
		return err
	}
	rows, err := s.db.Query(`PRAGMA table_info(scenario_data_pool_bindings)`)
	if err != nil {
		return err
	}
	rebuild := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "pool_id" && notnull != 0 {
			rebuild = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !rebuild {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range []string{
		`CREATE TABLE scenario_data_pool_bindings_live_migration(id TEXT PRIMARY KEY,scenario_id TEXT NOT NULL,pool_id TEXT,alias TEXT NOT NULL,access_mode TEXT NOT NULL,selection_strategy TEXT NOT NULL,created_at DATETIME NOT NULL,updated_at DATETIME NOT NULL,live_options_json TEXT,scope TEXT,project_table_id TEXT,UNIQUE(scenario_id,alias),FOREIGN KEY(scenario_id) REFERENCES scenarios(id),FOREIGN KEY(pool_id) REFERENCES shared_data_pools(id))`,
		`INSERT INTO scenario_data_pool_bindings_live_migration(id,scenario_id,pool_id,alias,access_mode,selection_strategy,created_at,updated_at,live_options_json,scope,project_table_id) SELECT id,scenario_id,pool_id,alias,access_mode,selection_strategy,created_at,updated_at,live_options_json,scope,project_table_id FROM scenario_data_pool_bindings`,
		`DROP TABLE scenario_data_pool_bindings`,
		`ALTER TABLE scenario_data_pool_bindings_live_migration RENAME TO scenario_data_pool_bindings`,
	} {
		if _, err := tx.Exec(query); err != nil {
			return fmt.Errorf("migrate live table optional seed bindings: %w", err)
		}
	}
	return tx.Commit()
}
