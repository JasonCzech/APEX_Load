package collectoragent

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func kafkaCodecTestI16(p []byte, value int16) []byte {
	return binary.BigEndian.AppendUint16(p, uint16(value))
}
func kafkaCodecTestI32(p []byte, value int32) []byte {
	return binary.BigEndian.AppendUint32(p, uint32(value))
}
func kafkaCodecTestI64(p []byte, value int64) []byte {
	return binary.BigEndian.AppendUint64(p, uint64(value))
}
func kafkaCodecTestUvarint(p []byte, value uint32) []byte {
	return binary.AppendUvarint(p, uint64(value))
}
func kafkaCodecTestString(p []byte, value string, compact bool) []byte {
	if compact {
		p = kafkaCodecTestUvarint(p, uint32(len(value)+1))
	} else {
		p = kafkaCodecTestI16(p, int16(len(value)))
	}
	return append(p, value...)
}
func kafkaCodecTestReason(t *testing.T, err error, reason string) {
	t.Helper()
	var typed *managementObservationError
	if !errors.As(err, &typed) || typed.reason != reason {
		t.Fatalf("wanted bounded reason %q, got %v", reason, err)
	}
}
func kafkaCodecTestTopic() []kafkaManagementTopicRequest {
	return []kafkaManagementTopicRequest{{Name: "orders", Partitions: []kafkaManagementPartitionRequest{{Partition: 0, CurrentLeaderEpoch: 7}, {Partition: 2, CurrentLeaderEpoch: -1}}}}
}

func TestKafkaManagementRequestWireContracts(t *testing.T) {
	const correlation int32 = 73
	tests := []struct {
		name    string
		request kafkaManagementRequest
		body    []byte
	}{
		{"api-versions-0", kafkaManagementRequest{Operation: kafkaManagementAPIVersions, Version: 0}, nil},
		{"sasl-handshake-1", kafkaManagementRequest{Operation: kafkaManagementSASLHandshake, Version: 1, Mechanism: "PLAIN"}, kafkaCodecTestString(nil, "PLAIN", false)},
		{"sasl-authenticate-1", kafkaManagementRequest{Operation: kafkaManagementSASLAuthenticate, Version: 1, AuthBytes: []byte{0, 1, 2}}, append(kafkaCodecTestI32(nil, 3), 0, 1, 2)},
		{"find-coordinator-2", kafkaManagementRequest{Operation: kafkaManagementFindCoordinator, Version: 2, Group: "approved-group"}, append(kafkaCodecTestString(nil, "approved-group", false), 0)},
	}
	for _, version := range []int16{8, 12} {
		request := kafkaManagementRequest{Operation: kafkaManagementMetadataOperation, Version: version, Topics: []kafkaManagementTopicRequest{{Name: "orders"}}}
		var body []byte
		if version == 8 {
			body = kafkaCodecTestI32(body, 1)
			body = kafkaCodecTestString(body, "orders", false)
			body = append(body, 0, 0, 0) // auto-create and both ACL flags disabled.
		} else {
			body = append(body, 2) // One explicit topic, never nullable/all topics.
			body = append(body, make([]byte, 16)...)
			body = kafkaCodecTestString(body, "orders", true)
			body = append(body, 0, 0, 0, 0) // topic tags, auto-create, topic ACL, request tags.
		}
		name := "metadata-8"
		if version == 12 {
			name = "metadata-12"
		}
		tests = append(tests, struct {
			name    string
			request kafkaManagementRequest
			body    []byte
		}{name, request, body})
	}
	offsetFetch := kafkaManagementRequest{Operation: kafkaManagementOffsetFetch, Version: 7, Group: "approved-group", Topics: kafkaCodecTestTopic()}
	body := kafkaCodecTestString(nil, offsetFetch.Group, true)
	body = append(body, 2)
	body = kafkaCodecTestString(body, "orders", true)
	body = append(body, 3)
	body = kafkaCodecTestI32(body, 0)
	body = kafkaCodecTestI32(body, 2)
	body = append(body, 0, 1, 0) // topic tags, RequireStable, request tags.
	tests = append(tests, struct {
		name    string
		request kafkaManagementRequest
		body    []byte
	}{"offset-fetch-7", offsetFetch, body})
	for _, isolation := range []int8{0, 1} {
		request := kafkaManagementRequest{Operation: kafkaManagementListOffsets, Version: 5, Topics: kafkaCodecTestTopic(), Isolation: isolation}
		body := kafkaCodecTestI32(nil, -1)
		body = append(body, byte(isolation))
		body = kafkaCodecTestI32(body, 1)
		body = kafkaCodecTestString(body, "orders", false)
		body = kafkaCodecTestI32(body, 2)
		for _, partition := range request.Topics[0].Partitions {
			body = kafkaCodecTestI32(body, partition.Partition)
			body = kafkaCodecTestI32(body, partition.CurrentLeaderEpoch)
			body = kafkaCodecTestI64(body, -1)
		}
		name := "list-offsets-high-watermark"
		if isolation == 1 {
			name = "list-offsets-last-stable"
		}
		tests = append(tests, struct {
			name    string
			request kafkaManagementRequest
			body    []byte
		}{name, request, body})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := encodeKafkaManagementRequest(test.request, correlation)
			if err != nil {
				t.Fatal(err)
			}
			want := make([]byte, 4)
			want = kafkaCodecTestI16(want, int16(test.request.Operation))
			want = kafkaCodecTestI16(want, test.request.Version)
			want = kafkaCodecTestI32(want, correlation)
			want = kafkaCodecTestString(want, "apexload-management", false)
			if test.request.flexibleResponse() {
				want = append(want, 0)
			}
			want = append(want, test.body...)
			binary.BigEndian.PutUint32(want[:4], uint32(len(want)-4))
			if !bytes.Equal(got, want) {
				t.Fatalf("request wire mismatch:\n got %x\nwant %x", got, want)
			}
		})
	}
}

