//go:build !(darwin || linux)

package api

import "time"

// The reference workloads' process usage is not measured on this platform.

func referenceCPUSeconds() (float64, bool) { return 0, false }

type referenceRSSSampler struct{}

func startReferenceRSSSampler(time.Duration) *referenceRSSSampler { return &referenceRSSSampler{} }

func (*referenceRSSSampler) stop() (self, descendants, combined int64, ok bool) {
	return 0, 0, 0, false
}
