package collectoragent

import (
	"bytes"
	"encoding/binary"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/twmb/franz-go/pkg/kmsg"
)

const kafkaCodecTestCorrelation int32 = 71

// The pinned encoder is an independent fixture source. Production decoding
// does not use kmsg.ReadFrom, and these tests do not either.
func kafkaCodecTestResponse(request kafkaManagementRequest, response kmsg.Response) []byte {
	response.SetVersion(request.Version)
	frame := kafkaCodecTestI32(nil, kafkaCodecTestCorrelation)
	if request.flexibleResponse() {
		frame = append(frame, 0)
	}
	return response.AppendTo(frame)
}

func kafkaCodecTestMetadata(version int16) (kafkaManagementRequest, *kmsg.MetadataResponse) {
	name, cluster, rack := "orders", "approved-cluster", "ignored-rack"
	request := kafkaManagementRequest{Operation: kafkaManagementMetadataOperation, Version: version, Topics: []kafkaManagementTopicRequest{{Name: name}}}
	response := kmsg.NewPtrMetadataResponse()
	response.SetVersion(version)
	response.ThrottleMillis = 3
	response.ClusterID = &cluster
	response.ControllerID = 0
	response.AuthorizedOperations = -2147483648
	response.Brokers = []kmsg.MetadataResponseBroker{{NodeID: 0, Host: "broker.example", Port: 9093, Rack: &rack}}
	partition := kmsg.NewMetadataResponseTopicPartition()
	partition.Partition, partition.Leader, partition.LeaderEpoch = 0, 0, 8
	partition.Replicas, partition.ISR, partition.OfflineReplicas = []int32{0, 1}, []int32{0}, []int32{1}
	topic := kmsg.NewMetadataResponseTopic()
	topic.Topic = &name
	topic.TopicID = [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	topic.Partitions = []kmsg.MetadataResponseTopicPartition{partition}
	response.Topics = []kmsg.MetadataResponseTopic{topic}
	return request, response
}

func kafkaCodecTestOffsetFetch(offset int64, epoch int32) (kafkaManagementRequest, *kmsg.OffsetFetchResponse) {
	request := kafkaManagementRequest{Operation: kafkaManagementOffsetFetch, Version: 7, Group: "approved-group", Topics: kafkaCodecTestTopic()}
	response := kmsg.NewPtrOffsetFetchResponse()
	response.SetVersion(7)
	metadata := "private application metadata, never evidence"
	response.Topics = []kmsg.OffsetFetchResponseTopic{{Topic: "orders", Partitions: []kmsg.OffsetFetchResponseTopicPartition{{Partition: 0, Offset: offset, LeaderEpoch: epoch, Metadata: &metadata}}}}
	return request, response
}

func kafkaCodecTestListOffsets(offset int64, epoch int32) (kafkaManagementRequest, *kmsg.ListOffsetsResponse) {
	request := kafkaManagementRequest{Operation: kafkaManagementListOffsets, Version: 5, Topics: kafkaCodecTestTopic()}
	response := kmsg.NewPtrListOffsetsResponse()
	response.SetVersion(5)
	response.Topics = []kmsg.ListOffsetsResponseTopic{{Topic: "orders", Partitions: []kmsg.ListOffsetsResponseTopicPartition{{Partition: 0, Offset: offset, LeaderEpoch: epoch, Timestamp: -1}}}}
	return request, response
}

type kafkaCodecTestFixture struct {
	name    string
	request kafkaManagementRequest
	frame   []byte
}

func kafkaCodecTestValidFixtures() []kafkaCodecTestFixture {
	api := kafkaManagementRequest{Operation: kafkaManagementAPIVersions}
	apiResponse := &kmsg.ApiVersionsResponse{ApiKeys: []kmsg.ApiVersionsResponseApiKey{{ApiKey: 3, MinVersion: 0, MaxVersion: 12}, {ApiKey: 9, MinVersion: 0, MaxVersion: 9}}}
	handshake := kafkaManagementRequest{Operation: kafkaManagementSASLHandshake, Version: 1, Mechanism: "PLAIN"}
	auth := kafkaManagementRequest{Operation: kafkaManagementSASLAuthenticate, Version: 1, AuthBytes: []byte{1}}
	secretError := "ignored broker detail"
	coordinator := kafkaManagementRequest{Operation: kafkaManagementFindCoordinator, Version: 2, Group: "approved-group"}
	fixtures := []kafkaCodecTestFixture{
		{"api-versions-0", api, kafkaCodecTestResponse(api, apiResponse)},
		{"handshake-1", handshake, kafkaCodecTestResponse(handshake, &kmsg.SASLHandshakeResponse{SupportedMechanisms: []string{"PLAIN", "SCRAM-SHA-256"}})},
		{"authentication-1", auth, kafkaCodecTestResponse(auth, &kmsg.SASLAuthenticateResponse{SASLAuthBytes: []byte{2, 3}, ErrorMessage: &secretError, SessionLifetimeMillis: 9000})},
		{"coordinator-2", coordinator, kafkaCodecTestResponse(coordinator, &kmsg.FindCoordinatorResponse{NodeID: 0, Host: "broker.example", Port: 9093, ErrorMessage: &secretError, ThrottleMillis: 4})},
	}
	for _, version := range []int16{8, 12} {
		request, response := kafkaCodecTestMetadata(version)
		name := "metadata-8"
		if version == 12 {
			name = "metadata-12"
		}
		fixtures = append(fixtures, kafkaCodecTestFixture{name, request, kafkaCodecTestResponse(request, response)})
	}
	fetch, fetchResponse := kafkaCodecTestOffsetFetch(1<<53+1, 5)
	list, listResponse := kafkaCodecTestListOffsets(math.MaxInt64, 4)
	return append(fixtures, kafkaCodecTestFixture{"offset-fetch-7", fetch, kafkaCodecTestResponse(fetch, fetchResponse)}, kafkaCodecTestFixture{"list-offsets-5", list, kafkaCodecTestResponse(list, listResponse)})
}

func TestKafkaManagementResponseFixedLayouts(t *testing.T) {
	for _, fixture := range kafkaCodecTestValidFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			result, err := decodeKafkaManagementResponse(fixture.frame, fixture.request, kafkaCodecTestCorrelation)
			if err != nil {
				t.Fatal(err)
			}
			switch fixture.request.Operation {
			case kafkaManagementAPIVersions:
				if !reflect.DeepEqual(result.APIVersions, map[int16]kafkaAPIVersion{3: {Min: 0, Max: 12}, 9: {Min: 0, Max: 9}}) {
					t.Fatalf("unexpected capabilities: %#v", result.APIVersions)
				}
			case kafkaManagementSASLHandshake:
				if !reflect.DeepEqual(result.SASLMechanisms, []string{"PLAIN", "SCRAM-SHA-256"}) {
					t.Fatalf("unexpected mechanisms: %#v", result.SASLMechanisms)
				}
			case kafkaManagementSASLAuthenticate:
				if !bytes.Equal(result.AuthBytes, []byte{2, 3}) || result.SessionLifetimeMS != 9000 {
					t.Fatalf("unexpected bounded authentication result: %#v", result)
				}
				// A challenge must own its storage before transport wipes its frame.
				for i := range fixture.frame {
					fixture.frame[i] = 0
				}
				if !bytes.Equal(result.AuthBytes, []byte{2, 3}) {
					t.Fatal("authentication challenge aliases transport frame")
				}
			case kafkaManagementFindCoordinator:
				want := &kafkaManagementCoordinator{NodeID: 0, Host: "broker.example", Port: 9093}
				if !reflect.DeepEqual(result.Coordinator, want) || result.ThrottleMS != 4 {
					t.Fatalf("unexpected coordinator: %#v", result)
				}
			case kafkaManagementMetadataOperation:
				if result.Metadata == nil || result.Metadata.ClusterID != "approved-cluster" || result.ThrottleMS != 3 {
					t.Fatalf("unexpected metadata: %#v", result)
				}
				topic := result.Metadata.Topics["orders"]
				if len(topic.Partitions) != 1 || topic.Partitions[0].LeaderEpoch != 8 || topic.Partitions[0].LeaderID != 0 {
					t.Fatalf("lost current leader identity: %#v", topic)
				}
				if fixture.request.Version == 8 && topic.TopicID != "" {
					t.Fatal("Metadata8 invented topic ID")
				}
				if fixture.request.Version == 12 && topic.TopicID != "01020304-0506-0708-090a-0b0c0d0e0f10" {
					t.Fatalf("invalid topic UUID: %q", topic.TopicID)
				}
			case kafkaManagementOffsetFetch:
				if len(result.Offsets) != 1 || result.Offsets[0].Offset != 1<<53+1 || result.Offsets[0].LeaderEpoch != 5 || result.Offsets[0].Timestamp != -1 {
					t.Fatalf("lost exact committed offset/epoch: %#v", result.Offsets)
				}
			case kafkaManagementListOffsets:
				if len(result.Offsets) != 1 || result.Offsets[0].Offset != math.MaxInt64 || result.Offsets[0].LeaderEpoch != 4 || result.Offsets[0].Timestamp != -1 {
					t.Fatalf("lost exact boundary/record epoch: %#v", result.Offsets)
				}
			}
		})
	}
}

