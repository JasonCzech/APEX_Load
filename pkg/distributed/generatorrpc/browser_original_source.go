package generatorrpc

import (
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const FeatureBrowserOriginalSourceV1 = "browser_original_source_v1"

func browserAuthoredDefinitionFromProto(source *pb.BrowserAuthoredDefinition) protocol.BrowserAuthoredDefinition {
	if source == nil {
		return protocol.BrowserAuthoredDefinition{}
	}
	return protocol.BrowserAuthoredDefinition{Version: int(source.Version), ScriptSHA256: source.ScriptSha256, StructureSHA256: source.StructureSha256}
}
func browserAuthoredDefinitionToProto(source protocol.BrowserAuthoredDefinition) *pb.BrowserAuthoredDefinition {
	if source == (protocol.BrowserAuthoredDefinition{}) {
		return nil
	}
	return &pb.BrowserAuthoredDefinition{Version: uint32(source.Version), ScriptSha256: source.ScriptSHA256, StructureSha256: source.StructureSHA256}
}
func validateBrowserOriginalSource(request *pb.WorkAssignment) error {
	if request.GetBrowserAuthoredDefinition() == nil {
		return nil
	}
	if !browserAuthoredDefinitionFromProto(request.BrowserAuthoredDefinition).Valid() {
		return status.Error(codes.InvalidArgument, "invalid browser original source definition")
	}
	if !request.PrepareOnly {
		return status.Error(codes.FailedPrecondition, "browser original source requires traffic-free staging")
	}
	return nil
}
func (c *Client) browserOriginalSourceNegotiated() bool {
	if c.connection == nil {
		return false
	}
	c.connection.mu.Lock()
	defer c.connection.mu.Unlock()
	age := time.Since(c.connection.browserInteractionObserved)
	return c.connection.browserOriginalSource && age >= 0 && age <= 30*time.Second
}
