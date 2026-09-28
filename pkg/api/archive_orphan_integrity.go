package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// Recovery must verify existing evidence before adjunct generation can compute
// new descriptors. All payload reads stay relative to the inspected directory.
func validateOrphanArchiveArtifacts(ctx context.Context, root *os.Root, manifest *RunArchiveManifest, total int64) error {
	names, err := materializationArtifactNames(manifest)
	if err != nil {
		return err
	}
	descriptors := make(map[string]RunArchiveArtifact, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		name, err := requiredArchiveArtifactName("artifacts.name", artifact.Name)
		if err != nil {
			return err
		}
		if _, ok := descriptors[name]; ok {
			return fmt.Errorf("duplicate orphan artifact %q", name)
		}
		descriptors[name] = artifact
	}
	limit := configuredArchiveMaterializationMaxBytes()
	if total > limit {
		return fmt.Errorf("orphan archive exceeds verification limit")
	}
	for _, name := range names {
		if name == runArchiveManifestFileName {
			continue
		}
		descriptor, ok := descriptors[name]
		digest, err := hex.DecodeString(descriptor.SHA256)
		if !ok || err != nil || len(digest) != sha256.Size || descriptor.SizeBytes < 0 || descriptor.SizeBytes > limit-total {
			return fmt.Errorf("orphan artifact %q lacks a bounded integrity descriptor", name)
		}
		file, info, err := openArchiveArtifactForReadAt(root, name)
		if err != nil {
			return err
		}
		if info.Size() != descriptor.SizeBytes {
			_ = file.Close()
			return fmt.Errorf("orphan artifact %q size mismatch", name)
		}
		hash := sha256.New()
		n, err := copyArchiveArtifactContext(ctx, hash, io.LimitReader(file, descriptor.SizeBytes+1))
		_ = file.Close()
		if err != nil {
			return err
		}
		if n != descriptor.SizeBytes || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), descriptor.SHA256) {
			return fmt.Errorf("orphan artifact %q hash mismatch", name)
		}
		total += n
	}
	return ctx.Err()
}