func TestKafkaManagementResponseOffsetSentinelsAndErrors(t *testing.T) {
	for _, offset := range []int64{-1, 0, 1, 1<<53 + 1, math.MaxInt64} {
		for _, fetch := range []bool{false, true} {
			var request kafkaManagementRequest
			var response kmsg.Response
			if fetch {
				request, response = kafkaCodecTestOffsetFetch(offset, -1)
			} else {
				request, response = kafkaCodecTestListOffsets(offset, -1)
			}
			result, err := decodeKafkaManagementResponse(kafkaCodecTestResponse(request, response), request, kafkaCodecTestCorrelation)
			if err != nil || len(result.Offsets) != 1 || result.Offsets[0].Offset != offset || result.Offsets[0].LeaderEpoch != -1 {
				t.Fatalf("fetch=%v offset=%d lost sentinel/exact value: %#v, %v", fetch, offset, result, err)
			}
		}
	}
	t.Run("group-error-precedes-success-looking-row", func(t *testing.T) {
		request, response := kafkaCodecTestOffsetFetch(42, 2)
		response.ErrorCode = 30
		result, err := decodeKafkaManagementResponse(kafkaCodecTestResponse(request, response), request, kafkaCodecTestCorrelation)
		if err != nil || result.ErrorCode != 30 || len(result.Offsets) != 1 || result.Offsets[0].ErrorCode != 0 {
			t.Fatalf("lost top-level group qualification: %#v, %v", result, err)
		}
	})
	t.Run("partition-error-retained", func(t *testing.T) {
		request, response := kafkaCodecTestListOffsets(42, 2)
		response.Topics[0].Partitions[0].ErrorCode = 6
		result, err := decodeKafkaManagementResponse(kafkaCodecTestResponse(request, response), request, kafkaCodecTestCorrelation)
		if err != nil || result.Offsets[0].ErrorCode != 6 {
			t.Fatalf("lost partition qualification: %#v, %v", result, err)
		}
	})
	for _, fetch := range []bool{false, true} {
		for _, badEpoch := range []bool{false, true} {
			var request kafkaManagementRequest
			var response kmsg.Response
			offset, epoch := int64(-2), int32(-1)
			if badEpoch {
				offset, epoch = 1, -2
			}
			if fetch {
				request, response = kafkaCodecTestOffsetFetch(offset, epoch)
			} else {
				request, response = kafkaCodecTestListOffsets(offset, epoch)
			}
			_, err := decodeKafkaManagementResponse(kafkaCodecTestResponse(request, response), request, kafkaCodecTestCorrelation)
			kafkaCodecTestReason(t, err, "invalid")
		}
	}
}

