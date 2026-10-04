//go:build linux

package duckdb

// Opening the kernel executable link retains the exact image that exec mapped,
// including after the installer unlinks or replaces its old pathname. Do not
// resolve this link into a pathname first: os.Executable trims " (deleted)" and
// reopening that result can instead read a replacement installed in its place.
// A missing/unavailable procfs executable link fails closed at the actual Open.
func workerRunningExecutableSourcePath() (string, error) {
	return "/proc/self/exe", nil
}
