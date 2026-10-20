package api

import "github.com/jasonczech/apexload/pkg/distributed/generatorrpc"

// Java evidence comes from authoritative action aggregates, not duplicated
// synthetic transaction wrappers in the legacy raw transaction stream.
func apiAggregateProtocolSummary(aggregate generatorrpc.MetricAggregate) *ServerTelemetrySummary {
	s := apiMessagingSummary(aggregate.Messaging)
	if aggregate.JavaHTTP != nil {
		if s == nil {
			s = &ServerTelemetrySummary{}
		}
		s.JavaHTTP = aggregate.JavaHTTP.RetainedForPopulation(aggregate.Count)
	}
	return s
}