func TestKafkaManagementResponseMissingDoesNotBecomeZero(t *testing.T) {
	request, response := kafkaCodecTestOffsetFetch(0, -1)
	response.Topics = nil
	result, err := decodeKafkaManagementResponse(kafkaCodecTestResponse(request, response), request, kafkaCodecTestCorrelation)
	if err != nil || len(result.Offsets) != 0 {
		t.Fatalf("missing rows became observations: %#v, %v", result, err)
	}
	// Multiple compact topics with no partitions remain valid missing evidence.
	request.Topics = nil
	response.Topics = nil
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		request.Topics = append(request.Topics, kafkaManagementTopicRequest{Name: name, Partitions: []kafkaManagementPartitionRequest{{Partition: 0}}})
		response.Topics = append(response.Topics, kmsg.OffsetFetchResponseTopic{Topic: name})
	}
	result, err = decodeKafkaManagementResponse(kafkaCodecTestResponse(request, response), request, kafkaCodecTestCorrelation)
	if err != nil || len(result.Offsets) != 0 {
		t.Fatalf("valid empty compact partition arrays rejected: %#v, %v", result, err)
	}
	metadataRequest, metadata := kafkaCodecTestMetadata(12)
	metadata.Topics[0].TopicID = [16]byte{}
	metadata.Topics[0].Partitions[0].Replicas = nil
	metadata.Topics[0].Partitions[0].ISR = nil
	metadata.Topics[0].Partitions[0].OfflineReplicas = nil
	result, err = decodeKafkaManagementResponse(kafkaCodecTestResponse(metadataRequest, metadata), metadataRequest, kafkaCodecTestCorrelation)
	if err != nil || result.Metadata.Topics["orders"].TopicID != "" {
		t.Fatalf("zero UUID invented identity or minimal partition rejected: %#v, %v", result, err)
	}
}

