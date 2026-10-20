package generatorrpc

import (
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func archiveJavaHTTPFromBatch(batch *pb.MetricsBatch) *protocol.JavaHTTPActions {
	if batch == nil {
		return nil
	}
	var out *protocol.JavaHTTPActions
	for _, a := range batch.ActionMetrics {
		if a != nil {
			protocol.AddJavaHTTPAction(&out, a.Name, a.Count, javaHTTPSummaryFromProto(a.JavaHttp))
		}
	}
	return out
}
