package importartifact

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

type ArtifactMetadataReader interface {
	GetImportSourceArtifact(id string, projectID string) (*storage.ImportSourceArtifact, error)
}

type StoreResolver struct {
	Objects      *EncryptedStore
	Metadata     ArtifactMetadataReader
	MaxReadBytes int64
}

func (r *StoreResolver) OpenBrunoArtifact(ctx context.Context, request Request) (io.ReadCloser, int64, error) {
	if r == nil || r.Objects == nil || r.Metadata == nil {
		return nil, 0, errors.New("Bruno artifact store resolver is not configured")
	}
	artifactID := strings.TrimPrefix(strings.TrimSpace(request.Reference), "artifact://")
	if artifactID == "" || artifactID == request.Reference && strings.Contains(artifactID, "://") {
		return nil, 0, errors.New("invalid Bruno artifact reference")
	}
	projectID := strings.TrimSpace(request.ProjectID)
	if projectID == "" {
		return nil, 0, errors.New("Bruno artifact project is required")
	}
	expectedDigest := strings.ToLower(strings.TrimSpace(request.SHA256))
	decodedDigest, err := hex.DecodeString(expectedDigest)
	if err != nil || len(decodedDigest) != 32 {
		return nil, 0, errors.New("Bruno artifact descriptor requires a SHA-256 digest")
	}
	artifact, err := r.Metadata.GetImportSourceArtifact(artifactID, projectID)
	if err != nil {
		return nil, 0, err
	}
	if artifact.LifecycleState != "active" {
		return nil, 0, fmt.Errorf("Bruno artifact %s is %s", artifact.ID, artifact.LifecycleState)
	}
	if !strings.EqualFold(strings.TrimSpace(artifact.ArtifactKind), "sanitized") {
		return nil, 0, errors.New("Bruno executable artifact must be a sanitized source bundle")
	}
	if !strings.EqualFold(strings.TrimSpace(artifact.SHA256), expectedDigest) {
		return nil, 0, errors.New("Bruno artifact descriptor digest does not match stored metadata")
	}
	return r.Objects.Open(ctx, OpenRequest{
		BackendKey: artifact.BackendKey,
		ProjectID:  projectID,
		Kind:       artifact.ArtifactKind,
		SHA256:     artifact.SHA256,
		SizeBytes:  artifact.SizeBytes,
	})
}

func (r *StoreResolver) ResolveBrunoArtifact(ctx context.Context, request Request) ([]byte, error) {
	reader, size, err := r.OpenBrunoArtifact(ctx, request)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	maximum := r.MaxReadBytes
	if maximum <= 0 {
		maximum = 1 << 30
	}
	if size > maximum {
		return nil, fmt.Errorf("Bruno artifact size %d exceeds in-memory resolver limit %d", size, maximum)
	}
	return io.ReadAll(io.LimitReader(reader, maximum+1))
}