func TestKafkaManagementResponseTruncationCorrelationAndTrailing(t *testing.T) {
	for _, fixture := range kafkaCodecTestValidFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			for n := 0; n < len(fixture.frame); n++ {
				result, err := decodeKafkaManagementResponse(fixture.frame[:n], fixture.request, kafkaCodecTestCorrelation)
				if err == nil || !reflect.DeepEqual(result, kafkaManagementResponse{}) {
					t.Fatalf("accepted truncation at %d/%d: %#v", n, len(fixture.frame), result)
				}
			}
			_, err := decodeKafkaManagementResponse(fixture.frame, fixture.request, kafkaCodecTestCorrelation+1)
			kafkaCodecTestReason(t, err, "invalid")
			_, err = decodeKafkaManagementResponse(append(append([]byte{}, fixture.frame...), 0), fixture.request, kafkaCodecTestCorrelation)
			kafkaCodecTestReason(t, err, "invalid")
		})
	}
	_, err := decodeKafkaManagementResponse(make([]byte, kafkaManagementMaxFrameBytes+1), kafkaManagementRequest{Operation: kafkaManagementAPIVersions}, kafkaCodecTestCorrelation)
	kafkaCodecTestReason(t, err, "budget_exhausted")
}

func TestKafkaManagementResponseRejectsDuplicatesAndExpansion(t *testing.T) {
	for _, version := range []int16{8, 12} {
		for _, kind := range []string{"broker", "topic", "partition", "replicas", "isr", "offline", "unknown-topic", "null-topic", "negative-epoch"} {
			if version == 8 && kind == "null-topic" {
				continue
			}
			t.Run(kind+string(rune('A'+version)), func(t *testing.T) {
				request, response := kafkaCodecTestMetadata(version)
				switch kind {
				case "broker":
					response.Brokers = append(response.Brokers, response.Brokers[0])
				case "topic":
					response.Topics = append(response.Topics, response.Topics[0])
				case "partition":
					response.Topics[0].Partitions = append(response.Topics[0].Partitions, response.Topics[0].Partitions[0])
				case "replicas":
					response.Topics[0].Partitions[0].Replicas = []int32{0, 0}
				case "isr":
					response.Topics[0].Partitions[0].ISR = []int32{0, 0}
				case "offline":
					response.Topics[0].Partitions[0].OfflineReplicas = []int32{1, 1}
				case "unknown-topic":
					name := "unapproved"
					response.Topics[0].Topic = &name
				case "null-topic":
					response.Topics[0].Topic = nil
				case "negative-epoch":
					response.Topics[0].Partitions[0].LeaderEpoch = -2
				}
				_, err := decodeKafkaManagementResponse(kafkaCodecTestResponse(request, response), request, kafkaCodecTestCorrelation)
				kafkaCodecTestReason(t, err, "invalid")
			})
		}
	}
	for _, kind := range []string{"duplicate-topic", "duplicate-partition", "unknown-topic", "unknown-partition"} {
		t.Run("fetch-"+kind, func(t *testing.T) {
			request, response := kafkaCodecTestOffsetFetch(42, 2)
			switch kind {
			case "duplicate-topic":
				response.Topics = append(response.Topics, response.Topics[0])
			case "duplicate-partition":
				response.Topics[0].Partitions = append(response.Topics[0].Partitions, response.Topics[0].Partitions[0])
			case "unknown-topic":
				response.Topics[0].Topic = "unapproved"
			case "unknown-partition":
				response.Topics[0].Partitions[0].Partition = 99
			}
			_, err := decodeKafkaManagementResponse(kafkaCodecTestResponse(request, response), request, kafkaCodecTestCorrelation)
			kafkaCodecTestReason(t, err, "invalid")
		})
		t.Run("list-"+kind, func(t *testing.T) {
			request, response := kafkaCodecTestListOffsets(42, 2)
			switch kind {
			case "duplicate-topic":
				response.Topics = append(response.Topics, response.Topics[0])
			case "duplicate-partition":
				response.Topics[0].Partitions = append(response.Topics[0].Partitions, response.Topics[0].Partitions[0])
			case "unknown-topic":
				response.Topics[0].Topic = "unapproved"
			case "unknown-partition":
				response.Topics[0].Partitions[0].Partition = 99
			}
			_, err := decodeKafkaManagementResponse(kafkaCodecTestResponse(request, response), request, kafkaCodecTestCorrelation)
			kafkaCodecTestReason(t, err, "invalid")
		})
	}
	t.Run("additional-metadata-partition-is-only-bounded-routing-context", func(t *testing.T) {
		request, response := kafkaCodecTestMetadata(12)
		extra := response.Topics[0].Partitions[0]
		extra.Partition = 99
		response.Topics[0].Partitions = append(response.Topics[0].Partitions, extra)
		result, err := decodeKafkaManagementResponse(kafkaCodecTestResponse(request, response), request, kafkaCodecTestCorrelation)
		if err != nil || len(result.Metadata.Topics["orders"].Partitions) != 2 {
			t.Fatalf("valid named-topic routing context rejected: %#v, %v", result, err)
		}
	})
}

