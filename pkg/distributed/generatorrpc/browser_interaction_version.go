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
	case 2:
		if request.GetPrepareOnly() {
			return nil
		}
		return status.Error(codes.FailedPrecondition, "interaction evidence v2 requires traffic-free staging")
	default:
		return status.Error(codes.InvalidArgument, "unsupported browser interaction evidence version")
	}
}

func assignedBrowserInteractionVersion(request *pb.WorkAssignment) int {
	if request.GetBrowserInteractionEvidenceVersion() == 2 {
		return protocol.BrowserInteractionVersion2
	}
	return protocol.BrowserInteractionVersion
}

func (c *Client) observeBrowserInteractionVersion(pong *pb.HeartbeatPong) {
	if c.connection == nil {
		return
	}
	available := pong != nil && protocol.RuntimeFeaturesSatisfy(pong.CompatibleFeatures, FeatureBrowserInteractionEvidenceV2+"+"+FeaturePreparationV1)
	c.connection.mu.Lock()
	c.connection.browserInteractionV2 = available
	c.connection.browserInteractionObserved = time.Now()
	c.connection.mu.Unlock()
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
