//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"time"

	nativedb "github.com/duckdb/duckdb-go/v2"
	"github.com/jasonczech/apexload/pkg/analysis"
)

const nativeLocalTimestampFunction = "apex_local_timestamp_qualified"

// A compiled-in, fixed-signature guard, never a caller-supplied UDF or SQL.
// A unique Go timezone resolution must agree with the native conversion. A
// timezone-database disagreement therefore rejects a row instead of silently
// changing its instant. The per-job location is immutable and bounded.
type nativeLocalTimestampQualifier struct {
	location               *time.Location
	wall, absolute, result nativedb.TypeInfo
}

func (q *nativeLocalTimestampQualifier) Config() nativedb.ScalarFuncConfig {
	return nativedb.ScalarFuncConfig{InputTypeInfos: []nativedb.TypeInfo{q.wall, q.absolute}, ResultTypeInfo: q.result, SpecialNullHandling: true}
}

func (q *nativeLocalTimestampQualifier) Executor() nativedb.ScalarFuncExecutor {
	return nativedb.ScalarFuncExecutor{RowContextExecutor: func(ctx context.Context, values []driver.Value) (any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(values) != 2 {
			return nil, errWorkerProtocol
		}
		if values[0] == nil || values[1] == nil {
			return false, nil
		}
		wall, ok := values[0].(time.Time)
		if !ok {
			return nil, errWorkerProtocol
		}
		converted, ok := values[1].(time.Time)
		if !ok {
			return nil, errWorkerProtocol
		}
		instant, state := analysis.ResolveLocalTimestamp(wall, q.location)
		return state == analysis.LocalTimestampUnique && instant.Equal(converted), nil
	}}
}

func registerNativeLocalTimestampQualifier(ctx context.Context, db *sql.DB, mapping Mapping) error {
	if mapping.TimestampEncoding != "timestamp" {
		return nil
	}
	location, err := time.LoadLocation(mapping.Timezone)
	if err != nil {
		return errWorkerProtocol
	}
	wall, err := nativedb.NewTypeInfo(nativedb.TYPE_TIMESTAMP)
	if err != nil {
		return err
	}
	absolute, err := nativedb.NewTypeInfo(nativedb.TYPE_TIMESTAMP_TZ)
	if err != nil {
		return err
	}
	result, err := nativedb.NewTypeInfo(nativedb.TYPE_BOOLEAN)
	if err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return nativedb.RegisterScalarUDF(conn, nativeLocalTimestampFunction, &nativeLocalTimestampQualifier{location, wall, absolute, result})
}
