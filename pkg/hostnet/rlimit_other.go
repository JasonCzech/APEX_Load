//go:build !unix

package hostnet

// Windows has no RLIMIT_NOFILE; its handle limit is not a per-process
// setting an operator tunes for this purpose.
func openFileLimit() OpenFileLimit { return OpenFileLimit{} }
