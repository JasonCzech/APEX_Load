package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"modernc.org/sqlite"
)

// The wrapper runs a second real SQLite writer after the first mutation commits,
// before the first caller receives its result. It exercises both Exec and
// RETURNING so a regression to a separate readback cannot escape the interleave.
type draftInterleaveConnector struct {
	path  string
	base  sqlite.Driver
	after func()
}

func (c *draftInterleaveConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.base.Open(sqliteDSN(c.path))
	if err != nil {
		return nil, err
	}
	return &draftInterleaveConn{Conn: conn, after: c.after}, nil
}

func (c *draftInterleaveConnector) Driver() driver.Driver { return &c.base }

type draftInterleaveConn struct {
	driver.Conn
	after func()
}

func (c *draftInterleaveConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	result, err := c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
	if err == nil && strings.HasPrefix(query, "UPDATE scenario_drafts SET scenario_id") {
		c.after()
	}
	return result, err
}

func (c *draftInterleaveConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
	if err == nil && strings.HasPrefix(query, "UPDATE scenario_drafts SET scenario_id") {
		return &draftInterleaveRows{Rows: rows, after: c.after}, nil
	}
	return rows, err
}

type draftInterleaveRows struct {
	driver.Rows
	after func()
	once  sync.Once
}

func (r *draftInterleaveRows) Close() error {
	err := r.Rows.Close()
	if err == nil {
		r.once.Do(r.after)
	}
	return err
}

func TestScenarioDraftSaveReturnsItsOwnCommittedSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "drafts.db")
	other, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	initial := &ScenarioDraft{ID: "draft", Name: "Initial", Document: `{}`, CreatedBy: "creator"}
	if err := other.CreateScenarioDraft(initial); err != nil {
		t.Fatal(err)
	}
	interleaved := false
	connector := &draftInterleaveConnector{path: path, after: func() {
		b, err := other.GetScenarioDraft(DefaultProjectID, initial.ID)
		if err != nil {
			t.Fatal(err)
		}
		if b.Revision != 2 {
			t.Fatalf("second writer observed revision %d, want 2", b.Revision)
		}
		b.Name, b.Document, b.UpdatedBy = "Writer B", `{"writer":"B"}`, "B"
		if ok, err := other.UpdateScenarioDraftIfRevision(b, 2); err != nil || !ok {
			t.Fatalf("second writer: ok=%v err=%v", ok, err)
		}
		interleaved = true
	}}
	db := sql.OpenDB(connector)
	defer db.Close()
	first := &SQLiteStore{db: &storeDB{db: db, dialect: storeDialectSQLite}}
	a := &ScenarioDraft{ID: initial.ID, Name: "Writer A", Document: `{"writer":"A"}`, UpdatedBy: "A"}
	if ok, err := first.UpdateScenarioDraftIfRevision(a, 1); err != nil || !ok {
		t.Fatalf("first writer: ok=%v err=%v", ok, err)
	}
	if !interleaved {
		t.Fatal("second writer did not run before the first response")
	}
	if a.Revision != 2 || a.Document != `{"writer":"A"}` || a.Name != "Writer A" || a.UpdatedBy != "A" || a.CreatedBy != "creator" || !a.CreatedAt.Equal(initial.CreatedAt) {
		t.Fatalf("first writer received an incorrect snapshot: %+v", a)
	}
	// The returned baseline must reject a subsequent save after B's edit.
	if ok, err := other.UpdateScenarioDraftIfRevision(a, a.Revision); err != nil || ok {
		t.Fatalf("stale follow-up save: ok=%v err=%v", ok, err)
	}
	stored, err := other.GetScenarioDraft(DefaultProjectID, initial.ID)
	if err != nil || stored.Revision != 3 || stored.Document != `{"writer":"B"}` {
		t.Fatalf("second writer's edit was lost: %+v, %v", stored, err)
	}
}
