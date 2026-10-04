package collectoragent

import (
	"encoding/binary"
	"encoding/hex"
)

// The cursor never delegates broker-controlled bytes to a generated decoder.
// All lengths are validated before allocation or iteration; ignored application
// metadata, error messages and tags are never retained in the typed response.
type kafkaManagementCursor struct {
	body           []byte
	at             int
	err            error
	routingRecords int
	tagSections    int
	tagBytes       int
}

func (c *kafkaManagementCursor) fail(reason string) {
	if c.err == nil {
		c.err = kafkaManagementWireError(reason)
	}
}

func (c *kafkaManagementCursor) take(n int) []byte {
	if c.err != nil {
		return nil
	}
	if n < 0 || n > len(c.body)-c.at {
		c.fail("invalid")
		return nil
	}
	p := c.body[c.at : c.at+n]
	c.at += n
	return p
}

func (c *kafkaManagementCursor) i16() int16 {
	p := c.take(2)
	if p == nil {
		return 0
	}
	return int16(binary.BigEndian.Uint16(p))
}

func (c *kafkaManagementCursor) i32() int32 {
	p := c.take(4)
	if p == nil {
		return 0
	}
	return int32(binary.BigEndian.Uint32(p))
}

func (c *kafkaManagementCursor) i64() int64 {
	p := c.take(8)
	if p == nil {
		return 0
	}
	return int64(binary.BigEndian.Uint64(p))
}

func (c *kafkaManagementCursor) boolean() bool {
	p := c.take(1)
	if p == nil {
		return false
	}
	if p[0] > 1 {
		c.fail("invalid")
	}
	return p[0] == 1
}

// uvarint accepts only the canonical uint32 form. It cannot wrap a compact
// length into a negative count or loop on a declared unbounded tag count.
func (c *kafkaManagementCursor) uvarint() uint32 {
	var value uint32
	for i := 0; i < 5; i++ {
		p := c.take(1)
		if p == nil {
			return 0
		}
		b := p[0]
		if i == 4 && b > 15 {
			c.fail("invalid")
			return 0
		}
		value |= uint32(b&127) << (7 * i)
		if b&128 == 0 {
			if i > 0 && b == 0 {
				c.fail("invalid")
			}
			return value
		}
	}
	c.fail("invalid")
	return 0
}

func (c *kafkaManagementCursor) array(compact bool, minElementBytes int) int {
	var count int64
	if compact {
		n := c.uvarint()
		if n == 0 {
			c.fail("invalid") // No response array in these contracts is nullable.
			return 0
		}
		count = int64(n) - 1
	} else {
		count = int64(c.i32())
	}
	if c.err != nil {
		return 0
	}
	if count < 0 {
		c.fail("invalid")
		return 0
	}
	if count > kafkaManagementMaxArray || count+int64(c.routingRecords) > kafkaManagementMaxRoutingRecords {
		c.fail("budget_exhausted")
		return 0
	}
	if minElementBytes < 1 || count > int64((len(c.body)-c.at)/minElementBytes) {
		c.fail("invalid")
		return 0
	}
	c.routingRecords += int(count)
	return int(count)
}

func (c *kafkaManagementCursor) text(compact, nullable, retain bool) (string, bool) {
	var length int64
	if compact {
		n := c.uvarint()
		if n == 0 {
			if !nullable {
				c.fail("invalid")
			}
			return "", false
		}
		length = int64(n) - 1
	} else {
		length = int64(c.i16())
		if length == -1 && nullable {
			return "", false
		}
	}
	if c.err != nil {
		return "", false
	}
	if length < 0 {
		c.fail("invalid")
		return "", false
	}
	if length > kafkaManagementMaxStringBytes {
		c.fail("budget_exhausted")
		return "", false
	}
	p := c.take(int(length))
	if c.err != nil {
		return "", false
	}
	if !retain {
		return "", true
	}
	return string(p), true
}

func (c *kafkaManagementCursor) tags() {
	if c.err != nil {
		return
	}
	c.tagSections++
	if c.tagSections > kafkaManagementMaxTagSections {
		c.fail("budget_exhausted")
		return
	}
	count := c.uvarint()
	if c.err != nil {
		return
	}
	if count > kafkaManagementMaxTags {
		c.fail("budget_exhausted")
		return
	}
	if int(count) > (len(c.body)-c.at)/2 {
		c.fail("invalid")
		return
	}
	var last uint32
	for i := uint32(0); i < count; i++ {
		id, size := c.uvarint(), c.uvarint()
		if c.err != nil {
			return
		}
		if i > 0 && id <= last {
			c.fail("invalid")
			return
		}
		if size > kafkaManagementMaxTagBytes || int(size) > kafkaManagementMaxTagBytes-c.tagBytes {
			c.fail("budget_exhausted")
			return
		}
		c.tagBytes += int(size)
		c.take(int(size))
		if c.err != nil {
			return
		}
		last = id
	}
}

func (c *kafkaManagementCursor) throttle() int32 {
	n := c.i32()
	if n < 0 {
		c.fail("invalid")
	}
	return n
}

