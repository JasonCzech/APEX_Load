package api

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

func TestImportCommitProjectionBodyPreservesNoOpBase64Representation(t *testing.T) {
	wire := []byte(`{"safe":true}`)
	stored := "\r\n" + base64.StdEncoding.EncodeToString(wire) + "\n"
	action := &dsl.Action{Body: stored, BodyEncoding: " BASE64 "}
	body, ok, err := decodeImportCommitProjectionBody(dslwalk.SurfaceRef{
		Path:   "actions[0]",
		Action: action,
	})
	if err != nil || !ok {
		t.Fatalf("decodeImportCommitProjectionBody = %#v, %t, %v", body, ok, err)
	}
	if string(body.Wire()) != string(wire) {
		t.Fatalf("wire = %q, want %q", body.Wire(), wire)
	}
	if got := body.Encode(append([]byte(nil), wire...)); got != stored {
		t.Fatalf("no-op encoding = %q, want original %q", got, stored)
	}

	changedWire := []byte(`{"safe":false}`)
	changed := body.Encode(changedWire)
	decoded, err := base64.StdEncoding.DecodeString(changed)
	if err != nil || string(decoded) != string(changedWire) {
		t.Fatalf("changed encoding decodes to %q, %v; want %q", decoded, err, changedWire)
	}
}

func TestImportCommitProjectionBodyBoundsBase64Inspection(t *testing.T) {
	maxBytes := credentialpolicy.DefaultLimits().MaxBytes
	oversized := strings.Repeat("A", base64.StdEncoding.EncodedLen(maxBytes)+4)
	action := &dsl.Action{Body: oversized, BodyEncoding: "base64"}
	_, ok, err := decodeImportCommitProjectionBody(dslwalk.SurfaceRef{
		Path:   "actions[0]",
		Action: action,
	})
	if err == nil || ok || !errors.Is(err, dsl.ErrBodyEncodingLimit) {
		t.Fatalf("oversized decode = ok %t, err %v; want bounded failure", ok, err)
	}
}

func TestImportCommitProjectionBodyLeavesDeferredBase64TemplateIntact(t *testing.T) {
	action := &dsl.Action{Body: "{{secret.ENCODED_PAYLOAD}}", BodyEncoding: "base64"}
	_, ok, err := decodeImportCommitProjectionBody(dslwalk.SurfaceRef{
		Path:   "actions[0]",
		Action: action,
	})
	if err != nil || ok {
		t.Fatalf("deferred template decode = ok %t, err %v; want safe skip", ok, err)
	}
}
