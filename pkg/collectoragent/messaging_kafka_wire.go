package collectoragent

import "github.com/twmb/franz-go/pkg/kmsg"

// These operations and versions form a closed read-only management contract.
// No caller can supply an arbitrary kmsg.Request or a broker-selected API.
type kafkaManagementOperation int16

const (
	kafkaManagementListOffsets       kafkaManagementOperation = 2
	kafkaManagementMetadataOperation kafkaManagementOperation = 3
	kafkaManagementOffsetFetch       kafkaManagementOperation = 9
	kafkaManagementFindCoordinator   kafkaManagementOperation = 10
	kafkaManagementSASLHandshake     kafkaManagementOperation = 17
	kafkaManagementAPIVersions       kafkaManagementOperation = 18
	kafkaManagementSASLAuthenticate  kafkaManagementOperation = 36
	kafkaManagementMaxFrameBytes                              = 256 << 10
	kafkaManagementMaxAuthBytes                               = 16 << 10
	kafkaManagementMaxStringBytes                             = 4 << 10
	kafkaManagementMaxArray                                   = 256
	kafkaManagementMaxRoutingRecords                          = 1024
	kafkaManagementMaxTags                                    = 32
	kafkaManagementMaxTagBytes                                = 16 << 10
	kafkaManagementMaxTagSections                             = 2048
)

type kafkaManagementRequest struct {
	Operation kafkaManagementOperation
	Version   int16
	Mechanism string
	AuthBytes []byte
	Group     string
	Topics    []kafkaManagementTopicRequest
	Isolation int8
}

type kafkaManagementTopicRequest struct {
	Name       string
	Partitions []kafkaManagementPartitionRequest
}

type kafkaManagementPartitionRequest struct {
	Partition          int32
	CurrentLeaderEpoch int32
}

type kafkaAPIVersion struct{ Min, Max int16 }

type kafkaManagementResponse struct {
	ErrorCode         int16
	ThrottleMS        int32
	APIVersions       map[int16]kafkaAPIVersion
	SASLMechanisms    []string
	AuthBytes         []byte
	SessionLifetimeMS int64
	Metadata          *kafkaManagementMetadata
	Coordinator       *kafkaManagementCoordinator
	Offsets           []kafkaManagementOffset
}

type kafkaManagementMetadata struct {
	ClusterID string
	Brokers   map[int32]kafkaManagementBroker
	Topics    map[string]kafkaManagementMetadataTopic
}

type kafkaManagementBroker struct {
	ID   int32
	Host string
	Port int32
}

type kafkaManagementMetadataTopic struct {
	ErrorCode  int16
	TopicID    string
	Partitions map[int32]kafkaManagementMetadataPartition
}

type kafkaManagementMetadataPartition struct {
	ErrorCode   int16
	LeaderID    int32
	LeaderEpoch int32
}

type kafkaManagementCoordinator struct {
	ErrorCode int16
	NodeID    int32
	Host      string
	Port      int32
}

type kafkaManagementOffset struct {
	Topic       string
	Partition   int32
	ErrorCode   int16
	Offset      int64
	LeaderEpoch int32
	// Timestamp is a ListOffsets lookup result, not a source capture clock.
	Timestamp int64
}

func kafkaManagementWireError(reason string) error {
	return &managementObservationError{reason: reason}
}

func (r kafkaManagementRequest) flexibleResponse() bool {
	return r.Operation == kafkaManagementMetadataOperation && r.Version == 12 || r.Operation == kafkaManagementOffsetFetch && r.Version == 7
}

// validate bounds the entire request before allocating generated request slices.
// Topic and partition order is canonical local configuration, not response order.
func (r kafkaManagementRequest) validate() error {
	validVersion := false
	switch r.Operation {
	case kafkaManagementAPIVersions:
		validVersion = r.Version == 0
	case kafkaManagementSASLHandshake, kafkaManagementSASLAuthenticate:
		validVersion = r.Version == 1
	case kafkaManagementMetadataOperation:
		validVersion = r.Version == 8 || r.Version == 12
	case kafkaManagementFindCoordinator:
		validVersion = r.Version == 2
	case kafkaManagementOffsetFetch:
		validVersion = r.Version == 7
	case kafkaManagementListOffsets:
		validVersion = r.Version == 5
	}
	if !validVersion {
		return kafkaManagementWireError("unsupported")
	}
	if len(r.AuthBytes) > kafkaManagementMaxAuthBytes || len(r.Mechanism) > kafkaManagementMaxStringBytes || len(r.Group) > kafkaManagementMaxStringBytes || len(r.Topics) > kafkaManagementMaxArray {
		return kafkaManagementWireError("budget_exhausted")
	}
	if r.Operation != kafkaManagementSASLHandshake && r.Mechanism != "" || r.Operation != kafkaManagementSASLAuthenticate && len(r.AuthBytes) != 0 || r.Operation != kafkaManagementFindCoordinator && r.Operation != kafkaManagementOffsetFetch && r.Group != "" || r.Operation != kafkaManagementListOffsets && r.Isolation != 0 {
		return kafkaManagementWireError("invalid")
	}
	switch r.Operation {
	case kafkaManagementSASLHandshake:
		if r.Mechanism != "PLAIN" && r.Mechanism != "SCRAM-SHA-256" && r.Mechanism != "SCRAM-SHA-512" {
			return kafkaManagementWireError("unsupported")
		}
	case kafkaManagementFindCoordinator, kafkaManagementOffsetFetch:
		if !managementLabel(r.Group, kafkaManagementMaxStringBytes) {
			return kafkaManagementWireError("invalid")
		}
	case kafkaManagementListOffsets:
		if r.Isolation != 0 && r.Isolation != 1 {
			return kafkaManagementWireError("invalid")
		}
	}
	needsTopics := r.Operation == kafkaManagementMetadataOperation || r.Operation == kafkaManagementOffsetFetch || r.Operation == kafkaManagementListOffsets
	if needsTopics != (len(r.Topics) != 0) {
		return kafkaManagementWireError("invalid")
	}
	// A conservative bound includes length prefixes, tags, fixed fields and the
	// client header. It is checked before kmsg is permitted to allocate a frame.
	estimatedBytes, entities := 128+len(r.Mechanism)+len(r.AuthBytes)+len(r.Group), 0
	for i, topic := range r.Topics {
		if len(topic.Name) > kafkaManagementMaxStringBytes || len(topic.Partitions) > kafkaManagementMaxArray {
			return kafkaManagementWireError("budget_exhausted")
		}
		if !managementLabel(topic.Name, kafkaManagementMaxStringBytes) || i > 0 && topic.Name <= r.Topics[i-1].Name {
			return kafkaManagementWireError("invalid")
		}
		if r.Operation == kafkaManagementMetadataOperation {
			if len(topic.Partitions) != 0 {
				return kafkaManagementWireError("invalid")
			}
		} else if len(topic.Partitions) == 0 {
			return kafkaManagementWireError("invalid")
		}
		entities += len(topic.Partitions)
		if entities > kafkaManagementMaxArray {
			return kafkaManagementWireError("budget_exhausted")
		}
		for j, partition := range topic.Partitions {
			if partition.Partition < 0 || partition.CurrentLeaderEpoch < -1 || j > 0 && partition.Partition <= topic.Partitions[j-1].Partition {
				return kafkaManagementWireError("invalid")
			}
		}
		estimatedBytes += 32 + len(topic.Name) + 32*len(topic.Partitions)
		if estimatedBytes > kafkaManagementMaxFrameBytes {
			return kafkaManagementWireError("budget_exhausted")
		}
	}
	return nil
}

