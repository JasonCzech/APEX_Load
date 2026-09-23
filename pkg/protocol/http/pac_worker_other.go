//go:build !linux

package http

// The statically checked PAC language profile bounds allocations on hosts that
// do not expose Linux's anonymous-heap RLIMIT_DATA enforcement. The parent also
// isolates execution and kills the worker at the deadline.
func limitPACWorkerMemory() error { return nil }
