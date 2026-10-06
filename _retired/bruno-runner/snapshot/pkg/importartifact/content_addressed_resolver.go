package importartifact

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"strings"
)

// ContentAddressedResolver opens sanitized Bruno bundles from a shared
// encrypted artifact root using only the project and content digest carried by
// the canonical script. It is intended for generator pods mounting the same
// immutable object namespace as the controller; artifact IDs are never treated
// as filesystem paths.
type ContentAddressedResolver struct {
	Objects *EncryptedStore
	Kind    string
}

func (r *ContentAddressedResolver) OpenBrunoArtifact(ctx context.Context, request Request) (io.ReadCloser, int64, error) {
	if r == nil || r.Objects == nil {
		return nil, 0, errors.New("Bruno content-addressed artifact resolver is not configured")
	}
	projectID := strings.TrimSpace(request.ProjectID)
	digest := strings.ToLower(strings.TrimSpace(request.SHA256))
	kind := strings.ToLower(strings.TrimSpace(r.Kind))
	if kind == "" {
		kind = "sanitized"
	}
	if projectID == "" {
		return nil, 0, errors.New("Bruno artifact project is required")
	}
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != 32 {
		return nil, 0, errors.New("Bruno artifact descriptor requires a SHA-256 digest")
	}
	if !validArtifactKind(kind) {
		return nil, 0, errors.New("Bruno artifact kind is invalid")
	}
	return r.Objects.Open(ctx, OpenRequest{
		BackendKey: artifactBackendKey(projectID, kind, digest),
		ProjectID:  projectID,
		Kind:       kind,
		SHA256:     digest,
	})
}

func (r *ContentAddressedResolver) ResolveBrunoArtifact(ctx context.Context, request Request) ([]byte, error) {
	reader, _, err := r.OpenBrunoArtifact(ctx, request)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}