// encodeKafkaManagementRequest returns the complete length-prefixed frame.
// kmsg is used only for encoding the explicitly constructed request bodies.
func encodeKafkaManagementRequest(request kafkaManagementRequest, correlation int32) ([]byte, error) {
	if err := request.validate(); err != nil {
		return nil, err
	}
	var wire kmsg.Request
	switch request.Operation {
	case kafkaManagementAPIVersions:
		v := kmsg.NewPtrApiVersionsRequest()
		v.SetVersion(0)
		wire = v
	case kafkaManagementSASLHandshake:
		v := kmsg.NewPtrSASLHandshakeRequest()
		v.SetVersion(1)
		v.Mechanism = request.Mechanism
		wire = v
	case kafkaManagementSASLAuthenticate:
		v := kmsg.NewPtrSASLAuthenticateRequest()
		v.SetVersion(1)
		v.SASLAuthBytes = request.AuthBytes
		wire = v
	case kafkaManagementMetadataOperation:
		v := kmsg.NewPtrMetadataRequest()
		v.SetVersion(request.Version)
		v.Topics = make([]kmsg.MetadataRequestTopic, len(request.Topics))
		for i := range request.Topics {
			v.Topics[i] = kmsg.NewMetadataRequestTopic()
			v.Topics[i].Topic = &request.Topics[i].Name
		}
		v.AllowAutoTopicCreation = false
		v.IncludeClusterAuthorizedOperations = false
		v.IncludeTopicAuthorizedOperations = false
		wire = v
	case kafkaManagementFindCoordinator:
		v := kmsg.NewPtrFindCoordinatorRequest()
		v.SetVersion(2)
		v.CoordinatorKey = request.Group
		v.CoordinatorType = 0
		wire = v
	case kafkaManagementOffsetFetch:
		v := kmsg.NewPtrOffsetFetchRequest()
		v.SetVersion(7)
		v.Group = request.Group
		v.RequireStable = true
		v.Topics = make([]kmsg.OffsetFetchRequestTopic, len(request.Topics))
		for i, topic := range request.Topics {
			v.Topics[i] = kmsg.NewOffsetFetchRequestTopic()
			v.Topics[i].Topic = topic.Name
			v.Topics[i].Partitions = make([]int32, len(topic.Partitions))
			for j, partition := range topic.Partitions {
				v.Topics[i].Partitions[j] = partition.Partition
			}
		}
		wire = v
	case kafkaManagementListOffsets:
		v := kmsg.NewPtrListOffsetsRequest()
		v.SetVersion(5)
		v.ReplicaID = -1
		v.IsolationLevel = request.Isolation
		v.Topics = make([]kmsg.ListOffsetsRequestTopic, len(request.Topics))
		for i, topic := range request.Topics {
			v.Topics[i] = kmsg.NewListOffsetsRequestTopic()
			v.Topics[i].Topic = topic.Name
			v.Topics[i].Partitions = make([]kmsg.ListOffsetsRequestTopicPartition, len(topic.Partitions))
			for j, partition := range topic.Partitions {
				p := kmsg.NewListOffsetsRequestTopicPartition()
				p.Partition = partition.Partition
				p.CurrentLeaderEpoch = partition.CurrentLeaderEpoch
				p.Timestamp = -1
				v.Topics[i].Partitions[j] = p
			}
		}
		wire = v
	}
	formatter := kmsg.NewRequestFormatter(kmsg.FormatterClientID("apexload-management"))
	frame := formatter.AppendRequest(nil, wire, correlation)
	if len(frame)-4 > kafkaManagementMaxFrameBytes {
		return nil, kafkaManagementWireError("budget_exhausted")
	}
	return frame, nil
}
