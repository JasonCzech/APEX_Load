package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

// importCommitProjectionBody separates the durable DSL representation from
// the bytes the HTTP runtime places on the wire. Structured credential policy
// must inspect and rewrite Wire; callers restore the result through encode so
// body_encoding remains an execution detail rather than a policy bypass.
type importCommitProjectionBody = importreview.ImportCommitProjectionBody

var decodeImportCommitProjectionBody = importreview.DecodeImportCommitProjectionBody

var importCommitSurfaceBodyEncoding = importreview.ImportCommitSurfaceBodyEncoding
