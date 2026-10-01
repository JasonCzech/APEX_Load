package generatorrpc

import (
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const FeatureWebSocketDiagnosticsV1 = "websocket_diagnostics_v1"

func websocketDiagnosticsToProto(d *protocol.WebSocketDiagnostics) *pb.WebSocketDiagnostics {
	if !d.Valid() {
		return nil
	}
	d = d.Clone()
	return &pb.WebSocketDiagnostics{Operation: d.Operation, MessageType: d.MessageType, HandshakeMs: d.HandshakeMs, ReconnectMs: d.ReconnectMs, SendMs: d.SendMs, ReceiveMs: d.ReceiveMs, WriteWaitMs: d.WriteWaitMs, PendingWrites: d.PendingWrites, Reconnects: d.Reconnects, MessagesSent: d.MessagesSent, MessagesReceived: d.MessagesReceived, BytesSent: d.BytesSent, BytesReceived: d.BytesReceived, PingRttMs: d.PingRTTMs, CloseCode: d.CloseCode, DisconnectReason: d.DisconnectReason, CallbackMode: d.CallbackMode}
}

func websocketDiagnosticsFromProto(p *pb.WebSocketDiagnostics) *protocol.WebSocketDiagnostics {
	if p == nil {
		return nil
	}
	d := &protocol.WebSocketDiagnostics{Operation: p.Operation, MessageType: p.MessageType, HandshakeMs: p.HandshakeMs, ReconnectMs: p.ReconnectMs, SendMs: p.SendMs, ReceiveMs: p.ReceiveMs, WriteWaitMs: p.WriteWaitMs, PendingWrites: p.PendingWrites, Reconnects: p.Reconnects, MessagesSent: p.MessagesSent, MessagesReceived: p.MessagesReceived, BytesSent: p.BytesSent, BytesReceived: p.BytesReceived, PingRTTMs: p.PingRttMs, CloseCode: p.CloseCode, DisconnectReason: p.DisconnectReason, CallbackMode: p.CallbackMode}
	if !d.Valid() {
		return nil
	}
	return d.Clone()
}
