package generatorrpc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// ArchiveSourceIdentity binds retained source files to the original supervised
// assignment. It is a domain-separated digest, not a reusable execution credential.
// Adoption keeps a writer's original identity even as supervision tokens rotate.
func ArchiveSourceIdentity(testID string, token []byte, target string) string {
	if validateArchiveTestID(testID) != nil || !validRunSupervisionToken(token) || !validArchiveSourceTarget(target) {
		return ""
	}
	h := sha256.New()
	_, _ = h.Write([]byte("apexload.generator.archive.v1\x00"))
	_, _ = h.Write([]byte(testID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(token)
	_, _ = h.Write([]byte(target))
	return hex.EncodeToString(h.Sum(nil))
}

func validArchiveSourceIdentity(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// The caller holds a retained catalog lease and the stable run directory. Read
// the identity from that same directory before opening any requested artifact.
func verifyArchiveSourceIdentity(root *os.Root, testID, expected string) error {
	f, _, err := openGeneratorArchiveRegularFile(root, "manifest.json")
	if err != nil {
		return err
	}
	defer f.Close()
	const maximum = 8 << 20
	data, err := io.ReadAll(io.LimitReader(f, maximum+1))
	if err != nil {
		return err
	}
	if len(data) > maximum {
		return fmt.Errorf("archive manifest exceeds identity verification limit")
	}
	var proof struct {
		TestID         string `json:"test_id"`
		SourceIdentity string `json:"source_identity"`
	}
	if err := json.Unmarshal(data, &proof); err != nil {
		return err
	}
	if proof.TestID != testID || proof.SourceIdentity != expected {
		return fmt.Errorf("archive source identity does not match the requested assignment")
	}
	return nil
}

func validArchiveSourceTarget(target string) bool {
	return target != "" && strings.TrimSpace(target) == target && len(target) <= 1024 && !strings.ContainsAny(target, "\x00\r\n")
}
