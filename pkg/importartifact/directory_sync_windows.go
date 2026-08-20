//go:build windows

package importartifact

// Windows namespace durability is supplied exclusively by the write-through
// mutation primitives in durable_namespace_windows.go. Revalidate the volume
// policy at the common sync boundary so a path redirected to remote or
// unsupported storage fails closed; an existence check is not treated as a
// durability operation.
func syncDirectory(path string) error {
	return validateDurableFilesystem(path)
}
