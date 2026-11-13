package engine

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Evidence bounding keeps the authored-request markers the publication scrub set.
func TestBoundedSnapshotKeepsAuthoredRequestMarkers(t *testing.T) {
	bounded, _ := boundedErrorSnapshot(&protocol.ErrorSnapshot{
		AuthoredRequestRetained: true,
		RequestURL:              "https://api.example.test/login",
		NetworkEntries:          []protocol.NetworkEntry{{URL: "https://api.example.test/login", Method: "POST", AuthoredRequestRetained: true}},
	}, defaultErrorSnapshotMaxBytes)
	if bounded == nil || !bounded.AuthoredRequestRetained {
		t.Fatalf("snapshot marker lost: %+v", bounded)
	}
	if len(bounded.NetworkEntries) != 1 || !bounded.NetworkEntries[0].AuthoredRequestRetained {
		t.Fatalf("network entry marker lost: %+v", bounded.NetworkEntries)
	}
}
