//go:build linux || darwin || windows

package duckdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Run two real helper children sequentially under one retained parent. This
// tests process/reap/descriptor ownership, not DuckDB performance or analytics.
func TestWorkerComparisonChildrenReuseOneReservationWithoutEarlyRelease(t *testing.T) {
	for _, secondMode := range []string{"preview", "invalid-frame"} {
		t.Run(secondMode, func(t *testing.T) {
			p, released := workerPipelineTestFixture(t)
			b := workerRuntimeTestBootstrap(t)
			p.limits = b.Limits
			ctx := context.WithValue(p.ctx, nativePipelineKey{}, p)
			defer p.Release()
			parent := t.TempDir()
			source, err := os.Create(filepath.Join(t.TempDir(), "source.parquet"))
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			if _, err := source.Write(make([]byte, 100)); err != nil {
				t.Fatal(err)
			}
			for side, mode := range []string{"preview", secondMode} {
				b.Identity.AttemptID = fmt.Sprintf("%032x", side+1)
				a, borrowed, err := workerPipelineAttempt(ctx, parent, b)
				if err != nil || borrowed != p || a.privatePipeline != nil {
					t.Fatal("side acquired a replacement parent", side, borrowed, err)
				}
				if err := a.stageInput(b.Handles[0].Handle, source); err != nil {
					_ = workerFinishAttempt(a)
					t.Fatal(err)
				}
				request := workerRequest{Preview: &workerPreviewOperation{InputHandle: b.Handles[0].Handle}}
				result, err := runWorkerParent(a, request, workerRunOptions{probe: func(*workerAttempt, *os.Process) error { return nil }, testLaunch: workerRuntimeTestLaunch(t, a, mode)})
				if mode == "preview" {
					if err != nil {
						t.Fatal("real child query failed", side, err)
					}
					if err := result.release(); err != nil {
						t.Fatal("actual child output/reap did not join", err)
					}
				} else {
					if err == nil {
						_ = result.release()
						t.Fatal("malformed second child accepted")
					}
					if err := workerFinishAttempt(a); err != nil {
						t.Fatal("failed child did not physically join", err)
					}
				}
				if released.Load() != 0 || !p.WritersQuiescent() {
					t.Fatal("per-side completion released enclosing comparison", side, released.Load())
				}
			}
			if err := p.Release(); err != nil || released.Load() != 1 {
				t.Fatal("comparison did not release once after both physical children", err)
			}
		})
	}
}
