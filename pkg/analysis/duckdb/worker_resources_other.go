//go:build !linux

package duckdb

func workerContainerHeadroom(int64) error { return nil } // host-only observed-memory profile
