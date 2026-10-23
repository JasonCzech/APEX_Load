// Command datamodeldoc generates docs/DATA_MODEL.md, the Help reference for
// the controller's persistent data model. It migrates a throwaway SQLite
// database with storage.NewSQLiteStore and reads the tables, columns, keys and
// foreign keys back from SQLite, so the page always matches the schema code.
// Run it after every schema change: `go generate ./pkg/storage`.
//
// Each domain renders as a Mermaid erDiagram (drawn natively by the Help
// center, and by GitHub) followed by a table of keys and references.
//
//	-out PATH   output path (default docs/DATA_MODEL.md)
//	-check      do not write; exit 1 if the file at -out differs from the output
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
	_ "modernc.org/sqlite"
)

const defaultOut = "docs/DATA_MODEL.md"

// maxAttributes caps the columns shown in a diagram box; the rest are counted.
const maxAttributes = 9

// notableColumns are shown after the key columns while there is room.
var notableColumns = []string{"ref_key", "name", "subject", "email", "status", "state", "kind", "protocol", "role",
	"scope_type", "resource_type", "revision", "content_revision", "version", "action", "mode", "alias"}

type column struct {
	name, typ     string
	notNull       bool
	primaryKeyPos int
}

type foreignKey struct{ column, parent string }

type table struct {
	name        string
	columns     []column
	foreignKeys []foreignKey
	uniqueSets  [][]string
}

type schema struct {
	version int
	tables  map[string]*table
}

// reference is one outgoing link from a table to the table it holds IDs of.
type reference struct {
	column, parent     string
	declared, required bool
	unique             bool
}

func loadSchema() (*schema, error) {
	dir, err := os.MkdirTemp("", "datamodeldoc-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "schema.db")
	store, err := storage.NewSQLiteStore(path)
	if err != nil {
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	status, err := store.SchemaStatus()
	closeErr := store.Close()
	if err != nil {
		return nil, fmt.Errorf("read schema status: %w", err)
	}
	if closeErr != nil {
		return nil, closeErr
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	names, err := queryStrings(db, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	out := &schema{version: status.CurrentVersion, tables: map[string]*table{}}
	for _, name := range names {
		t := &table{name: name}
		rows, err := db.Query(`SELECT name, type, "notnull", pk FROM pragma_table_info(?) ORDER BY cid`, name)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var c column
			if err := rows.Scan(&c.name, &c.typ, &c.notNull, &c.primaryKeyPos); err != nil {
				rows.Close()
				return nil, err
			}
			t.columns = append(t.columns, c)
		}
		if err := closeRows(rows); err != nil {
			return nil, err
		}
		rows, err = db.Query(`SELECT "from", "table" FROM pragma_foreign_key_list(?) ORDER BY id, seq`, name)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var fk foreignKey
			if err := rows.Scan(&fk.column, &fk.parent); err != nil {
				rows.Close()
				return nil, err
			}
			t.foreignKeys = append(t.foreignKeys, fk)
		}
		if err := closeRows(rows); err != nil {
			return nil, err
		}
		// A partial index is unique only within its WHERE clause, and an
		// expression index (NULL column name) is not unique on any one column.
		indexes, err := queryStrings(db, `SELECT name FROM pragma_index_list(?) WHERE "unique" = 1 AND partial = 0 ORDER BY name`, name)
		if err != nil {
			return nil, err
		}
		for _, index := range indexes {
			cols, err := queryStrings(db, `SELECT COALESCE(name, '') FROM pragma_index_info(?) ORDER BY seqno`, index)
			if err != nil {
				return nil, err
			}
			if !slices.Contains(cols, "") {
				t.uniqueSets = append(t.uniqueSets, cols)
			}
		}
		out.tables[name] = t
	}
	return out, nil
}

func queryStrings(db *sql.DB, query string, args ...any) ([]string, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, value)
	}
	return out, closeRows(rows)
}

func closeRows(rows *sql.Rows) error {
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	return rows.Close()
}

func (t *table) column(name string) (column, bool) {
	for _, c := range t.columns {
		if c.name == name {
			return c, true
		}
	}
	return column{}, false
}

func (t *table) primaryKey() []string {
	var cols []column
	for _, c := range t.columns {
		if c.primaryKeyPos > 0 {
			cols = append(cols, c)
		}
	}
	sort.Slice(cols, func(i, j int) bool { return cols[i].primaryKeyPos < cols[j].primaryKeyPos })
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.name
	}
	return names
}

// uniqueAlone reports whether a column alone identifies a row, so a reference
// through it is one-to-one rather than one-to-many.
func (t *table) uniqueAlone(name string) bool {
	if pk := t.primaryKey(); len(pk) == 1 && pk[0] == name {
		return true
	}
	for _, set := range t.uniqueSets {
		if len(set) == 1 && set[0] == name {
			return true
		}
	}
	return false
}

