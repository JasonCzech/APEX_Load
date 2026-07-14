package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// PostgreSQL cannot create every foreign key inline because the shared schema
// contains forward references (for example, applications references projects,
// which is created later). The dialect rewriter therefore removes references
// while creating/upgrading columns, and this pass restores the complete declared
// relationship graph once every table and compatibility column exists.
type postgresForeignKeySpec struct {
	table      string
	column     string
	references string
	refColumn  string
	onDelete   string
}

var (
	createTableNamePattern  = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+IF\s+NOT\s+EXISTS\s+([a-z_][a-z0-9_]*)`)
	alterTableNamePattern   = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+([a-z_][a-z0-9_]*)\s+ADD\s+COLUMN\s+([a-z_][a-z0-9_]*)`)
	foreignKeyColumnPattern = regexp.MustCompile(`(?i)FOREIGN\s+KEY\s*\(\s*([a-z_][a-z0-9_]*)\s*\)`)
	referencePattern        = regexp.MustCompile(`(?i)REFERENCES\s+([a-z_][a-z0-9_]*)\s*\(\s*([a-z_][a-z0-9_]*)\s*\)(?:\s+ON\s+DELETE\s+(CASCADE|SET\s+NULL|RESTRICT|NO\s+ACTION))?`)
)

func postgresForeignKeySpecs() ([]postgresForeignKeySpec, error) {
	specs := make([]postgresForeignKeySpec, 0, 40)
	for _, statement := range schemaCreateTableStatements() {
		tableMatch := createTableNamePattern.FindStringSubmatch(statement)
		if len(tableMatch) != 2 {
			continue
		}
		for _, line := range strings.Split(statement, "\n") {
			if !strings.Contains(strings.ToUpper(line), "REFERENCES") {
				continue
			}
			spec, err := parsePostgresForeignKeyLine(tableMatch[1], "", line)
			if err != nil {
				return nil, err
			}
			specs = append(specs, spec)
		}
	}
	for _, migration := range schemaColumnMigrations() {
		if !strings.Contains(strings.ToUpper(migration.query), "REFERENCES") {
			continue
		}
		tableMatch := alterTableNamePattern.FindStringSubmatch(migration.query)
		if len(tableMatch) != 3 {
			return nil, fmt.Errorf("parse PostgreSQL foreign key migration %s: missing table or column", migration.name)
		}
		spec, err := parsePostgresForeignKeyLine(tableMatch[1], tableMatch[2], migration.query)
		if err != nil {
			return nil, fmt.Errorf("parse PostgreSQL foreign key migration %s: %w", migration.name, err)
		}
		specs = append(specs, spec)
	}

	byColumn := make(map[string]postgresForeignKeySpec, len(specs))
	for _, spec := range specs {
		key := spec.table + "\x00" + spec.column
		if previous, ok := byColumn[key]; ok {
			if previous.references != spec.references || previous.refColumn != spec.refColumn || previous.onDelete != spec.onDelete {
				return nil, fmt.Errorf("conflicting PostgreSQL foreign keys declared for %s.%s", spec.table, spec.column)
			}
			continue
		}
		byColumn[key] = spec
	}

	result := make([]postgresForeignKeySpec, 0, len(byColumn))
	for _, spec := range byColumn {
		result = append(result, spec)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].table == result[j].table {
			return result[i].column < result[j].column
		}
		return result[i].table < result[j].table
	})
	return result, nil
}

func parsePostgresForeignKeyLine(table, column, line string) (postgresForeignKeySpec, error) {
	referenceMatch := referencePattern.FindStringSubmatch(line)
	if len(referenceMatch) != 4 {
		return postgresForeignKeySpec{}, fmt.Errorf("parse PostgreSQL foreign key for %s from %q", table, strings.TrimSpace(line))
	}
	if column == "" {
		if keyMatch := foreignKeyColumnPattern.FindStringSubmatch(line); len(keyMatch) == 2 {
			column = keyMatch[1]
		} else {
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) == 0 {
				return postgresForeignKeySpec{}, fmt.Errorf("parse PostgreSQL foreign key column for %s from %q", table, strings.TrimSpace(line))
			}
			column = strings.Trim(fields[0], `"`)
		}
	}
	return postgresForeignKeySpec{
		table:      strings.ToLower(table),
		column:     strings.ToLower(column),
		references: strings.ToLower(referenceMatch[1]),
		refColumn:  strings.ToLower(referenceMatch[2]),
		onDelete:   strings.ToUpper(strings.Join(strings.Fields(referenceMatch[3]), " ")),
	}, nil
}

