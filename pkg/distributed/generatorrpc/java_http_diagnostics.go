package generatorrpc

import (
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func javaHTTPDiagnosticsToProto(d *protocol.JavaHTTPDiagnostics) *pb.JavaHTTPDiagnostics {
	if d == nil {
		return nil
	}
	if !d.Valid() {
		return &pb.JavaHTTPDiagnostics{}
	}
	d = d.Clone()
	return &pb.JavaHTTPDiagnostics{
		Version: uint32(d.Version), Mode: d.Mode, InputState: d.InputState,
		RequestEncodedBytes: d.RequestEncodedBytes, RequestDecodedBytes: d.RequestDecodedBytes,
		PreparedRequestBodyBytes: d.PreparedRequestBodyBytes, AppliedRequestCompression: d.AppliedRequestCompression,
		ResponseState: d.ResponseState, ResponseRetainedBytes: d.ResponseRetainedBytes, ResponseBase64Bytes: d.ResponseBase64Bytes,
		Base64DecodeMs: d.Base64DecodeMs, Base64EncodeMs: d.Base64EncodeMs, EnclosingMs: d.EnclosingMs,
		ObjectFootprint: d.ObjectFootprint,
	}
}

func javaHTTPDiagnosticsFromProto(p *pb.JavaHTTPDiagnostics) *protocol.JavaHTTPDiagnostics {
	if p == nil {
		return nil
	}
	if p.Version != 1 {
		return &protocol.JavaHTTPDiagnostics{}
	}
	d := &protocol.JavaHTTPDiagnostics{
		Version: 1, Mode: p.Mode, InputState: p.InputState,
		RequestEncodedBytes: p.RequestEncodedBytes, RequestDecodedBytes: p.RequestDecodedBytes,
		PreparedRequestBodyBytes: p.PreparedRequestBodyBytes, AppliedRequestCompression: p.AppliedRequestCompression,
		ResponseState: p.ResponseState, ResponseRetainedBytes: p.ResponseRetainedBytes, ResponseBase64Bytes: p.ResponseBase64Bytes,
		Base64DecodeMs: p.Base64DecodeMs, Base64EncodeMs: p.Base64EncodeMs, EnclosingMs: p.EnclosingMs,
		ObjectFootprint: p.ObjectFootprint,
	}
	return d.Retained()
}