func TestKafkaManagementRequestRejectsExpansionAndUnsupportedAPIs(t *testing.T) {
	cases := []struct {
		name    string
		request kafkaManagementRequest
		reason  string
	}{
		{"produce", kafkaManagementRequest{Operation: 0}, "unsupported"},
		{"generic-api", kafkaManagementRequest{Operation: 1000}, "unsupported"},
		{"future-metadata", kafkaManagementRequest{Operation: kafkaManagementMetadataOperation, Version: 13}, "unsupported"},
		{"downgraded-offset-fetch", kafkaManagementRequest{Operation: kafkaManagementOffsetFetch, Version: 6}, "unsupported"},
		{"all-topics", kafkaManagementRequest{Operation: kafkaManagementMetadataOperation, Version: 12}, "invalid"},
		{"all-commits", kafkaManagementRequest{Operation: kafkaManagementOffsetFetch, Version: 7, Group: "g"}, "invalid"},
		{"empty-group", kafkaManagementRequest{Operation: kafkaManagementFindCoordinator, Version: 2}, "invalid"},
		{"auth-fallback", kafkaManagementRequest{Operation: kafkaManagementSASLHandshake, Version: 1, Mechanism: "OAUTHBEARER"}, "unsupported"},
		{"oversized-auth", kafkaManagementRequest{Operation: kafkaManagementSASLAuthenticate, Version: 1, AuthBytes: make([]byte, kafkaManagementMaxAuthBytes+1)}, "budget_exhausted"},
		{"unexpected-auth", kafkaManagementRequest{Operation: kafkaManagementAPIVersions, AuthBytes: []byte{1}}, "invalid"},
		{"unknown-isolation", kafkaManagementRequest{Operation: kafkaManagementListOffsets, Version: 5, Topics: kafkaCodecTestTopic(), Isolation: 2}, "invalid"},
		{"duplicate-topic", kafkaManagementRequest{Operation: kafkaManagementMetadataOperation, Version: 8, Topics: []kafkaManagementTopicRequest{{Name: "orders"}, {Name: "orders"}}}, "invalid"},
		{"unsorted-topics", kafkaManagementRequest{Operation: kafkaManagementMetadataOperation, Version: 12, Topics: []kafkaManagementTopicRequest{{Name: "z"}, {Name: "a"}}}, "invalid"},
		{"oversized-topic", kafkaManagementRequest{Operation: kafkaManagementMetadataOperation, Version: 8, Topics: []kafkaManagementTopicRequest{{Name: strings.Repeat("a", kafkaManagementMaxStringBytes+1)}}}, "budget_exhausted"},
		{"null-partitions", kafkaManagementRequest{Operation: kafkaManagementOffsetFetch, Version: 7, Group: "g", Topics: []kafkaManagementTopicRequest{{Name: "orders"}}}, "invalid"},
		{"negative-partition", kafkaManagementRequest{Operation: kafkaManagementListOffsets, Version: 5, Topics: []kafkaManagementTopicRequest{{Name: "orders", Partitions: []kafkaManagementPartitionRequest{{Partition: -1}}}}}, "invalid"},
		{"negative-epoch", kafkaManagementRequest{Operation: kafkaManagementListOffsets, Version: 5, Topics: []kafkaManagementTopicRequest{{Name: "orders", Partitions: []kafkaManagementPartitionRequest{{CurrentLeaderEpoch: -2}}}}}, "invalid"},
		{"duplicate-partition", kafkaManagementRequest{Operation: kafkaManagementListOffsets, Version: 5, Topics: []kafkaManagementTopicRequest{{Name: "orders", Partitions: []kafkaManagementPartitionRequest{{Partition: 0}, {Partition: 0}}}}}, "invalid"},
		{"unsorted-partitions", kafkaManagementRequest{Operation: kafkaManagementListOffsets, Version: 5, Topics: []kafkaManagementTopicRequest{{Name: "orders", Partitions: []kafkaManagementPartitionRequest{{Partition: 2}, {Partition: 1}}}}}, "invalid"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			frame, err := encodeKafkaManagementRequest(test.request, 1)
			kafkaCodecTestReason(t, err, test.reason)
			if frame != nil {
				t.Fatal("invalid request returned wire bytes")
			}
		})
	}
}

func TestKafkaManagementRequestBudgetsBeforeEncoding(t *testing.T) {
	t.Run("aggregate-frame", func(t *testing.T) {
		topics := make([]kafkaManagementTopicRequest, 64)
		for i := range topics {
			topics[i].Name = string([]byte{byte('a' + i/26), byte('a' + i%26)}) + strings.Repeat("x", 4094)
		}
		_, err := encodeKafkaManagementRequest(kafkaManagementRequest{Operation: kafkaManagementMetadataOperation, Version: 12, Topics: topics}, 1)
		kafkaCodecTestReason(t, err, "budget_exhausted")
	})
	t.Run("aggregate-entities", func(t *testing.T) {
		partitions := make([]kafkaManagementPartitionRequest, 129)
		for i := range partitions {
			partitions[i].Partition = int32(i)
		}
		_, err := encodeKafkaManagementRequest(kafkaManagementRequest{Operation: kafkaManagementOffsetFetch, Version: 7, Group: "g", Topics: []kafkaManagementTopicRequest{{Name: "a", Partitions: partitions}, {Name: "b", Partitions: partitions}}}, 1)
		kafkaCodecTestReason(t, err, "budget_exhausted")
	})
}