func postgresForeignKeyConstraintName(spec postgresForeignKeySpec) string {
	name := "apexload_fk_" + spec.table + "_" + spec.column
	if len(name) <= 63 {
		return name
	}
	digest := sha256.Sum256([]byte(name))
	return name[:54] + "_" + hex.EncodeToString(digest[:])[:8]
}

func quotePostgresIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func postgresAddForeignKeyStatement(spec postgresForeignKeySpec) string {
	name := postgresForeignKeyConstraintName(spec)
	onDelete := ""
	if spec.onDelete != "" {
		onDelete = " ON DELETE " + spec.onDelete
	}
	deleteAction := "a"
	switch spec.onDelete {
	case "CASCADE":
		deleteAction = "c"
	case "SET NULL":
		deleteAction = "n"
	case "RESTRICT":
		deleteAction = "r"
	case "", "NO ACTION":
		deleteAction = "a"
	}
	// The duplicate_object handler makes simultaneous HA controller startup safe:
	// both replicas may observe a missing constraint before one wins the table lock.
	// Checking the complete catalog shape also repairs an older or manually-created
	// constraint that happens to reuse the deterministic name with the wrong target
	// or delete action.
	return fmt.Sprintf(`DO $apexload$
DECLARE
	existing_definition text;
	constraint_matches boolean := false;
BEGIN
	SELECT
		pg_get_constraintdef(constraint_row.oid),
		constraint_row.contype = 'f'
			AND constraint_row.confrelid = '%s'::regclass
			AND constraint_row.conkey = ARRAY[(
				SELECT attnum FROM pg_attribute
				WHERE attrelid = '%s'::regclass AND attname = '%s' AND NOT attisdropped
			)]::smallint[]
			AND constraint_row.confkey = ARRAY[(
				SELECT attnum FROM pg_attribute
				WHERE attrelid = '%s'::regclass AND attname = '%s' AND NOT attisdropped
			)]::smallint[]
			AND constraint_row.confdeltype = '%s'
			AND constraint_row.confupdtype = 'a'
			AND constraint_row.confmatchtype = 's'
			AND NOT constraint_row.condeferrable
			AND NOT constraint_row.condeferred
	INTO existing_definition, constraint_matches
	FROM pg_constraint AS constraint_row
	WHERE constraint_row.conname = '%s'
		AND constraint_row.conrelid = '%s'::regclass;

	IF existing_definition IS NOT NULL AND NOT constraint_matches THEN
		ALTER TABLE %s DROP CONSTRAINT %s;
		existing_definition := NULL;
	END IF;

	IF existing_definition IS NULL THEN
		ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)%s NOT VALID;
	END IF;
EXCEPTION WHEN duplicate_object THEN
	NULL;
END;
$apexload$;`,
		spec.references,
		spec.table,
		spec.column,
		spec.references,
		spec.refColumn,
		deleteAction,
		name,
		spec.table,
		quotePostgresIdentifier(spec.table),
		quotePostgresIdentifier(name),
		quotePostgresIdentifier(spec.table),
		quotePostgresIdentifier(name),
		quotePostgresIdentifier(spec.column),
		quotePostgresIdentifier(spec.references),
		quotePostgresIdentifier(spec.refColumn),
		onDelete,
	)
}

func postgresValidateForeignKeyStatement(spec postgresForeignKeySpec) string {
	return fmt.Sprintf(
		"ALTER TABLE %s VALIDATE CONSTRAINT %s",
		quotePostgresIdentifier(spec.table),
		quotePostgresIdentifier(postgresForeignKeyConstraintName(spec)),
	)
}

func (s *SQLiteStore) ensurePostgresForeignKeys() error {
	if s == nil || s.db == nil || s.db.dialect != storeDialectPostgres {
		return nil
	}
	specs, err := postgresForeignKeySpecs()
	if err != nil {
		return err
	}
	for _, spec := range specs {
		// These statements are already PostgreSQL-specific. Sending them through
		// storeDB.Exec would intentionally remove FOREIGN KEY clauses because that
		// rewriter is what makes the earlier forward-referencing CREATE TABLE pass
		// possible.
		if _, err := s.db.db.Exec(postgresAddForeignKeyStatement(spec)); err != nil {
			return fmt.Errorf("create PostgreSQL foreign key %s.%s -> %s.%s: %w", spec.table, spec.column, spec.references, spec.refColumn, err)
		}
	}
	for _, spec := range specs {
		if _, err := s.db.db.Exec(postgresValidateForeignKeyStatement(spec)); err != nil {
			return fmt.Errorf(
				"validate PostgreSQL foreign key %s.%s -> %s.%s: repair existing orphaned rows before restarting the controller: %w",
				spec.table, spec.column, spec.references, spec.refColumn, err,
			)
		}
	}
	return nil
}
