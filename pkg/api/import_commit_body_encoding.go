package api

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

// importCommitProjectionBody separates the durable DSL representation from
// the bytes the HTTP runtime places on the wire. Structured credential policy
// must inspect and rewrite Wire; callers restore the result through encode so
// body_encoding remains an execution detail rather than a policy bypass.
type importCommitProjectionBody struct {
	stored string
	wire   []byte
	base64 bool
}

func decodeImportCommitProjectionBody(
	ref dslwalk.SurfaceRef,
) (importCommitProjectionBody, bool, error) {
	stored, ok := importCommitSurfaceBody(ref)
	if !ok {
		return importCommitProjectionBody{}, false, nil
	}
	encoding := importCommitSurfaceBodyEncoding(ref)
	decoded, encoded, err := dsl.DecodeBodyEncoding(
		stored,
		encoding,
		credentialpolicy.DefaultLimits().MaxBytes,
	)
	if err != nil {
		// A trusted full-body template resolves to encoded data at runtime. It
		// is already governed as one value and cannot be decoded during commit.
		if encoded && dsl.IsTrustedFullBodyBase64Template(stored) {
			return importCommitProjectionBody{}, false, nil
		}
		return importCommitProjectionBody{}, false, fmt.Errorf(
			"decode request body at %s: %w",
			ref.Path,
			err,
		)
	}
	if !encoded {
		decoded = []byte(stored)
	}
	return importCommitProjectionBody{
		stored: stored,
		wire:   decoded,
		base64: encoded,
	}, true, nil
}

func (body importCommitProjectionBody) text() string {
	return string(body.wire)
}

func (body importCommitProjectionBody) encode(projected []byte) string {
	if !body.base64 {
		return string(projected)
	}
	// A no-op projection must not canonicalize harmless wrapping or surrounding
	// whitespace in the authored base64 representation.
	if bytes.Equal(projected, body.wire) {
		return body.stored
	}
	return base64.StdEncoding.EncodeToString(projected)
}

func importCommitSurfaceBodyEncoding(ref dslwalk.SurfaceRef) string {
	if ref.Action != nil {
		return ref.Action.BodyEncoding
	}
	if ref.Request == nil {
		return ""
	}
	encoding, _ := ref.Request["body_encoding"].(string)
	return strings.TrimSpace(encoding)
}
