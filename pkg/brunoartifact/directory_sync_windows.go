//go:build windows

package brunoartifact

import "errors"

// errDirectorySyncUnsupported is deliberately format-neutral. This store began
// as Bruno-only, but it now backs the protected import sessions of every format
// that uses the shared commit service — OpenAPI and Postman included — so an
// operator importing a Postman collection on Windows must not be told that
// something about "Bruno" failed.
var errDirectorySyncUnsupported = errors.New(
	"durable protected-import-artifact directory synchronization is unsupported on Windows",
)

// Windows exposes directory handles, but FlushFileBuffers is documented for
// file handles and MoveFileEx's WRITE_THROUGH guarantee does not cover the
// hard-link and unlink operations this store also uses. Silently succeeding
// here could acknowledge a database receipt before its encrypted source
// object's namespace entry is durable. Fail closed until every namespace
// mutation has a documented write-through implementation.
func syncDirectory(string) error {
	return errDirectorySyncUnsupported
}