func (t *table) references() []reference {
	var out []reference
	seen := map[string]bool{}
	add := func(col, parent string, declared bool) {
		if seen[col] {
			return
		}
		seen[col] = true
		// Without a constraint nothing guarantees the parent row exists, and
		// many such columns store '' for "none", so only a declared, non-null
		// foreign key is drawn as exactly one parent.
		c, _ := t.column(col)
		out = append(out, reference{column: col, parent: parent, declared: declared,
			required: declared && (c.notNull || c.primaryKeyPos > 0), unique: t.uniqueAlone(col)})
	}
	for _, fk := range t.foreignKeys {
		add(fk.column, fk.parent, true)
	}
	for _, c := range t.columns {
		if parent, ok := logicalRefs[columnRef{t.name, c.name}]; ok {
			add(c.name, parent, false)
		} else if parent, ok := hubColumns[c.name]; ok && t.name != parent {
			add(c.name, parent, false)
		}
	}
	return out
}

func mermaidType(typ string) string {
	typ = strings.TrimSpace(strings.SplitN(typ, "(", 2)[0])
	if typ == "" {
		return "TEXT"
	}
	return strings.ReplaceAll(typ, " ", "_")
}

// diagramAttributes picks the key columns plus a few identifying columns.
func diagramAttributes(t *table) []string {
	refs := map[string]bool{}
	for _, r := range t.references() {
		refs[r.column] = true
	}
	chosen := map[string]bool{}
	for _, c := range t.columns {
		if c.primaryKeyPos > 0 || refs[c.name] || polymorphicRefs[columnRef{t.name, c.name}] {
			chosen[c.name] = true
		}
	}
	for _, name := range notableColumns {
		if len(chosen) >= maxAttributes {
			break
		}
		if _, ok := t.column(name); ok {
			chosen[name] = true
		}
	}
	var lines []string
	for _, c := range t.columns {
		if !chosen[c.name] {
			continue
		}
		var keys []string
		if c.primaryKeyPos > 0 {
			keys = append(keys, "PK")
		}
		if refs[c.name] {
			keys = append(keys, "FK")
		}
		line := mermaidType(c.typ) + " " + c.name
		if len(keys) > 0 {
			line += " " + strings.Join(keys, ", ")
		}
		if polymorphicRefs[columnRef{t.name, c.name}] {
			line += ` "any resource"`
		}
		lines = append(lines, line)
	}
	if rest := len(t.columns) - len(chosen); rest > 0 {
		lines = append(lines, fmt.Sprintf(`etc more "+%d columns"`, rest))
	}
	return lines
}

func relationship(parent, child, label string, required, unique, declared bool) string {
	left, right, line := "|o", "o{", ".."
	if required {
		left = "||"
	}
	if unique {
		right = "o|"
	}
	if declared {
		line = "--"
	}
	return fmt.Sprintf("    %s %s%s%s %s : %q", parent, left, line, right, child, label)
}

func renderDomain(s *schema, d domain) (diagram string, drawn int, edges int) {
	inDomain := map[string]bool{}
	for _, name := range d.tables {
		inDomain[name] = true
	}
	linked := map[string]bool{}
	for _, name := range d.tables {
		for _, r := range s.tables[name].references() {
			if inDomain[r.parent] || !quietHubs[r.parent] {
				linked[name] = true
				if inDomain[r.parent] {
					linked[r.parent] = true
				}
			}
		}
	}
	var b strings.Builder
	b.WriteString("```mermaid\nerDiagram\n")
	var relationships []string
	for _, name := range d.tables {
		if !linked[name] {
			continue
		}
		drawn++
		t := s.tables[name]
		fmt.Fprintf(&b, "    %s {\n", name)
		for _, line := range diagramAttributes(t) {
			fmt.Fprintf(&b, "        %s\n", line)
		}
		b.WriteString("    }\n")
		for _, r := range t.references() {
			if !inDomain[r.parent] && quietHubs[r.parent] {
				continue
			}
			relationships = append(relationships, relationship(r.parent, name, r.column, r.required, r.unique, r.declared))
		}
	}
	for _, line := range relationships {
		b.WriteString(line + "\n")
	}
	b.WriteString("```\n")
	if drawn == 0 {
		return "", 0, 0
	}
	return b.String(), drawn, len(relationships)
}

