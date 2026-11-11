package storage

import "github.com/jasonczech/apexload/pkg/dsl"

// validateImportBatchCredentialSafety checks basic non-nil script presence for
// atomic import batch commits. Under the unredacted import policy, imported
// scripts retain literal credentials and metadata directly in durable storage.
func validateImportBatchCredentialSafety(
	script *dsl.Script,
	trustPreservedExecutableCredentials bool,
) error {
	_ = trustPreservedExecutableCredentials
	if script == nil {
		return ErrImportBatchCredentialsUnmanaged
	}
	return nil
}
