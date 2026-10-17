package importreview

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/google/uuid"
	"strings"
)

func DirectImportOperationHash(projectID, sourceProfile, operationID string) string {
	return DirectImportSHA256(
		NormalizeProjectID(projectID),
		strings.TrimSpace(sourceProfile),
		strings.TrimSpace(operationID),
	)
}
func DirectImportSHA256(parts ...string) string {
	digest := sha256.New()
	for _, part := range parts {
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write([]byte(part))
	}
	return hex.EncodeToString(digest.Sum(nil))
}
func DeterministicDirectImportAssetID(operationHash, kind string, ordinal int) string {
	return uuid.NewSHA1(
		uuid.NameSpaceURL,
		[]byte(fmt.Sprintf("apexload:direct-import:%s:%s:%d", operationHash, kind, ordinal)),
	).String()
}
