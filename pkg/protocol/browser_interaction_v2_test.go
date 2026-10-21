package protocol

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func interactionV2Fixture() BrowserInteraction {
	i := interactionFixture()
	i.Version = BrowserInteractionVersion2
	i.Execution = &BrowserActionExecution{Invocation: 1, Attempt: 1}
	i.DocumentID = i.SourceGeneration + "-d1"
	i.AuthoredStep = NewBrowserAuthoredStep(strings.Repeat("c", 64), "actions[0]")
	i.Dispatch = &BrowserInteractionDispatch{At: time.UnixMilli(1700000000125).UTC(), TimeOriginMS: 1700000000000, OffsetMS: 125, DocumentID: i.DocumentID, LifecycleID: i.SourceGeneration + "-l2"}
	return i
}

func TestBrowserInteractionV2RetainsIndependentDispatchAndAuthoredIdentity(t *testing.T) {
	i := interactionV2Fixture()
	e := &BrowserInteractionEvidence{Version: 2, SourceGeneration: i.SourceGeneration, Interactions: []BrowserInteraction{i}}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBrowserInteractionEvidence(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := decoded.Interactions[0]
	if !got.Dispatch.At.Equal(i.Dispatch.At) || got.CapturedAt.Equal(got.Dispatch.At) || got.AuthoredStep.StepID != i.AuthoredStep.StepID {
		t.Fatal("dispatch, capture and authored identities were conflated")
	}
	clone := e.Clone()
	clone.Interactions[0].Dispatch.OffsetMS++
	clone.Interactions[0].Execution.Attempt++
	clone.Interactions[0].AuthoredStep.StepPath = "actions[1]"
	if e.Interactions[0].Execution.Attempt != 1 || e.Interactions[0].Dispatch.OffsetMS != 125 || !e.Interactions[0].AuthoredStep.Valid() {
		t.Fatal("clone aliases dispatch or identity")
	}
	legacy := interactionFixture()
	legacyRaw, err := json.Marshal(legacy)
	if err != nil || bytes.Contains(legacyRaw, []byte("dispatch")) || bytes.Contains(legacyRaw, []byte("authored_step")) {
		t.Fatalf("legacy shape changed: %s %v", legacyRaw, err)
	}
}

func TestBrowserInteractionV2RefusesInventedOrInconsistentDispatch(t *testing.T) {
	for name, mutate := range map[string]func(*BrowserInteraction){
		"invalid_attempt":             func(i *BrowserInteraction) { i.Execution.Attempt = 0 },
		"execution_without_step":      func(i *BrowserInteraction) { i.AuthoredStep = nil },
		"version":                     func(i *BrowserInteraction) { i.Version = 3 },
		"legacy_enrichment":           func(i *BrowserInteraction) { i.Version = 1 },
		"foreign_document":            func(i *BrowserInteraction) { i.Dispatch.DocumentID = strings.Repeat("d", 32) + "-d1" },
		"foreign_lifecycle":           func(i *BrowserInteraction) { i.Dispatch.LifecycleID = strings.Repeat("d", 32) + "-l1" },
		"wrong_document":              func(i *BrowserInteraction) { i.DocumentID = i.SourceGeneration + "-d2" },
		"invented_wall_clock":         func(i *BrowserInteraction) { i.Dispatch.At = i.CapturedAt },
		"nan_clock":                   func(i *BrowserInteraction) { i.Dispatch.OffsetMS = math.NaN() },
		"negative_clock":              func(i *BrowserInteraction) { i.Dispatch.OffsetMS = -1 },
		"missing_clock":               func(i *BrowserInteraction) { i.Dispatch.TimeOriginMS = 0 },
		"child_without_transform":     func(i *BrowserInteraction) { i.FrameScope = "child_frame" },
		"contradictory_availability":  func(i *BrowserInteraction) { i.DispatchUnavailableReason = "not_observed" },
		"missing_availability_reason": func(i *BrowserInteraction) { i.Dispatch = nil },
		"unbound_step":                func(i *BrowserInteraction) { i.AuthoredStep.StepID = strings.Repeat("0", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			i := interactionV2Fixture()
			mutate(&i)
			if i.Valid() {
				t.Fatal("accepted inconsistent v2 evidence")
			}
		})
	}
	for _, reason := range []string{"not_observed", InteractionDispatchClockUnavailable, InteractionDispatchDocumentUnavailable, InteractionDispatchActionUnavailable, InteractionUnavailablePageState, InteractionUnavailableChildFrame, InteractionUnavailableSyntheticDispatch, InteractionUnavailableDocumentChanged} {
		i := interactionV2Fixture()
		i.Dispatch = nil
		i.DispatchUnavailableReason = reason
		i.AuthoredStep = nil
		i.Execution = nil
		if !i.Valid() {
			t.Fatalf("unavailable %q refused", reason)
		}
	}
}

func TestBrowserInteractionDecoderRejectsTrailingOrMixedVersionRecords(t *testing.T) {
	for _, i := range []BrowserInteraction{interactionFixture(), interactionV2Fixture()} {
		e := &BrowserInteractionEvidence{Version: i.Version, SourceGeneration: i.SourceGeneration, Interactions: []BrowserInteraction{i}}
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{" {}", " null", " garbage"} {
			if _, err := DecodeBrowserInteractionEvidence(append(append([]byte(nil), raw...), []byte(suffix)...)); err == nil {
				t.Fatalf("trailing %q accepted", suffix)
			}
		}
		if _, err := DecodeBrowserInteractionEvidence(append(raw, []byte(" \n\t")...)); err != nil {
			t.Fatal(err)
		}
		e.Version = 3 - i.Version
		if e.Clone() != nil {
			t.Fatal("mixed envelope/row version accepted")
		}
	}
}
