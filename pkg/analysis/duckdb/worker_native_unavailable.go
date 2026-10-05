//go:build !analytics_duckdb || !cgo

package duckdb

func newWorkerNativeExecutor() (workerChildExecutor, error) { return nil, ErrUnavailable }

func workerNativeResourceError(error) bool { return false }
