package generatorrpc

import (
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestValidateWorkAssignmentTable(t *testing.T) {
	base := &pb.WorkAssignment{
		TestId:        "run",
		GroupName:     "group",
		ScriptId:      "script",
		ScriptContent: []byte(`{"name":"script"}`),
		VuserCount:    1,
	}
	tests := []struct {
		name        string
		mutate      func(*pb.WorkAssignment)
		allowSecret bool
		wantCode    codes.Code
		want        string
	}{
		{name: "valid defaults", allowSecret: true},
		{name: "missing test", mutate: func(request *pb.WorkAssignment) { request.TestId = "" }, allowSecret: true, wantCode: codes.InvalidArgument, want: "test_id is required"},
		{name: "negative users", mutate: func(request *pb.WorkAssignment) { request.VuserCount = -1 }, allowSecret: true, wantCode: codes.InvalidArgument, want: "must not be negative"},
		{name: "offset needs duration", mutate: func(request *pb.WorkAssignment) { request.DurationOffset = durationpb.New(time.Second) }, allowSecret: true, wantCode: codes.InvalidArgument, want: "requires a positive target_duration"},
		{name: "invalid init strategy", mutate: func(request *pb.WorkAssignment) { request.InitStrategy = "eager" }, allowSecret: true, wantCode: codes.InvalidArgument, want: "at_runtime or at_once"},
		{name: "ordinal needs shard count", mutate: func(request *pb.WorkAssignment) { request.ShardOrdinal = 1 }, allowSecret: true, wantCode: codes.InvalidArgument, want: "requires a positive shard_count"},
		{name: "ordinal outside shard count", mutate: func(request *pb.WorkAssignment) { request.ShardOrdinal, request.ShardCount = 2, 2 }, allowSecret: true, wantCode: codes.InvalidArgument, want: "smaller than shard_count"},
		{name: "secret needs secure transport", mutate: func(request *pb.WorkAssignment) {
			request.SecretBundle = map[string]string{"token": "secret"}
			request.SecretBundleId = "bundle"
		}, wantCode: codes.FailedPrecondition, want: "authenticated TLS"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := proto.Clone(base).(*pb.WorkAssignment)
			if test.mutate != nil {
				test.mutate(request)
			}
			validated, err := validateWorkAssignment(request, defaultGeneratorScriptMaxBytes, test.allowSecret)
			if test.wantCode != codes.OK {
				if status.Code(err) != test.wantCode || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("validation error = %v (%s), want %s containing %q", err, status.Code(err), test.wantCode, test.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if validated.request != request || validated.initStrategy != "at_runtime" || validated.shardCount != 1 {
				t.Fatalf("normalized assignment = %+v", validated)
			}
			if validated.fingerprint != workAssignmentFingerprint(request) {
				t.Fatal("validator fingerprint differs from request fingerprint")
			}
		})
	}
}

func TestValidateWorkAssignmentNormalizesPreciseTiming(t *testing.T) {
	request := &pb.WorkAssignment{
		TestId: "run", GroupName: "group", ScriptId: "script", VuserCount: 1,
		TargetDuration: durationpb.New(10 * time.Second),
		DurationOffset: durationpb.New(2 * time.Second),
		RampUp:         durationpb.New(3 * time.Second),
		RampDown:       durationpb.New(4 * time.Second),
		PreBaseline:    durationpb.New(time.Second),
		PostBaseline:   durationpb.New(5 * time.Second),
		InitStrategy:   " AT_ONCE ",
		ShardCount:     3,
	}
	validated, err := validateWorkAssignment(request, defaultGeneratorScriptMaxBytes, true)
	if err != nil {
		t.Fatal(err)
	}
	if validated.targetDuration != 10*time.Second || validated.logicalTargetDuration != 12*time.Second || validated.requestedDurationOffset != 2*time.Second ||
		validated.rampUp != 3*time.Second || validated.rampDown != 4*time.Second || validated.preBaseline != time.Second || validated.postBaseline != 5*time.Second ||
		validated.initStrategy != "at_once" || validated.shardCount != 3 {
		t.Fatalf("normalized timing = %+v", validated)
	}
}
