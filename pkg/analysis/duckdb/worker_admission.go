package duckdb

import (
	"context"
	"errors"
	"sync"
)

var errWorkerResourceLimit = errors.New("native analytics resource limit")

// One coherent reservation covers staging, process/reap, durable output copy
// and final publication reconciliation. Comparisons must acquire once and run
// sequential child operations; never nest a second reservation while holding it.
type workerAdmissionController struct {
	gate  chan struct{}
	probe func(workerBootstrap) error
}

func newWorkerAdmissionController(probe func(workerBootstrap) error) *workerAdmissionController {
	return &workerAdmissionController{gate: make(chan struct{}, 1), probe: probe}
}
func (a *workerAdmissionController) reserve(ctx context.Context, b workerBootstrap) (func(), error) {
	if a == nil || a.probe == nil || ctx == nil || b.validate() != nil {
		return nil, errWorkerProtocol
	}
	if err := workerContextError(ctx); err != nil {
		return nil, err
	}
	select {
	case a.gate <- struct{}{}:
	default:
		return nil, ErrBusy
	}
	release := sync.OnceFunc(func() { <-a.gate })
	if err := workerContextError(ctx); err != nil {
		release()
		return nil, err
	}
	if err := a.probe(b); err != nil {
		release()
		return nil, errWorkerResourceLimit
	}
	if err := workerContextError(ctx); err != nil {
		release()
		return nil, err
	}
	return release, nil
}
