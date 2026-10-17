// Package contentcontract holds wire metadata shared by content persistence and projections.
package contentcontract

// ImportSourceCommitFenceMetadataKey marks an imported candidate whose source
// publication has not yet completed. It is never part of a disclosure projection.
const ImportSourceCommitFenceMetadataKey = "protected_source_commit_fence_v1"
