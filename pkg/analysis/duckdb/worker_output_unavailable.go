//go:build !analytics_duckdb || !cgo

package duckdb

import "io"

func (*Engine) OpenOutput(string) (io.ReadCloser, error) { return nil, ErrUnavailable }
func (*Engine) ReleaseOutputs() error                    { return ErrUnavailable }