func (c *kafkaManagementCursor) replicaIDs(compact bool) {
	count := c.array(compact, 4)
	seen := make(map[int32]struct{}, count)
	for i := 0; i < count && c.err == nil; i++ {
		id := c.i32()
		if _, duplicate := seen[id]; id < 0 || duplicate {
			c.fail("invalid")
			return
		}
		seen[id] = struct{}{}
	}
}

// decodeKafkaManagementResponse accepts a response body including its header,
// excluding the four-byte length prefix that transport validates before reading.
func decodeKafkaManagementResponse(frame []byte, request kafkaManagementRequest, correlation int32) (kafkaManagementResponse, error) {
	var result kafkaManagementResponse
	if err := request.validate(); err != nil {
		return result, err
	}
	if len(frame) > kafkaManagementMaxFrameBytes {
		return result, kafkaManagementWireError("budget_exhausted")
	}
	c := kafkaManagementCursor{body: frame}
	if c.i32() != correlation {
		c.fail("invalid")
	}
	if request.flexibleResponse() {
		c.tags()
	}
	if c.err != nil {
		return kafkaManagementResponse{}, c.err
	}
	switch request.Operation {
	case kafkaManagementAPIVersions:
		result.ErrorCode = c.i16()
		count := c.array(false, 6)
		result.APIVersions = make(map[int16]kafkaAPIVersion, count)
		for i := 0; i < count && c.err == nil; i++ {
			key := c.i16()
			version := kafkaAPIVersion{Min: c.i16(), Max: c.i16()}
			if _, duplicate := result.APIVersions[key]; key < 0 || version.Min < 0 || version.Max < version.Min || duplicate {
				c.fail("invalid")
				break
			}
			result.APIVersions[key] = version
		}
	case kafkaManagementSASLHandshake:
		result.ErrorCode = c.i16()
		count := c.array(false, 2)
		result.SASLMechanisms = make([]string, 0, count)
		seen := make(map[string]struct{}, count)
		for i := 0; i < count && c.err == nil; i++ {
			mechanism, _ := c.text(false, false, true)
			if _, duplicate := seen[mechanism]; !managementLabel(mechanism, kafkaManagementMaxStringBytes) || duplicate {
				c.fail("invalid")
				break
			}
			seen[mechanism] = struct{}{}
			result.SASLMechanisms = append(result.SASLMechanisms, mechanism)
		}
	case kafkaManagementSASLAuthenticate:
		result.ErrorCode = c.i16()
		c.text(false, true, false)
		length := c.i32()
		if length < 0 {
			c.fail("invalid")
		} else if length > kafkaManagementMaxAuthBytes {
			c.fail("budget_exhausted")
		} else if p := c.take(int(length)); c.err == nil {
			result.AuthBytes = append([]byte(nil), p...)
		}
		result.SessionLifetimeMS = c.i64()
		if result.SessionLifetimeMS < 0 {
			c.fail("invalid")
		}
	case kafkaManagementMetadataOperation:
		result.ThrottleMS = c.throttle()
		result.Metadata = c.metadata(request)
	case kafkaManagementFindCoordinator:
		result.ThrottleMS = c.throttle()
		result.ErrorCode = c.i16()
		c.text(false, true, false)
		coordinator := &kafkaManagementCoordinator{ErrorCode: result.ErrorCode, NodeID: c.i32()}
		coordinator.Host, _ = c.text(false, false, true)
		coordinator.Port = c.i32()
		if coordinator.NodeID < -1 || coordinator.Port < -1 || coordinator.Port > 65535 || coordinator.Host != "" && !managementLabel(coordinator.Host, kafkaManagementMaxStringBytes) {
			c.fail("invalid")
		}
		if result.ErrorCode == 0 && (coordinator.NodeID < 0 || coordinator.Port < 1 || coordinator.Host == "") {
			c.fail("invalid")
		}
		result.Coordinator = coordinator
	case kafkaManagementOffsetFetch, kafkaManagementListOffsets:
		result.ThrottleMS = c.throttle()
		result.Offsets = c.offsets(request)
		if request.Operation == kafkaManagementOffsetFetch {
			result.ErrorCode = c.i16() // v7 remains a single group with this trailing error.
			c.tags()
		}
	}
	if c.err == nil && c.at != len(c.body) {
		c.fail("invalid")
	}
	if c.err != nil {
		// Never expose a partly decoded result as accepted evidence.
		for i := range result.AuthBytes {
			result.AuthBytes[i] = 0
		}
		return kafkaManagementResponse{}, c.err
	}
	return result, nil
}

