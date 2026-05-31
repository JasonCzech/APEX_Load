package database

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestDatabaseProtocolExecutesQueryAndAssertions(t *testing.T) {
	db := &Database{}
	if err := db.Init(context.Background(), map[string]any{
		"database_driver":   "sqlite",
		"database_dsn":      ":memory:",
		"database_max_rows": float64(10),
	}); err != nil {
		t.Fatalf("init database: %v", err)
	}
	defer db.Teardown(context.Background())

	for _, action := range []*protocol.Action{
		{Name: "create", Type: "db_exec", Target: "create table users (id integer primary key, name text)"},
		{Name: "insert", Type: "db_exec", Target: "insert into users(name) values (?)", Params: map[string]any{"binds": []any{"Ada"}}, Assertions: []protocol.Assertion{{Type: "rows_affected", Expected: 1}}},
	} {
		result, err := db.Execute(context.Background(), action)
		if err != nil {
			t.Fatalf("execute %s: %v", action.Name, err)
		}
		if !result.Success {
			t.Fatalf("execute %s success = false: %s", action.Name, result.ErrorMessage)
		}
	}

	result, err := db.Execute(context.Background(), &protocol.Action{
		Name:       "query",
		Type:       "db_query",
		Target:     "select name from users where name = ?",
		Params:     map[string]any{"binds": []any{"Ada"}},
		Assertions: []protocol.Assertion{{Type: "row_count", Expected: 1}},
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !result.Success {
		t.Fatalf("query failed: %s", result.ErrorMessage)
	}
	if result.DependencyType != "database" || result.DependencyTarget != "sqlite" {
		t.Fatalf("dependency = %s/%s, want database/sqlite", result.DependencyType, result.DependencyTarget)
	}
	if !strings.Contains(result.ResponseBody, `"name":"Ada"`) || !strings.Contains(result.ResponseBody, `"row_count":1`) {
		t.Fatalf("response body = %s, want Ada row", result.ResponseBody)
	}
}

func TestDatabaseProtocolConnectAndDisconnectActions(t *testing.T) {
	db := &Database{}
	if err := db.Init(context.Background(), map[string]any{
		"database_driver": "sqlite",
		"database_dsn":    ":memory:",
	}); err != nil {
		t.Fatalf("init database: %v", err)
	}

	connect, err := db.Execute(context.Background(), &protocol.Action{Name: "connect", Type: "db_connect"})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if !connect.Success || !strings.Contains(connect.ResponseBody, `"connection":"open"`) {
		t.Fatalf("connect result = %#v, want open connection", connect)
	}

	disconnect, err := db.Execute(context.Background(), &protocol.Action{Name: "disconnect", Type: "db_disconnect"})
	if err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if !disconnect.Success || !strings.Contains(disconnect.ResponseBody, `"connection":"closed"`) {
		t.Fatalf("disconnect result = %#v, want closed connection", disconnect)
	}
}

func TestDatabaseProtocolTransactionsAndRollback(t *testing.T) {
	db := &Database{}
	if err := db.Init(context.Background(), map[string]any{
		"database_driver":         "sqlite",
		"database_dsn":            "file:apexload_tx_test?mode=memory&cache=shared",
		"database_max_open_conns": float64(1),
	}); err != nil {
		t.Fatalf("init database: %v", err)
	}
	defer db.Teardown(context.Background())

	_, _ = db.Execute(context.Background(), &protocol.Action{Name: "create", Type: "db_exec", Target: "create table items (id integer)"})
	if result, _ := db.Execute(context.Background(), &protocol.Action{Name: "begin", Type: "db_begin"}); !result.Success {
		t.Fatalf("begin failed: %s", result.ErrorMessage)
	}
	if result, _ := db.Execute(context.Background(), &protocol.Action{Name: "insert", Type: "db_exec", Target: "insert into items(id) values (1)"}); !result.Success {
		t.Fatalf("insert failed: %s", result.ErrorMessage)
	}
	if result, _ := db.Execute(context.Background(), &protocol.Action{Name: "rollback", Type: "db_rollback"}); !result.Success {
		t.Fatalf("rollback failed: %s", result.ErrorMessage)
	}
	result, _ := db.Execute(context.Background(), &protocol.Action{Name: "query", Type: "db_query", Target: "select id from items"})
	if !result.Success || !strings.Contains(result.ResponseBody, `"row_count":0`) {
		t.Fatalf("query after rollback = success %v body %s err %s, want zero rows", result.Success, result.ResponseBody, result.ErrorMessage)
	}
}

func TestDatabaseProtocolReadOnlyBlocksMutations(t *testing.T) {
	db := &Database{}
	if err := db.Init(context.Background(), map[string]any{
		"database_driver":    "sqlite",
		"database_dsn":       ":memory:",
		"database_read_only": true,
	}); err != nil {
		t.Fatalf("init database: %v", err)
	}
	defer db.Teardown(context.Background())

	result, err := db.Execute(context.Background(), &protocol.Action{Name: "create", Type: "db_exec", Target: "create table blocked (id integer)"})
	if err != nil {
		t.Fatalf("execute readonly mutation: %v", err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "read_only") {
		t.Fatalf("result = %#v, want read_only block", result)
	}
}
