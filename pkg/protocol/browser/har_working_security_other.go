//go:build !darwin && !linux && !windows

package browser

import (
	"fmt"
	"os"
)

var errHARPrivacyUnqualified = fmt.Errorf("native HAR ownership is unavailable on this platform pending private ACL and durable publication qualification")

func validateHARPrivate(*os.File, bool) error { return errHARPrivacyUnqualified }

func persistentHARIdentityAt(*os.Root, string, os.FileInfo) (harPersistentIdentity, error) {
	return harPersistentIdentity{}, errHAROwnershipUnproven
}

func validHARPersistentIdentity(harPersistentIdentity) bool { return false }

func pinHARPathIdentity(_ string, info os.FileInfo) (os.FileInfo, error) { return info, nil }

func validateHARVolume(string) error { return errHARPrivacyUnqualified }

func createHARPrivateDir(*os.Root, string) error { return errHARPrivacyUnqualified }

func syncHARRoot(*os.Root) error { return errHARPrivacyUnqualified }

func renameHARNoReplace(*os.Root, string, string) error { return errHARPrivacyUnqualified }

func removeHAR(*os.Root, string) error { return errHARPrivacyUnqualified }

func replaceHAR(*os.Root, string, string) error { return errHARPrivacyUnqualified }

const harLeaseInheritableAcrossExec = false

func setHARLeaseInheritable(*os.File, bool) error { return errHARPrivacyUnqualified }