func TestKafkaManagementResponseBoundedArraysAndStrings(t *testing.T) {
	api := kafkaManagementRequest{Operation: kafkaManagementAPIVersions}
	for _, count := range []int32{257, math.MaxInt32} {
		frame := kafkaCodecTestI32(nil, kafkaCodecTestCorrelation)
		frame = kafkaCodecTestI16(frame, 0)
		frame = kafkaCodecTestI32(frame, count)
		_, err := decodeKafkaManagementResponse(frame, api, kafkaCodecTestCorrelation)
		kafkaCodecTestReason(t, err, "budget_exhausted")
	}
	for _, count := range []int32{-1, -2} {
		frame := kafkaCodecTestI32(nil, kafkaCodecTestCorrelation)
		frame = kafkaCodecTestI16(frame, 0)
		frame = kafkaCodecTestI32(frame, count)
		_, err := decodeKafkaManagementResponse(frame, api, kafkaCodecTestCorrelation)
		kafkaCodecTestReason(t, err, "invalid")
	}
	t.Run("routing-record-total", func(t *testing.T) {
		request, response := kafkaCodecTestMetadata(8)
		ids := make([]int32, 128)
		for i := range ids {
			ids[i] = int32(i)
		}
		response.Topics[0].Partitions = nil
		for i := 0; i < 4; i++ {
			response.Topics[0].Partitions = append(response.Topics[0].Partitions, kmsg.MetadataResponseTopicPartition{Partition: int32(i), LeaderEpoch: 1, Replicas: ids, ISR: ids, OfflineReplicas: ids})
		}
		_, err := decodeKafkaManagementResponse(kafkaCodecTestResponse(request, response), request, kafkaCodecTestCorrelation)
		kafkaCodecTestReason(t, err, "budget_exhausted")
	})
	t.Run("array-limit-applies-to-skipped-replicas", func(t *testing.T) {
		request, response := kafkaCodecTestMetadata(12)
		response.Topics[0].Partitions[0].Replicas = make([]int32, 257)
		_, err := decodeKafkaManagementResponse(kafkaCodecTestResponse(request, response), request, kafkaCodecTestCorrelation)
		kafkaCodecTestReason(t, err, "budget_exhausted")
	})
	t.Run("ignored-application-metadata-bounded", func(t *testing.T) {
		request, response := kafkaCodecTestOffsetFetch(1, 1)
		metadata := strings.Repeat("s", kafkaManagementMaxStringBytes+1)
		response.Topics[0].Partitions[0].Metadata = &metadata
		_, err := decodeKafkaManagementResponse(kafkaCodecTestResponse(request, response), request, kafkaCodecTestCorrelation)
		kafkaCodecTestReason(t, err, "budget_exhausted")
	})
	t.Run("auth-challenge-bounded", func(t *testing.T) {
		request := kafkaManagementRequest{Operation: kafkaManagementSASLAuthenticate, Version: 1}
		response := &kmsg.SASLAuthenticateResponse{SASLAuthBytes: make([]byte, kafkaManagementMaxAuthBytes+1)}
		_, err := decodeKafkaManagementResponse(kafkaCodecTestResponse(request, response), request, kafkaCodecTestCorrelation)
		kafkaCodecTestReason(t, err, "budget_exhausted")
	})
	t.Run("auth-lifetime-negative", func(t *testing.T) {
		request := kafkaManagementRequest{Operation: kafkaManagementSASLAuthenticate, Version: 1}
		_, err := decodeKafkaManagementResponse(kafkaCodecTestResponse(request, &kmsg.SASLAuthenticateResponse{SessionLifetimeMillis: -1}), request, kafkaCodecTestCorrelation)
		kafkaCodecTestReason(t, err, "invalid")
	})
}

