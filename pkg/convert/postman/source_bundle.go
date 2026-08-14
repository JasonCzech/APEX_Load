package postman

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const ProtectedSourceBundleVersion = "apexload.postman-source-bundle.v1"

const (
	maxProtectedSourceBundleBytes = 64 << 20
	maxProtectedSourceFileBytes   = 32 << 20
	maxProtectedSourcePathBytes   = 4 << 10
)

// ProtectedSourceBundle is the lossless encrypted-retention representation
// used when an import has both a collection and a separately uploaded
// environment. []byte fields use JSON base64 encoding so arbitrary uploaded
// bytes round-trip without being interpreted or normalized.
type ProtectedSourceBundle struct {
	Version string                      `json:"version"`
	Files   []ProtectedSourceBundleFile `json:"files"`
}

type ProtectedSourceBundleFile struct {
	Role      string `json:"role"`
	Path      string `json:"path"`
	MediaType string `json:"media_type"`
	Content   []byte `json:"content"`
}

// BuildProtectedSourceBundle returns the original collection bytes unchanged
// when there is no environment, preserving compatibility with existing source
// downloads. With an environment, it returns a deterministic two-file bundle.
func BuildProtectedSourceBundle(collectionPath string, collection []byte, environmentPath string, environment []byte) []byte {
	if len(environment) == 0 {
		return append([]byte(nil), collection...)
	}
	usedPaths := make(map[string]struct{}, 2)
	collectionPath = allocateProtectedSourcePath(
		collectionPath,
		"postman_collection.json",
		usedPaths,
	)
	environmentPath = allocateProtectedSourcePath(
		environmentPath,
		"postman_environment.json",
		usedPaths,
	)
	bundle := ProtectedSourceBundle{
		Version: ProtectedSourceBundleVersion,
		Files: []ProtectedSourceBundleFile{
			{
				Role:      "collection",
				Path:      collectionPath,
				MediaType: "application/json",
				Content:   collection,
			},
			{
				Role:      "environment",
				Path:      environmentPath,
				MediaType: "application/json",
				Content:   environment,
			},
		},
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		// The structure contains only strings and byte slices, so encoding cannot
		// fail in practice. Retain both byte streams even under that impossible
		// condition rather than silently dropping the environment.
		fallback := append([]byte(nil), collection...)
		fallback = append(fallback, '\n')
		fallback = append(fallback, environment...)
		return fallback
	}
	return encoded
}

func allocateProtectedSourcePath(raw, fallback string, used map[string]struct{}) string {
	name := safePostmanSourceFileName(raw, fallback)
	if len(name) > maxProtectedSourcePathBytes {
		name = fallback
	}
	base := strings.TrimSuffix(name, pathExtension(name))
	extension := pathExtension(name)
	for suffix := 1; ; suffix++ {
		candidate := name
		if suffix > 1 {
			candidate = fmt.Sprintf("%s_%d%s", base, suffix, extension)
			if len(candidate) > maxProtectedSourcePathBytes {
				candidate = fmt.Sprintf("postman_source_%d.json", suffix)
			}
		}
		identity := strings.ToLower(candidate)
		if _, duplicate := used[identity]; duplicate {
			continue
		}
		used[identity] = struct{}{}
		return candidate
	}
}

func pathExtension(name string) string {
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		return name[dot:]
	}
	return ""
}

func ParseProtectedSourceBundle(data []byte) (ProtectedSourceBundle, error) {
	if len(data) == 0 {
		return ProtectedSourceBundle{}, errors.New("Postman source bundle is empty")
	}
	if len(data) > maxProtectedSourceBundleBytes {
		return ProtectedSourceBundle{}, fmt.Errorf(
			"Postman source bundle exceeds %d bytes",
			maxProtectedSourceBundleBytes,
		)
	}
	var bundle ProtectedSourceBundle
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return ProtectedSourceBundle{}, fmt.Errorf("invalid Postman source bundle: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ProtectedSourceBundle{}, errors.New("invalid Postman source bundle: trailing data")
	}
	if bundle.Version != ProtectedSourceBundleVersion {
		return ProtectedSourceBundle{}, fmt.Errorf("unsupported Postman source bundle version %q", bundle.Version)
	}
	if len(bundle.Files) != 2 {
		return ProtectedSourceBundle{}, errors.New("Postman source bundle must contain one collection and one environment")
	}
	seenRoles := make(map[string]struct{}, len(bundle.Files))
	seenPaths := make(map[string]struct{}, len(bundle.Files))
	for index := range bundle.Files {
		file := &bundle.Files[index]
		role := strings.ToLower(strings.TrimSpace(file.Role))
		if role != "collection" && role != "environment" {
			return ProtectedSourceBundle{}, fmt.Errorf("Postman source bundle file %d has unsupported role %q", index, file.Role)
		}
		if _, duplicate := seenRoles[role]; duplicate {
			return ProtectedSourceBundle{}, fmt.Errorf("Postman source bundle repeats %s role", role)
		}
		seenRoles[role] = struct{}{}
		file.Role = role
		if strings.TrimSpace(file.Path) == "" || len(file.Path) > maxProtectedSourcePathBytes ||
			file.Path != safePostmanSourceFileName(file.Path, "") {
			return ProtectedSourceBundle{}, fmt.Errorf("Postman source bundle file %d has an invalid path", index)
		}
		pathIdentity := strings.ToLower(file.Path)
		if _, duplicate := seenPaths[pathIdentity]; duplicate {
			return ProtectedSourceBundle{}, fmt.Errorf("Postman source bundle file %d repeats path %q", index, file.Path)
		}
		seenPaths[pathIdentity] = struct{}{}
		if file.MediaType != "application/json" {
			return ProtectedSourceBundle{}, fmt.Errorf("Postman source bundle file %d has unsupported media type %q", index, file.MediaType)
		}
		if len(file.Content) == 0 || len(file.Content) > maxProtectedSourceFileBytes {
			return ProtectedSourceBundle{}, fmt.Errorf("Postman source bundle file %d content size is invalid", index)
		}
	}
	if _, ok := seenRoles["collection"]; !ok {
		return ProtectedSourceBundle{}, errors.New("Postman source bundle has no collection")
	}
	if _, ok := seenRoles["environment"]; !ok {
		return ProtectedSourceBundle{}, errors.New("Postman source bundle has no environment")
	}
	return bundle, nil
}
