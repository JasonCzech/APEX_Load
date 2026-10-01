package generatorrpc

import (
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func grpcDiagnosticsToProto(d *protocol.GRPCDiagnostics) *pb.GRPCDiagnostics {
	if !d.Valid() {
		return nil
	}
	d = d.Clone()
	return &pb.GRPCDiagnostics{
		Method: d.Method, Mode: d.Mode, CallMs: d.CallMs, FirstMessageMs: d.FirstMessageMs,
		DeadlineBudgetMs: d.DeadlineBudgetMs, StatusCode: d.StatusCode, EncodeMs: d.EncodeMs, DecodeMs: d.DecodeMs,
		MessagesSent: d.MessagesSent, MessagesReceived: d.MessagesReceived,
		RequestBytes: d.RequestBytes, ResponseBytes: d.ResponseBytes, ReceiveLimitReached: d.ReceiveLimitReached,
	}
}

func grpcDiagnosticsFromProto(p *pb.GRPCDiagnostics) *protocol.GRPCDiagnostics {
	if p == nil {
		return nil
	}
	d := &protocol.GRPCDiagnostics{
		Method: p.Method, Mode: p.Mode, CallMs: p.CallMs, FirstMessageMs: p.FirstMessageMs,
		DeadlineBudgetMs: p.DeadlineBudgetMs, StatusCode: p.StatusCode, EncodeMs: p.EncodeMs, DecodeMs: p.DecodeMs,
		MessagesSent: p.MessagesSent, MessagesReceived: p.MessagesReceived,
		RequestBytes: p.RequestBytes, ResponseBytes: p.ResponseBytes, ReceiveLimitReached: p.ReceiveLimitReached,
	}
	if !d.Valid() {
		return nil
	}
	return d.Clone()
}
