package api

import (
	"testing"
	"time"
)

func TestJointPendingOversizeRefusalPreservesEarlierWindows(t *testing.T) {
	old := maxPendingJointBytes
	t.Cleanup(func() { maxPendingJointBytes = old })
	at := time.Now().UTC()
	small := jointBatch(1, at, jointAggregate(jointKey("login", "east", "3g"), 10))
	size := jointPublicationBytes(jointPublicationEvidence("gen-a", small, jointTestProjection))
	maxPendingJointBytes = size
	run := &ManagedTest{JointEvidence: jointTestProjection.Clone()}
	rememberJointPublicationLocked(run, "gen-a", small)
	if len(run.pendingJointPublications) != 1 || run.pendingJointBytes != size {
		t.Fatal("exact byte boundary refused")
	}
	large := jointBatch(2, at, jointAggregate(jointKey("login", "east", "3g"), 10), jointAggregate(jointKey("login", "west", "wifi"), 20))
	rememberJointPublicationLocked(run, "gen-a", large)
	if len(run.pendingJointPublications) != 1 || run.pendingJointPublications[0].Sequence != 1 || run.pendingJointBytes != size || run.jointPublicationsDropped != 1 {
		t.Fatal("oversized batch evicted useful evidence or escaped budget", len(run.pendingJointPublications), run.pendingJointBytes, run.jointPublicationsDropped)
	}
	maxPendingJointBytes = size - 1
	empty := &ManagedTest{JointEvidence: jointTestProjection.Clone()}
	rememberJointPublicationLocked(empty, "gen-a", small)
	if len(empty.pendingJointPublications) != 0 || empty.pendingJointBytes != 0 || empty.jointPublicationsDropped != 1 {
		t.Fatal("one oversized batch escaped empty queue", empty.pendingJointBytes)
	}
	if len(takePendingJointPublicationsLocked(run, nil)) != 1 || run.pendingJointBytes != 0 || run.jointPublicationsDropped != 1 {
		t.Fatal("refusal coverage disappeared when taking retained windows")
	}
}