func renderReferenceTable(s *schema, d domain) string {
	var b strings.Builder
	b.WriteString("| Table | Primary key | References | Columns |\n| --- | --- | --- | --- |\n")
	for _, name := range d.tables {
		t := s.tables[name]
		var refs []string
		for _, r := range t.references() {
			entry := fmt.Sprintf("`%s` → `%s`", r.column, r.parent)
			if !r.declared {
				entry += " (no constraint)"
			}
			refs = append(refs, entry)
		}
		for _, c := range t.columns {
			if polymorphicRefs[columnRef{name, c.name}] {
				refs = append(refs, fmt.Sprintf("`%s` → any resource", c.name))
			}
		}
		if len(refs) == 0 {
			refs = []string{"—"}
		}
		pk := t.primaryKey()
		pkText := "—"
		if len(pk) > 0 {
			pkText = "`" + strings.Join(pk, "`, `") + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %d |\n", name, pkText, strings.Join(refs, "; "), len(t.columns))
	}
	return b.String()
}

func renderSpine(s *schema) string {
	var b strings.Builder
	b.WriteString("```mermaid\nerDiagram\n")
	for _, e := range spine {
		required, unique, declared := false, false, false
		for _, r := range s.tables[e.table].references() {
			if r.column == e.column {
				required, unique, declared = r.required, r.unique, r.declared
			}
		}
		b.WriteString(relationship(e.parent, e.table, e.column, required, unique, declared) + "\n")
	}
	b.WriteString("```\n")
	return b.String()
}

func render(s *schema) string {
	declared, logical, columns := 0, 0, 0
	for _, t := range s.tables {
		declared += len(t.foreignKeys)
		columns += len(t.columns)
		for _, r := range t.references() {
			if !r.declared {
				logical++
			}
		}
	}
	var b strings.Builder
	b.WriteString("# Data Model\n\n")
	b.WriteString("<!-- Generated by `go generate ./pkg/storage` (cmd/datamodeldoc). Do not edit by hand. -->\n\n")
	fmt.Fprintf(&b, "This reference describes the tables the APEX Load controller stores, how they relate, and which domain of the product each one belongs to. It is generated from schema version %d: %d tables, %d columns, %d foreign key constraints and %d references the controller maintains without a constraint, where the referenced row may be missing.\n\n",
		s.version, len(s.tables), columns, declared, logical)
	b.WriteString("Use this page when you plan [backups](ENTERPRISE_PLATFORM_RUNBOOK.md#postgresql-backup-and-restore) and [migrations](ENTERPRISE_PLATFORM_RUNBOOK.md#sqlite-to-postgresql-migration-guide), write reporting queries, or trace a run's records. The API is the supported way to read and change data; change the database directly only when a runbook tells you to.\n\n")
	b.WriteString("SQLite and PostgreSQL deployments use the same tables and keys. Column types are shown as SQLite declares them. On PostgreSQL, DATETIME columns are TIMESTAMPTZ; the `enabled`, `active`, `shakeout_enabled`, `resource_baseline_enabled`, `baseline_pinned`, `base_missing` and `auto_promote_on_heal` flags are BOOLEAN; and every other INTEGER column, including the remaining 0/1 flags, is BIGINT.\n\n")

	b.WriteString("## Overview\n\n")
	b.WriteString("Most tables belong to a project, directly or through a parent table, and a project belongs to a domain. A script belongs to a project and can sit in a folder, and a scenario groups scripts through `scenario_scripts`. A run is a row in `tests`: it references its scenario, release and capacity timeslot, and the run's results, archive, root-cause analysis and monitoring records all point back to it through `test_id`.\n\n")
	b.WriteString(renderSpine(s))
	b.WriteString("\nA run's placement settings, frozen in `assignment_json` and per group in `groups_json`, may name a fleet pool or specific generators. That optional link is not a column, so it is drawn dashed.\n\n")

	b.WriteString("## Reading the diagrams\n\n")
	b.WriteString("- Each box is a table. It lists the primary key (PK), the columns that reference another table (FK), and a few identifying columns. The count at the bottom is the number of columns not shown; the table under each diagram lists every table in the domain.\n")
	b.WriteString("- A line joins a table to the table whose ID it holds. In Help the arrowhead is at the referenced table; GitHub draws the crow's foot at the referencing table instead. A solid line is a foreign key constraint. A dashed line is a reference with no database constraint, so the referenced row may be missing: use an outer join when you follow one.\n")
	b.WriteString("- Tables from another domain appear as boxes with no columns.\n")
	b.WriteString("- Links to `projects`, `users` and `domains` are drawn only in the overview and in the Tenancy, identity & access diagram, because nearly every table has one. Elsewhere they appear as FK columns, and tables whose only links are these are listed in the table but not drawn.\n")
	b.WriteString("- Columns ending in `_json`, and `payload`, hold nested documents such as a run's scenario snapshot. They are not split into tables.\n\n")

	for _, d := range domains {
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", d.title, d.summary)
		if diagram, _, _ := renderDomain(s, d); diagram != "" {
			b.WriteString(diagram + "\n")
		}
		b.WriteString(renderReferenceTable(s, d))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func main() {
	out := flag.String("out", defaultOut, "output markdown path")
	check := flag.Bool("check", false, "verify the file at -out is up to date instead of writing it")
	flag.Parse()

	s, err := loadSchema()
	if err != nil {
		fmt.Fprintf(os.Stderr, "datamodeldoc: %v\n", err)
		os.Exit(1)
	}
	if err := validate(s); err != nil {
		fmt.Fprintf(os.Stderr, "datamodeldoc: %v\n", err)
		os.Exit(1)
	}
	content := render(s)
	if *check {
		got, err := os.ReadFile(*out)
		if err != nil || string(got) != content {
			fmt.Fprintf(os.Stderr, "datamodeldoc: %s is stale; run `go generate ./pkg/storage`\n", *out)
			os.Exit(1)
		}
		return
	}
	if err := os.WriteFile(*out, []byte(content), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "datamodeldoc: %v\n", err)
		os.Exit(1)
	}
}

// validate keeps the hand-maintained grouping honest against the live schema.
func validate(s *schema) error {
	var problems []string
	owner := map[string]string{}
	for _, d := range domains {
		for _, name := range d.tables {
			if _, ok := s.tables[name]; !ok {
				problems = append(problems, fmt.Sprintf("domain %q lists %s, which is not in the schema", d.title, name))
			}
			if previous, ok := owner[name]; ok {
				problems = append(problems, fmt.Sprintf("%s is listed in both %q and %q", name, previous, d.title))
			}
			owner[name] = d.title
		}
	}
	for name := range s.tables {
		if _, ok := owner[name]; !ok {
			problems = append(problems, fmt.Sprintf("table %s has no domain; add it to a domain in cmd/datamodeldoc/domains.go", name))
		}
	}
	for ref, parent := range logicalRefs {
		t, ok := s.tables[ref.table]
		if !ok {
			problems = append(problems, fmt.Sprintf("logical reference %s.%s: no such table", ref.table, ref.column))
			continue
		}
		if _, ok := t.column(ref.column); !ok {
			problems = append(problems, fmt.Sprintf("logical reference %s.%s: no such column", ref.table, ref.column))
		}
		if _, ok := s.tables[parent]; !ok {
			problems = append(problems, fmt.Sprintf("logical reference %s.%s: no such parent table %s", ref.table, ref.column, parent))
		}
		for _, fk := range t.foreignKeys {
			if fk.column == ref.column {
				problems = append(problems, fmt.Sprintf("logical reference %s.%s is now a foreign key; remove it from logicalRefs", ref.table, ref.column))
			}
		}
	}
	for name, t := range s.tables {
		matched := map[string]bool{}
		for _, r := range t.references() {
			matched[r.column] = true
		}
		pk := t.primaryKey()
		for _, c := range t.columns {
			ref := columnRef{name, c.name}
			if !strings.HasSuffix(c.name, "_id") || matched[c.name] || polymorphicRefs[ref] || notReferences[ref] || (len(pk) == 1 && pk[0] == c.name) {
				continue
			}
			problems = append(problems, fmt.Sprintf("%s.%s is not classified; add it to logicalRefs, polymorphicRefs or notReferences in cmd/datamodeldoc/domains.go", name, c.name))
		}
	}
	for ref := range notReferences {
		if t, ok := s.tables[ref.table]; !ok {
			problems = append(problems, fmt.Sprintf("notReferences %s.%s: no such table", ref.table, ref.column))
		} else if _, ok := t.column(ref.column); !ok {
			problems = append(problems, fmt.Sprintf("notReferences %s.%s: no such column", ref.table, ref.column))
		}
	}
	for ref := range polymorphicRefs {
		if t, ok := s.tables[ref.table]; !ok {
			problems = append(problems, fmt.Sprintf("polymorphic reference %s.%s: no such table", ref.table, ref.column))
		} else if _, ok := t.column(ref.column); !ok {
			problems = append(problems, fmt.Sprintf("polymorphic reference %s.%s: no such column", ref.table, ref.column))
		}
	}
	for _, e := range spine {
		t, ok := s.tables[e.table]
		if !ok {
			problems = append(problems, fmt.Sprintf("overview edge %s.%s: no such table", e.table, e.column))
			continue
		}
		if _, ok := t.column(e.column); !ok {
			problems = append(problems, fmt.Sprintf("overview edge %s.%s: no such column", e.table, e.column))
		}
		if e.jsonOnly {
			continue
		}
		found := false
		for _, r := range t.references() {
			found = found || (r.column == e.column && r.parent == e.parent)
		}
		if !found {
			problems = append(problems, fmt.Sprintf("overview edge %s.%s → %s is not a reference in the schema", e.table, e.column, e.parent))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("data model grouping is out of date:\n  %s", strings.Join(problems, "\n  "))
}
