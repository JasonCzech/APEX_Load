package collectoragent

import (
	"context"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// kafkaManagementObserver is a fixed administrative reader. It has no load
// protocol client, consumer membership, retries, mutation or record-fetch API.
type kafkaManagementObserver struct{ dial kafkaManagementDialFunc }

func (kafkaManagementObserver) prepareTarget(target *ManagementTarget) error {
	return prepareKafkaManagementTarget(target)
}
func (kafkaManagementObserver) validateTarget(target *ManagementTarget) error {
	return validateKafkaManagementTarget(target)
}

func (observer kafkaManagementObserver) observe(ctx context.Context, poll *managementObservation) (snapshot *analysis.MessagingManagementSnapshot, err error) {
	if err = validateKafkaManagementTarget(&poll.target); err != nil {
		return nil, &managementObservationError{reason: "permission_denied"}
	}
	if !poll.credentials.MatchesIdentity(poll.target.Identity) {
		return nil, &managementObservationError{reason: "permission_denied"}
	}
	started := time.Now()
	topics := kafkaManagementTopics(poll.target.Scope.Entities, false)
	transport := newKafkaManagementTransport(poll, observer.dial, topics)
	defer func() {
		closeErr := transport.close()
		if closeErr != nil && err == nil {
			err = closeErr
			snapshot = nil
		}
	}()
	seed := poll.target.Endpoints[0]
	if _, err = transport.connection(ctx, seed); err != nil {
		return nil, err
	}
	rows := make([]analysis.MessagingManagementRow, len(poll.target.Scope.Entities))
	observed := make([]time.Time, len(rows))
	leaders := map[string][]analysis.ManagementEntity{}
	groups := map[string][]analysis.ManagementEntity{}
	for i, entity := range poll.target.Scope.Entities {
		rows[i] = kafkaManagementMissingRow(entity, "not_observed")
		groups[entity.Kafka.Group] = append(groups[entity.Kafka.Group], entity)
		topic, topicFound := transport.metadata.Topics[entity.Kafka.Topic]
		if !topicFound {
			continue
		}
		if topic.ErrorCode != 0 {
			kafkaManagementSetUnavailableRow(&rows[i], kafkaManagementErrorReason(kafkaManagementProviderError(topic.ErrorCode)))
			continue
		}
		partition, partitionFound := topic.Partitions[entity.Kafka.Partition]
		if !partitionFound {
			continue
		}
		if partition.ErrorCode != 0 {
			kafkaManagementSetUnavailableRow(&rows[i], kafkaManagementErrorReason(kafkaManagementProviderError(partition.ErrorCode)))
			continue
		}
		rows[i].KafkaOffsets.TopicID = topic.TopicID
		rows[i].KafkaOffsets.LeaderEpoch = kafkaManagementPosition(int64(partition.LeaderEpoch), nil)
		if rows[i].KafkaOffsets.LeaderEpoch.State == analysis.ManagementStateObserved {
			observed[i] = transport.metadataAt
		}
		broker, exists := transport.metadata.Brokers[partition.LeaderID]
		if !exists {
			continue
		}
		route, routeErr := kafkaRouteForBroker(&poll.target, broker.Host, broker.Port)
		if routeErr != nil {
			rows[i].KafkaOffsets.HighWatermarkNext = kafkaManagementUnavailableInteger("permission_denied")
			rows[i].KafkaOffsets.LastStableNext = kafkaManagementUnavailableInteger("permission_denied")
			continue
		}
		leaders[route.endpoint] = append(leaders[route.endpoint], entity)
	}
	// Each group is one exact OffsetFetch query. No null/all-topic request or
	// consumer subscription can be constructed by this closed operation set.
	for _, group := range kafkaManagementSortedKeys(groups) {
		entities := groups[group]
		connection, operationErr := transport.connection(ctx, seed)
		var response kafkaManagementResponse
		if operationErr == nil {
			response, operationErr = connection.exchange(ctx, poll, kafkaManagementRequest{Operation: kafkaManagementFindCoordinator, Version: 2, Group: group})
			if operationErr == nil && response.ErrorCode != 0 {
				operationErr = kafkaManagementProviderError(response.ErrorCode)
			}
		}
		if operationErr == nil {
			coordinator := response.Coordinator
			if coordinator == nil {
				operationErr = &managementObservationError{reason: "invalid"}
			} else if coordinator.ErrorCode != 0 {
				operationErr = kafkaManagementProviderError(coordinator.ErrorCode)
			} else {
				if known, ok := transport.metadata.Brokers[coordinator.NodeID]; ok && (known.Host != coordinator.Host || known.Port != coordinator.Port) {
					operationErr = &managementObservationError{reason: "identity_mismatch"}
				} else {
					route, routeErr := kafkaRouteForBroker(&poll.target, coordinator.Host, coordinator.Port)
					if routeErr != nil {
						operationErr = &managementObservationError{reason: "permission_denied"}
					} else {
						connection, operationErr = transport.connection(ctx, route.endpoint)
					}
				}
			}
		}
		if operationErr == nil {
			response, operationErr = connection.exchange(ctx, poll, kafkaManagementRequest{Operation: kafkaManagementOffsetFetch, Version: 7, Group: group, Topics: kafkaManagementTopics(entities, true)})
			if operationErr == nil && response.ErrorCode != 0 {
				operationErr = kafkaManagementProviderError(response.ErrorCode)
			}
		}
		captured := time.Now()
		offsets := kafkaManagementOffsetIndex(response.Offsets)
		for i := range rows {
			entity := rows[i].Entity.Kafka
			if entity.Group != group {
				continue
			}
			value, exists := offsets[kafkaManagementPartitionKey(entity.Topic, entity.Partition)]
			if operationErr != nil {
				rows[i].KafkaOffsets.CommittedNext = kafkaManagementUnavailableInteger(kafkaManagementErrorReason(operationErr))
				rows[i].KafkaOffsets.CommittedLeaderEpoch = rows[i].KafkaOffsets.CommittedNext.Clone()
				continue
			}
			if !exists {
				continue
			}
			providerErr := kafkaManagementProviderError(value.ErrorCode)
			rows[i].KafkaOffsets.CommittedNext = kafkaManagementPosition(value.Offset, providerErr)
			rows[i].KafkaOffsets.CommittedLeaderEpoch = kafkaManagementPosition(int64(value.LeaderEpoch), providerErr)
			if rows[i].KafkaOffsets.CommittedNext.State == analysis.ManagementStateObserved || rows[i].KafkaOffsets.CommittedLeaderEpoch.State == analysis.ManagementStateObserved {
				kafkaManagementOldest(&observed[i], captured)
			}
		}
	}
	for _, endpoint := range kafkaManagementSortedKeys(leaders) {
		entities := leaders[endpoint]
		partitions := kafkaManagementTopics(entities, true)
		for ti := range partitions {
			topic := transport.metadata.Topics[partitions[ti].Name]
			for pi := range partitions[ti].Partitions {
				partitions[ti].Partitions[pi].CurrentLeaderEpoch = topic.Partitions[partitions[ti].Partitions[pi].Partition].LeaderEpoch
			}
		}
		// Read-uncommitted's end is the high watermark; read-committed's end is
		// the last stable offset. Neither value is a physical replica log end.
		for _, isolation := range []int8{0, 1} {
			connection, operationErr := transport.connection(ctx, endpoint)
			var response kafkaManagementResponse
			if operationErr == nil {
				response, operationErr = connection.exchange(ctx, poll, kafkaManagementRequest{Operation: kafkaManagementListOffsets, Version: 5, Isolation: isolation, Topics: partitions})
				if operationErr == nil && response.ErrorCode != 0 {
					operationErr = kafkaManagementProviderError(response.ErrorCode)
				}
			}
			captured := time.Now()
			offsets := kafkaManagementOffsetIndex(response.Offsets)
			for i := range rows {
				entity := rows[i].Entity.Kafka
				if !kafkaManagementContainsPartition(entities, entity.Topic, entity.Partition) {
					continue
				}
				position := kafkaManagementUnavailableInteger("not_observed")
				if operationErr != nil {
					position = kafkaManagementUnavailableInteger(kafkaManagementErrorReason(operationErr))
				} else if value, exists := offsets[kafkaManagementPartitionKey(entity.Topic, entity.Partition)]; exists {
					position = kafkaManagementPosition(value.Offset, kafkaManagementProviderError(value.ErrorCode))
					// ListOffsets' returned epoch is provenance of its offset lookup; it is
					// not a replacement for Metadata's current leader epoch.
				}
				if isolation == 0 {
					rows[i].KafkaOffsets.HighWatermarkNext = position
				} else {
					rows[i].KafkaOffsets.LastStableNext = position
				}
				if position.State == analysis.ManagementStateObserved {
					kafkaManagementOldest(&observed[i], captured)
				}
			}
		}
	}
	if err = transport.close(); err != nil {
		return nil, err
	}
	if err = managementPollAuthorityError(ctx, poll.grant); err != nil {
		return nil, err
	}
	completed := time.Now()
	base := poll.base(started, completed, completed.Sub(started), managementMinimumInterval("kafka-offsets", poll.target), 0)
	base.Rows = rows
	base.InventoryComplete = true
	for i := range base.Rows {
		row := &base.Rows[i]
		offsets := row.KafkaOffsets
		if offsets.HighWatermarkNext.State == analysis.ManagementStateObserved && offsets.LastStableNext.State == analysis.ManagementStateObserved {
			hwm, _ := analysis.ParseManagementInteger(*offsets.HighWatermarkNext.Value)
			lso, _ := analysis.ParseManagementInteger(*offsets.LastStableNext.Value)
			if lso > hwm {
				offsets.LastStableNext = kafkaManagementUnavailableInteger("inconsistent_position")
			}
		}
		if !observed[i].IsZero() {
			captured := observed[i].UTC()
			age := max(int64(0), completed.Sub(observed[i]).Milliseconds())
			row.State, row.Reason, row.ObservedAt, row.ObservationAgeMS = analysis.ManagementStateObserved, "", &captured, &age
		} else {
			base.InventoryComplete, base.Incomplete = false, true
		}
		for _, position := range []analysis.ManagementInteger{offsets.CommittedNext, offsets.HighWatermarkNext, offsets.LastStableNext} {
			if position.State != analysis.ManagementStateObserved {
				base.Incomplete = true
			}
		}
	}
	base.RequestCount, base.PageCount = poll.counts()
	if err = base.ValidateFor(poll.target.Scope, poll.target.Identity); err != nil {
		return nil, &managementObservationError{reason: "invalid"}
	}
	if err = managementPollAuthorityError(ctx, poll.grant); err != nil {
		return nil, err
	}
	return &base, nil
}

func kafkaManagementMissingRow(entity analysis.ManagementEntity, reason string) analysis.MessagingManagementRow {
	missing := kafkaManagementUnavailableInteger(reason)
	return analysis.MessagingManagementRow{Entity: entity.Clone(), State: missing.State, Reason: reason, KafkaOffsets: &analysis.KafkaOffsets{
		CommittedNext: missing.Clone(), HighWatermarkNext: missing.Clone(), LastStableNext: missing.Clone(), LeaderEpoch: missing.Clone(), CommittedLeaderEpoch: missing.Clone(),
		LogEndNext: analysis.ManagementInteger{State: analysis.ManagementStateUnsupported, Reason: "unsupported"},
	}}
}
func kafkaManagementSetUnavailableRow(row *analysis.MessagingManagementRow, reason string) {
	*row = kafkaManagementMissingRow(row.Entity, reason)
}
func kafkaManagementUnavailableInteger(reason string) analysis.ManagementInteger {
	state := analysis.ManagementStateInvalid
	switch reason {
	case "not_observed", "unqualified":
		state = analysis.ManagementStateMissing
	case "permission_denied":
		state = analysis.ManagementStatePermission
	case "budget_exhausted":
		state = analysis.ManagementStateBudget
	case "unsupported":
		state = analysis.ManagementStateUnsupported
	case "cancelled":
		state = analysis.ManagementStateCancelled
	case "timeout", "stale":
		state = analysis.ManagementStateStale
	case "fenced":
		state = analysis.ManagementStateFenced
	case "throttled":
		state = analysis.ManagementStateThrottled
	}
	return analysis.ManagementInteger{State: state, Reason: reason}
}
func kafkaManagementPosition(value int64, providerErr error) analysis.ManagementInteger {
	if providerErr != nil {
		return kafkaManagementUnavailableInteger(kafkaManagementErrorReason(providerErr))
	}
	if value == -1 {
		return kafkaManagementUnavailableInteger("not_observed")
	}
	if value < -1 {
		return kafkaManagementUnavailableInteger("invalid")
	}
	observed, err := analysis.ObservedManagementInteger(value)
	if err != nil {
		return kafkaManagementUnavailableInteger("invalid")
	}
	return observed
}
func kafkaManagementOldest(oldest *time.Time, candidate time.Time) {
	if oldest.IsZero() || candidate.Before(*oldest) {
		*oldest = candidate
	}
}
func kafkaManagementPartitionKey(topic string, partition int32) analysis.KafkaManagementEntity {
	return analysis.KafkaManagementEntity{Topic: topic, Partition: partition}
}
func kafkaManagementOffsetIndex(offsets []kafkaManagementOffset) map[analysis.KafkaManagementEntity]kafkaManagementOffset {
	indexed := make(map[analysis.KafkaManagementEntity]kafkaManagementOffset, len(offsets))
	for _, offset := range offsets {
		indexed[kafkaManagementPartitionKey(offset.Topic, offset.Partition)] = offset
	}
	return indexed
}
func kafkaManagementContainsPartition(entities []analysis.ManagementEntity, topic string, partition int32) bool {
	for _, entity := range entities {
		if entity.Kafka.Topic == topic && entity.Kafka.Partition == partition {
			return true
		}
	}
	return false
}
func kafkaManagementTopics(entities []analysis.ManagementEntity, withPartitions bool) []kafkaManagementTopicRequest {
	inventory := map[string]map[int32]bool{}
	for _, entity := range entities {
		if inventory[entity.Kafka.Topic] == nil {
			inventory[entity.Kafka.Topic] = map[int32]bool{}
		}
		inventory[entity.Kafka.Topic][entity.Kafka.Partition] = true
	}
	topics := make([]kafkaManagementTopicRequest, 0, len(inventory))
	for _, name := range kafkaManagementSortedKeys(inventory) {
		topic := kafkaManagementTopicRequest{Name: name}
		if withPartitions {
			for partition := range inventory[name] {
				topic.Partitions = append(topic.Partitions, kafkaManagementPartitionRequest{Partition: partition, CurrentLeaderEpoch: -1})
			}
			sort.Slice(topic.Partitions, func(i, j int) bool { return topic.Partitions[i].Partition < topic.Partitions[j].Partition })
		}
		topics = append(topics, topic)
	}
	return topics
}
func kafkaManagementSortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

var _ managementObserver = kafkaManagementObserver{}
