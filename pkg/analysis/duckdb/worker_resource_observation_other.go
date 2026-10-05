//go:build !linux && !darwin && !windows

package duckdb

func workerPlatformProcessAbsent(error) bool { return false }
