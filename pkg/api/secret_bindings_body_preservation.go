package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

// structuredImportBodyCanRemainExact distinguishes a disclosure-only whole-
// body mask from an unavailable credential leaf. The encoded-credential guard
// applies to credential scalars; a complete JSON/form document can exceed that
// guard because of ordinary payload fields. A bounded, byte-preserving codec
// must independently prove the exact imported body's credential fields before
// review or commit may retain it. This does not make the opaque whole body an
// available value for optional secret migration.
var structuredImportBodyCanRemainExact = importreview.StructuredImportBodyCanRemainExact

type structuredImportPayloadProof = importreview.StructuredImportPayloadProof

var exactStructuredImportPayload = importreview.ExactStructuredImportPayload

var exactStructuredImportJSONLeaves = importreview.ExactStructuredImportJSONLeaves

var exactStructuredImportScalar = importreview.ExactStructuredImportScalar

var credentialPreservingImportJSONNode = importreview.CredentialPreservingImportJSONNode

// A selected full-body secret reference changes the body from a structured
// document to one governed runtime value. Rebase only that body's retained
// locators to an opaque locator; other credentials retain their exact locators
// and malformed ordinary body edits still fail the engine's locator checks.
var reconcileWholeBodySecretBindingLocators = importreview.ReconcileWholeBodySecretBindingLocators

var reboundBodyTargetField = importreview.ReboundBodyTargetField
