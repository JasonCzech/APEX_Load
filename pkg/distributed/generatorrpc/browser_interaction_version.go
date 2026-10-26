package generatorrpc

import (
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func validateBrowserInteractionVersion(request *pb.WorkAssignment) error {
	switch request.GetBrowserInteractionEvidenceVersion() {
	case 0, 1:
		return nil
	case 2, 3:
		if request.GetPrepareOnly() {
			return nil
		}
		return status.Errorf(codes.FailedPrecondition, "interaction evidence v%d requires traffic-free staging", request.GetBrowserInteractionEvidenceVersion())
	default:
		return status.Error(codes.InvalidArgument, "unsupported browser interaction evidence version")
	}
}

func assignedBrowserInteractionVersion(request *pb.WorkAssignment) int {
	if protocol.BrowserInteractionHasDispatch(int(request.GetBrowserInteractionEvidenceVersion())) {
		return int(request.GetBrowserInteractionEvidenceVersion())
	}
	return protocol.BrowserInteractionVersion
}

func (c *Client) observeBrowserInteractionVersion(pong *pb.HeartbeatPong) {
	if c.connection == nil {
		return
	}
	available := pong != nil && protocol.RuntimeFeaturesSatisfy(pong.CompatibleFeatures, FeatureBrowserInteractionEvidenceV2+"+"+FeaturePreparationV1)
	c.connection.mu.Lock()
	c.connection.browserDocumentProof = 0
	if pong != nil && protocol.RuntimeFeaturesSatisfy(pong.CompatibleFeatures, FeaturePreparationV1) {
		if protocol.RuntimeFeaturesSatisfy(pong.CompatibleFeatures, FeatureBrowserDocumentProofV1) {
			c.connection.browserDocumentProof = 1
		}
		if protocol.RuntimeFeaturesSatisfy(pong.CompatibleFeatures, FeatureBrowserDocumentProofV2) {
			c.connection.browserDocumentProof = 2
		}
		if protocol.RuntimeFeaturesSatisfy(pong.CompatibleFeatures, FeatureBrowserDocumentProofV3) {
			c.connection.browserDocumentProof = 3
		}
	}
	c.connection.browserOriginalSource = pong != nil && protocol.RuntimeFeaturesSatisfy(pong.CompatibleFeatures, FeatureBrowserOriginalSourceV1+"+"+FeaturePreparationV1)
	c.connection.browserNativeHAR = pong != nil && protocol.RuntimeFeaturesSatisfy(pong.CompatibleFeatures, FeatureBrowserNativeHARV1)
	c.connection.browserInteractionV2 = available
	c.connection.browserInteractionV3 = pong != nil && protocol.RuntimeFeaturesSatisfy(pong.CompatibleFeatures, FeatureBrowserInteractionEvidenceV3+"+"+FeatureBrowserInteractionEvidenceV2+"+"+FeaturePreparationV1)
	c.connection.browserInteractionObserved = time.Now()
	c.connection.mu.Unlock()
}

func (c *Client) browserNativeHARNegotiated() bool {
	if c.connection == nil {
		return false
	}
	c.connection.mu.Lock()
	defer c.connection.mu.Unlock()
	age := time.Since(c.connection.browserInteractionObserved)
	return c.connection.browserNativeHAR && age >= 0 && age <= 30*time.Second
}

func (c *Client) browserInteractionVersionNegotiated() bool {
	if c.connection == nil {
		return false
	}
	c.connection.mu.Lock()
	defer c.connection.mu.Unlock()
	age := time.Since(c.connection.browserInteractionObserved)
	return c.connection.browserInteractionV2 && age >= 0 && age <= 30*time.Second
}

func (c *Client) browserOutcomeClocksNegotiated() bool {
	if c.connection == nil {
		return false
	}
	c.connection.mu.Lock()
	defer c.connection.mu.Unlock()
	age := time.Since(c.connection.browserInteractionObserved)
	return c.connection.browserInteractionV3 && age >= 0 && age <= 30*time.Second
}
