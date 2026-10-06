//go:build !darwin && !linux

package browser

import (
	"fmt"
	"os"
)

func validateHARPrivateInfo(_ os.FileInfo, _ bool) error {
	return fmt.Errorf("native HAR ownership is unavailable on this platform pending private ACL and durable publication qualification")
}

func persistentHARIdentity(_ os.FileInfo) (harPersistentIdentity, error) {
	return harPersistentIdentity{}, errHAROwnershipUnproven
}