func (c *kafkaManagementCursor) metadata(request kafkaManagementRequest) *kafkaManagementMetadata {
	compact := request.Version == 12
	result := &kafkaManagementMetadata{}
	count := c.array(compact, 10)
	result.Brokers = make(map[int32]kafkaManagementBroker, count)
	for i := 0; i < count && c.err == nil; i++ {
		broker := kafkaManagementBroker{ID: c.i32()}
		broker.Host, _ = c.text(compact, false, true)
		broker.Port = c.i32()
		c.text(compact, true, false) // Rack is not an output or routing authority.
		if compact {
			c.tags()
		}
		if _, duplicate := result.Brokers[broker.ID]; broker.ID < 0 || broker.Port < 1 || broker.Port > 65535 || !managementLabel(broker.Host, kafkaManagementMaxStringBytes) || duplicate {
			c.fail("invalid")
			break
		}
		result.Brokers[broker.ID] = broker
	}
	result.ClusterID, _ = c.text(compact, true, true)
	if result.ClusterID != "" && !managementLabel(result.ClusterID, kafkaManagementMaxStringBytes) {
		c.fail("invalid")
	}
	if c.i32() < -1 { // Controller ID is context only, never a discovered destination.
		c.fail("invalid")
	}
	count = c.array(compact, 8)
	result.Topics = make(map[string]kafkaManagementMetadataTopic, count)
	approved := make(map[string]struct{}, len(request.Topics))
	for _, topic := range request.Topics {
		approved[topic.Name] = struct{}{}
	}
	for i := 0; i < count && c.err == nil; i++ {
		topic := kafkaManagementMetadataTopic{ErrorCode: c.i16()}
		name, present := c.text(compact, compact, true)
		if _, ok := approved[name]; !ok || !present {
			c.fail("invalid")
			break
		}
		if _, duplicate := result.Topics[name]; duplicate {
			c.fail("invalid")
			break
		}
		if compact {
			id := c.take(16)
			if c.err == nil {
				var zero [16]byte
				if [16]byte(id) != zero {
					encoded := hex.EncodeToString(id)
					topic.TopicID = encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
				}
			}
		}
		c.boolean() // Canonical IsInternal; it does not qualify offsets.
		minPartitionBytes := 26
		if compact {
			minPartitionBytes = 18
		}
		partitions := c.array(compact, minPartitionBytes)
		topic.Partitions = make(map[int32]kafkaManagementMetadataPartition, partitions)
		for j := 0; j < partitions && c.err == nil; j++ {
			partition := kafkaManagementMetadataPartition{ErrorCode: c.i16()}
			id := c.i32()
			partition.LeaderID = c.i32()
			partition.LeaderEpoch = c.i32()
			if _, duplicate := topic.Partitions[id]; id < 0 || partition.LeaderID < -1 || partition.LeaderEpoch < -1 || duplicate {
				c.fail("invalid")
				break
			}
			c.replicaIDs(compact)
			c.replicaIDs(compact)
			c.replicaIDs(compact)
			if compact {
				c.tags()
			}
			topic.Partitions[id] = partition
		}
		c.i32() // TopicAuthorizedOperations exists in both supported versions.
		if compact {
			c.tags()
		}
		result.Topics[name] = topic
	}
	if compact {
		c.tags()
	} else {
		c.i32() // ClusterAuthorizedOperations exists in v8, but not v12.
	}
	return result
}

func (c *kafkaManagementCursor) offsets(request kafkaManagementRequest) []kafkaManagementOffset {
	compact := request.Operation == kafkaManagementOffsetFetch
	approved := make(map[string]map[int32]struct{}, len(request.Topics))
	for _, topic := range request.Topics {
		partitions := make(map[int32]struct{}, len(topic.Partitions))
		for _, partition := range topic.Partitions {
			partitions[partition.Partition] = struct{}{}
		}
		approved[topic.Name] = partitions
	}
	minTopicBytes := 6
	if compact {
		minTopicBytes = 3
	}
	count := c.array(compact, minTopicBytes)
	seenTopics := make(map[string]struct{}, count)
	result := make([]kafkaManagementOffset, 0)
	for i := 0; i < count && c.err == nil; i++ {
		name, _ := c.text(compact, false, true)
		allowed, ok := approved[name]
		if _, duplicate := seenTopics[name]; !ok || duplicate {
			c.fail("invalid")
			break
		}
		seenTopics[name] = struct{}{}
		minPartitionBytes := 26
		if compact {
			minPartitionBytes = 20
		}
		partitions := c.array(compact, minPartitionBytes)
		if partitions+len(result) > kafkaManagementMaxArray {
			c.fail("budget_exhausted")
			break
		}
		seenPartitions := make(map[int32]struct{}, partitions)
		for j := 0; j < partitions && c.err == nil; j++ {
			row := kafkaManagementOffset{Topic: name, Partition: c.i32(), Timestamp: -1}
			if _, ok := allowed[row.Partition]; !ok {
				c.fail("invalid")
				break
			}
			if _, duplicate := seenPartitions[row.Partition]; duplicate {
				c.fail("invalid")
				break
			}
			seenPartitions[row.Partition] = struct{}{}
			if compact {
				row.Offset = c.i64()
				row.LeaderEpoch = c.i32()
				c.text(true, true, false) // Arbitrary consumer application metadata.
				row.ErrorCode = c.i16()
				c.tags()
			} else {
				row.ErrorCode = c.i16()
				row.Timestamp = c.i64()
				row.Offset = c.i64()
				row.LeaderEpoch = c.i32()
			}
			if row.Offset < -1 || row.LeaderEpoch < -1 || row.Timestamp < -1 {
				c.fail("invalid")
				break
			}
			result = append(result, row)
		}
		if compact {
			c.tags()
		}
	}
	return result
}
