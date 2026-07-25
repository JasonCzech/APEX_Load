package javasidecar

import (
	"strings"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

// materializeClasspathAssets copies any user-uploaded classpath JARs into the
// runner work directory and rewrites the matching Classpath entries to their
// materialized absolute paths, so the subsequent resolveClasspath sees real
// files. Entries without an attached asset pass through unchanged and continue
// to fail closed in resolveClasspath if absent.
//
// The feature is inert unless the operator has opted in
// (ClasspathUploadEnabled): with it off, or with no assets attached, the
// original classpath is returned verbatim and behavior is identical to the
// pre-feature model. Failures use value-free codes because classpath values may
// contain resolved secrets/templates.
func (s *Server) materializeClasspathAssets(classpath []string, assets []ClasspathAssetRef, workDir string) ([]string, *safeFailure) {
	if len(assets) == 0 || !s.operator.ClasspathUploadEnabled {
		return classpath, nil
	}
	converted := make([]javarunner.ClasspathAsset, 0, len(assets))
	for _, asset := range assets {
		converted = append(converted, javarunner.ClasspathAsset{
			Entry:      strings.TrimSpace(asset.Entry),
			SHA256:     asset.SHA256,
			StorageRef: asset.StorageRef,
		})
	}
	rewritten, issue := javarunner.MaterializeClasspathAssets(classpath, converted, workDir)
	if issue == nil {
		return rewritten, nil
	}
	failure := &safeFailure{code: issue.Category}
	if issue.Index >= 0 {
		failure.index = intPointer(issue.Index)
	}
	return nil, failure
}
