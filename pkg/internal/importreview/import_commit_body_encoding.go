package importreview

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"strings"
)

type ImportCommitProjectionBody struct {
	stored string
	wire   []byte
	base64 bool
}

func DecodeImportCommitProjectionBody(
	ref dslwalk.SurfaceRef,
) (ImportCommitProjectionBody, bool, error) {
	stored, ok := ImportCommitSurfaceBody(ref)
	if !ok {
		return ImportCommitProjectionBody{}, false, nil
	}
	encoding := ImportCommitSurfaceBodyEncoding(ref)
	decoded, encoded, err := dsl.DecodeBodyEncoding(
		stored,
		encoding,
		credentialpolicy.DefaultLimits().MaxBytes,
	)
	if err != nil {
		// A trusted full-body template resolves to encoded data at runtime. It
		// is already governed as one value and cannot be decoded during commit.
		if encoded && dsl.IsTrustedFullBodyBase64Template(stored) {
			return ImportCommitProjectionBody{}, false, nil
		}
		return ImportCommitProjectionBody{}, false, fmt.Errorf(
			"decode request body at %s: %w",
			ref.Path,
			err,
		)
	}
	if !encoded {
		decoded = []byte(stored)
	}
	return ImportCommitProjectionBody{
		stored: stored,
		wire:   decoded,
		base64: encoded,
	}, true, nil
}

func (body ImportCommitProjectionBody) Text() string {
	return string(body.wire)
}

func (body ImportCommitProjectionBody) Encode(projected []byte) string {
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

func ImportCommitSurfaceBodyEncoding(ref dslwalk.SurfaceRef) string {
	if ref.Action != nil {
		return ref.Action.BodyEncoding
	}
	if ref.Request == nil {
		return ""
	}
	encoding, _ := ref.Request["body_encoding"].(string)
	return strings.TrimSpace(encoding)
}

// Wire returns the decoded bytes for this local projection value. Treat them as read-only.
func (body ImportCommitProjectionBody) Wire() []byte { return body.wire }