func TestKafkaManagementResponseFlexibleTagsAndCompactLengths(t *testing.T) {
	request, response := kafkaCodecTestMetadata(12)
	valid := kafkaCodecTestResponse(request, response)
	for _, test := range []struct {
		name   string
		header []byte
		reason string
	}{
		{"huge-tag-count", []byte{255, 255, 255, 255, 15}, "budget_exhausted"},
		{"too-many-tags", []byte{33}, "budget_exhausted"},
		{"noncanonical-count", []byte{128, 0}, "invalid"},
		{"overflow-count", []byte{255, 255, 255, 255, 16}, "invalid"},
		{"duplicate-tags", []byte{2, 1, 0, 1, 0}, "invalid"},
		{"descending-tags", []byte{2, 2, 0, 1, 0}, "invalid"},
		{"huge-payload", kafkaCodecTestUvarint([]byte{1, 0}, kafkaManagementMaxTagBytes+1), "budget_exhausted"},
		{"truncated-payload", []byte{1, 0, 127}, "invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			frame := kafkaCodecTestI32(nil, kafkaCodecTestCorrelation)
			frame = append(frame, test.header...)
			if test.name != "truncated-payload" {
				frame = append(frame, valid[5:]...)
			}
			_, err := decodeKafkaManagementResponse(frame, request, kafkaCodecTestCorrelation)
			kafkaCodecTestReason(t, err, test.reason)
		})
	}
	t.Run("valid-header-and-nested-tags-are-skipped", func(t *testing.T) {
		response.Brokers[0].UnknownTags.Set(100, []byte("private broker payload"))
		response.Topics[0].UnknownTags.Set(101, []byte("private topic payload"))
		response.Topics[0].Partitions[0].UnknownTags.Set(102, []byte("private partition payload"))
		response.UnknownTags.Set(103, []byte("private response payload"))
		frame := kafkaCodecTestResponse(request, response)
		header := []byte{1, 3, 3, 9, 8, 7}
		frame = append(append(append([]byte{}, frame[:4]...), header...), frame[5:]...)
		result, err := decodeKafkaManagementResponse(frame, request, kafkaCodecTestCorrelation)
		if err != nil || result.Metadata.ClusterID != "approved-cluster" {
			t.Fatalf("valid bounded tags rejected: %#v, %v", result, err)
		}
	})
	t.Run("cumulative-tag-payload", func(t *testing.T) {
		request, response := kafkaCodecTestMetadata(12)
		response.Brokers[0].UnknownTags.Set(100, make([]byte, 2048))
		frame := kafkaCodecTestResponse(request, response)
		header := kafkaCodecTestUvarint([]byte{1, 0}, 15<<10)
		header = append(header, make([]byte, 15<<10)...)
		frame = append(append(append([]byte{}, frame[:4]...), header...), frame[5:]...)
		_, err := decodeKafkaManagementResponse(frame, request, kafkaCodecTestCorrelation)
		kafkaCodecTestReason(t, err, "budget_exhausted")
	})
	// After correlation + header tags + throttle is the compact broker count.
	for _, test := range []struct {
		name   string
		count  []byte
		reason string
	}{
		{"null-array", []byte{0}, "invalid"},
		{"overflow-compact-array", []byte{255, 255, 255, 255, 15}, "budget_exhausted"},
		{"noncanonical-array", []byte{130, 0}, "invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			frame := append([]byte{}, valid[:9]...)
			frame = append(frame, test.count...)
			frame = append(frame, valid[10:]...)
			_, err := decodeKafkaManagementResponse(frame, request, kafkaCodecTestCorrelation)
			kafkaCodecTestReason(t, err, test.reason)
		})
	}
}

