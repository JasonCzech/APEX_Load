package generatorrpc

import (
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"time"
)

const FeatureBrowserDocumentProofV1 = "browser_document_proof_v1"

func validateBrowserDocumentProofVersion(request *pb.WorkAssignment) error {
	switch request.GetBrowserDocumentProofVersion() {
	case 0:
		return nil
	case 1:
		if request.GetPrepareOnly() {
			return nil
		}
		return status.Error(codes.FailedPrecondition, "browser proof requires traffic-free staging")
	default:
		return status.Error(codes.InvalidArgument, "unsupported browser document proof version")
	}
}
func assignedBrowserDocumentProofVersion(request *pb.WorkAssignment) int {
	if request.GetBrowserDocumentProofVersion() == 1 {
		return 1
	}
	// Zero means the local default in engine configuration. A legacy remote
	// controller must explicitly suppress the additive sidecar on a new peer.
	return -1
}
func (c *Client) browserDocumentProofNegotiated() bool {
	if c.connection == nil {
		return false
	}
	c.connection.mu.Lock()
	defer c.connection.mu.Unlock()
	age := time.Since(c.connection.browserInteractionObserved)
	return c.connection.browserDocumentProof && age >= 0 && age <= 30*time.Second
}

func validateBrowserDocumentProofRuntime(script *dsl.Script, instance protocol.Protocol) error {
	if !dsl.BrowserDocumentCaptureRequested(script) {
		return status.Error(codes.InvalidArgument, "browser document proof requires browser document capture")
	}
	_, receipt := instance.(protocol.BrowserDocumentReceiptEvidenceProvider)
	_, document := instance.(protocol.BrowserDocumentEvidenceProvider)
	_, wiring := instance.(interface{ BrowserDocumentReceiptTransportVersion() int })
	if !receipt || !document || !wiring {
		return status.Error(codes.FailedPrecondition, "runtime does not support browser document receipt transport")
	}
	return nil
}
