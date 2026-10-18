//go:build linux && !race

package http

// pacWorkerDataLimit is the PAC worker's RLIMIT_DATA.
const pacWorkerDataLimit = 256 << 20