func TestKafkaManagementResponseCanonicalBoolAndVersionBoundaries(t *testing.T) {
	// Locate IsInternal independently using the pinned encoder's known topic
	// name/UUID prefix, then corrupt its canonical bool without changing lengths.
	for _, version := range []int16{8, 12} {
		request, response := kafkaCodecTestMetadata(version)
		frame := kafkaCodecTestResponse(request, response)
		needle := kafkaCodecTestString(nil, "orders", version == 12)
		if version == 12 {
			needle = append(needle, response.Topics[0].TopicID[:]...)
		}
		at := bytes.Index(frame, needle)
		if at < 0 {
			t.Fatal("fixture topic prefix missing")
		}
		frame[at+len(needle)] = 2
		_, err := decodeKafkaManagementResponse(frame, request, kafkaCodecTestCorrelation)
		kafkaCodecTestReason(t, err, "invalid")
	}
	t.Run("metadata12-has-no-cluster-acl", func(t *testing.T) {
		request, response := kafkaCodecTestMetadata(12)
		frame := kafkaCodecTestResponse(request, response)
		frame = append(frame, 0, 0, 0, 0)
		_, err := decodeKafkaManagementResponse(frame, request, kafkaCodecTestCorrelation)
		kafkaCodecTestReason(t, err, "invalid")
	})
	t.Run("api-versions0-has-no-throttle-or-header-tags", func(t *testing.T) {
		request := kafkaManagementRequest{Operation: kafkaManagementAPIVersions}
		frame := kafkaCodecTestResponse(request, &kmsg.ApiVersionsResponse{})
		frame = append(frame, 0, 0, 0, 0)
		_, err := decodeKafkaManagementResponse(frame, request, kafkaCodecTestCorrelation)
		kafkaCodecTestReason(t, err, "invalid")
	})
}

func FuzzKafkaManagementResponseDecoder(f *testing.F) {
	fixtures := kafkaCodecTestValidFixtures()
	for i, fixture := range fixtures {
		f.Add(uint8(i), fixture.frame)
	}
	f.Fuzz(func(t *testing.T, which uint8, frame []byte) {
		fixture := fixtures[int(which)%len(fixtures)]
		result, err := decodeKafkaManagementResponse(frame, fixture.request, kafkaCodecTestCorrelation)
		if err != nil && !reflect.DeepEqual(result, kafkaManagementResponse{}) {
			t.Fatal("malformed frame returned partial evidence")
		}
		if err == nil && (len(frame) > kafkaManagementMaxFrameBytes || len(frame) < 4 || int32(binary.BigEndian.Uint32(frame[:4])) != kafkaCodecTestCorrelation) {
			t.Fatal("accepted invalid frame boundary")
		}
	})
}
