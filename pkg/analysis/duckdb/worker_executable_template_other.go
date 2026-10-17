//go:build !darwin

package duckdb

// Only macOS assesses each new executable on its first exec, so other
// platforms keep copying the executable into each attempt.
func workerLinkedExecutableSnapshot(*workerAttempt, string, string) (string, bool, error) {
	return "", false, nil
}

// ReleaseNativeWorkerTemplate is a no-op where no template is kept.
func ReleaseNativeWorkerTemplate() {}
