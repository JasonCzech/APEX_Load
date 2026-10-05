// Package rca is the pure root-cause analysis engine. It reads frozen run
// evidence that adapters have already loaded and returns verdicts, so it
// imports no storage, provider or HTTP package (dependency_test.go enforces
// this). Adapters live in pkg/api/rca_*.go.
package rca
