package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

// restoreServerOwnedImportSecurityMetadata keeps importer-owned runtime
// artifacts, opaque repair payloads, and disclosure/redaction locators across
// an ordinary masked read-modify-write. They are deliberately visible (but
// value-free) in a disclosure projection so editors can round-trip them, yet
// clients must not be able to delete or rewrite them and thereby expose or
// destroy a raw artifact, repair source, parameter table, or retained request
// credential.
//
// Trusted importer updates bypass this helper and may replace the locator set
// with a newly projected source revision.
var restoreServerOwnedImportSecurityMetadata = importreview.RestoreServerOwnedImportSecurityMetadata

var cloneImportSecurityMetadata = importreview.CloneImportSecurityMetadata

// validatePreservedImportCredentialEdits permits ordinary edits to keep only
// credential values already present in the durable imported script. A client
// may return either the exact raw value it received at import time or the
// disclosure-safe stored-script projection. Introducing a new literal requires
// a trusted import commit/session or an explicit runtime binding.
var validatePreservedImportCredentialEdits = importreview.ValidatePreservedImportCredentialEdits

var hasNewProjectedCredential = importreview.HasNewProjectedCredential

// unsentStepReviewMetadata reports whether a step metadata entry is review
// output (a gate code, review flag or migration note) that the step does not
// send.
var unsentStepReviewMetadata = importreview.UnsentStepReviewMetadata

// retainedImportCredentialContextChange prevents a masked read-modify-write
// from turning an already-authorized imported credential into a bearer
// capability for a different destination or request. A credential restored
// from the prior durable script remains usable only in the request-shaped
// surface where it was imported and only while the shared transport context is
// unchanged.
//
// Credential material outside a single request (for example default auth,
// proxy auth, or a credential-bearing data source) is necessarily script-wide,
// so it is bound to every request context in the script. The comparison uses
// disclosure-safe projections exclusively; errors therefore never include a
// source credential.
var retainedImportCredentialContextChange = importreview.RetainedImportCredentialContextChange

// importTransportContext returns request-affecting script configuration while
// leaving ordinary scheduling, pacing, logging, and result-policy edits free
// to round-trip. Values come from maskScriptSecrets, so credential bytes never
// enter the context or an error.
var importTransportContext = importreview.ImportTransportContext

// importGlobalCredentialContext binds script-wide credentials to their data
// bindings and to every request-shaped surface that could consume them.
var importGlobalCredentialContext = importreview.ImportGlobalCredentialContext

// importActionContexts indexes every regular action and HTTP-sequence child by
// its structural DSL path. The action context contains outbound/request
// semantics, excluding authoring labels, response processing, review metadata,
// and timing-only controls.
var importActionContexts = importreview.ImportActionContexts

var importActionWireContext = importreview.ImportActionWireContext

var appendImportContextPath = importreview.AppendImportContextPath

var importContextDisplayPath = importreview.ImportContextDisplayPath

var stringJSONValue = importreview.StringJSONValue
