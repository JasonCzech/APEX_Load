//go:build linux && race

package http

// pacWorkerDataLimit is the PAC worker's RLIMIT_DATA. Only tests run a
// race-instrumented binary as the worker, and its runtime allocates past the
// production 256 MiB bound, so it keeps a bound with room for the detector.
const pacWorkerDataLimit = 2 << 30
