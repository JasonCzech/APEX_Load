package signatures

import "testing"

func latencySymptom() Observation {
	return Observation{AnomalyID: "symptom", Series: "transaction/checkout/p95_ms", Kind: KindLatency, Direction: Increase, PeakZ: 9}
}

// Every signature, three ways: an indicator that moved before the symptom on
// the symptom's path supports it; an indicator measured on the path that
// stayed in band contradicts it; with none of its evidence measured it lacks
// evidence.
func TestEverySignatureHasPositiveContradictedAndMissingCases(t *testing.T) {
	for _, s := range All() {
		indicator := s.Indicators[0]
		direction := indicator.Direction
		if direction == "" {
			direction = Increase
		}
		available := map[string]Availability{indicator.Kind: {Any: true, Connected: true}}

		positive := s.Judge(Context{Symptom: latencySymptom(), Available: available, Observations: []Observation{
			{AnomalyID: "cause", Kind: indicator.Kind, Direction: direction, PeakZ: 8, Order: Precedes, Connected: true},
		}})
		if positive.Verdict != Supported || len(positive.Supporting) != 1 || positive.Score <= 0 {
			t.Errorf("%s positive: %+v", s.Name, positive)
		}
		contradicted := s.Judge(Context{Symptom: latencySymptom(), Available: available})
		if contradicted.Verdict != Contradicted || len(contradicted.Reasons) == 0 || contradicted.Score >= 0 {
			t.Errorf("%s contradicted: %+v", s.Name, contradicted)
		}
		missing := s.Judge(Context{Symptom: latencySymptom(), Available: map[string]Availability{}})
		if missing.Verdict != MissingEvidence || len(missing.Missing) != len(s.Required) {
			t.Errorf("%s missing: %+v", s.Name, missing)
		}
		if s.Falsification == "" || len(s.Competing) == 0 || len(s.Required) == 0 {
			t.Errorf("%s is incompletely declared", s.Name)
		}
	}
}

func TestEvidenceThatFollowsTheSymptomContradicts(t *testing.T) {
	r := ResourcePressure.Judge(Context{Symptom: latencySymptom(), Available: map[string]Availability{KindCPU: {Any: true, Connected: true}}, Observations: []Observation{
		{AnomalyID: "late", Kind: KindCPU, Direction: Increase, PeakZ: 8, Order: Follows, Connected: true},
	}})
	if r.Verdict != Contradicted || len(r.Contradicting) != 1 || len(r.Supporting) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestSupportIsWeightedByConnectionAndOrder(t *testing.T) {
	judge := func(o Observation) float64 {
		return GarbageCollection.Judge(Context{Symptom: latencySymptom(), Available: map[string]Availability{KindGC: {Any: true, Connected: o.Connected}}, Observations: []Observation{o}}).Score
	}
	strong := judge(Observation{Kind: KindGC, Direction: Increase, PeakZ: 10, Order: Precedes, Connected: true})
	unordered := judge(Observation{Kind: KindGC, Direction: Increase, PeakZ: 10, Order: Unordered, Connected: true})
	loose := judge(Observation{Kind: KindGC, Direction: Increase, PeakZ: 10, Order: Precedes})
	if !(strong > unordered && strong > loose && unordered > 0 && loose > 0) {
		t.Fatalf("strong %.2f unordered %.2f loose %.2f", strong, unordered, loose)
	}
	// A decrease is not GC pressure.
	if r := GarbageCollection.Judge(Context{Symptom: latencySymptom(), Available: map[string]Availability{KindGC: {Any: true}}, Observations: []Observation{{Kind: KindGC, Direction: Decrease, PeakZ: 9, Order: Precedes}}}); r.Verdict != Contradicted {
		t.Fatalf("%+v", r)
	}
}

func TestSignatureSpecificRules(t *testing.T) {
	scaling := map[string]Availability{KindScaling: {Any: true, Connected: true}}
	restart := []Observation{{Kind: KindScaling, Direction: Decrease, PeakZ: 6, Order: Precedes, Connected: true}}
	plain := ColdStart.Judge(Context{Symptom: latencySymptom(), Available: scaling, Observations: restart})
	atStep := latencySymptom()
	atStep.LoadCoincident = true
	stepped := ColdStart.Judge(Context{Symptom: atStep, Available: scaling, Observations: restart})
	if plain.Verdict != Supported || stepped.Score <= plain.Score || len(stepped.Notes) == 0 || len(stepped.Reasons) != 0 {
		t.Fatalf("plain %+v stepped %+v", plain, stepped)
	}
	// Network phases explain slow or failing requests, not lost throughput.
	throughput := Observation{Kind: KindThroughput, Direction: Decrease, PeakZ: 7}
	if r := NetworkPhase.Judge(Context{Symptom: throughput, Available: map[string]Availability{KindDNS: {Any: true}}}); r.Verdict != NotApplicable {
		t.Fatalf("%+v", r)
	}
}

func TestAFlatSiblingIndicatorDoesNotContradict(t *testing.T) {
	available := map[string]Availability{KindCPU: {Any: true, Connected: true}, KindMemory: {Any: true, Connected: true}}
	cpu := []Observation{{Kind: KindCPU, Direction: Increase, PeakZ: 10, Order: Precedes, Connected: true}}
	r := ResourcePressure.Judge(Context{Symptom: latencySymptom(), Available: available, Observations: cpu})
	alone := ResourcePressure.Judge(Context{Symptom: latencySymptom(), Available: map[string]Availability{KindCPU: {Any: true, Connected: true}}, Observations: cpu})
	if r.Verdict != Supported || len(r.Reasons) != 0 || r.Score != alone.Score {
		t.Fatalf("with flat memory %+v, alone %+v", r, alone)
	}
}

func TestClassify(t *testing.T) {
	for name, want := range map[string]string{
		"host_cpu_percent":                             KindCPU,
		"Hardware Resources|CPU|%Busy":                 KindCPU,
		"process_memory_bytes":                         KindMemory,
		"go_heap_bytes":                                KindMemory,
		"jvm_gc_seconds_rate":                          KindGC,
		"builtin:tech.jvm.memory.gc.suspensionTime":    KindGC,
		"JVM|Garbage Collection|GC Time Spent Per Min": KindGC,
		"pod_oom_kills":                                KindScaling,
		"pod_restart_count":                            KindScaling,
		"hpa_current_replicas":                         KindScaling,
		"kafka_consumergroup_lag":                      KindConsumerLag,
		"kafka_consumer_records_lag_max":               KindConsumerLag,
		"orders.DLQ.messages":                          KindDeadLetters,
		"consumer_rebalances_total":                    KindRebalance,
		"http_client_retries_total":                    KindRetries,
		"hikaricp_connections_pending_acquire":         KindPool,
		"db_connection_pool_active":                    KindPool,
		"pg_lock_wait_seconds":                         KindDBWait,
		"queue_depth":                                  KindQueueDepth,
		"rabbitmq_queue_messages_ready":                KindQueueDepth,
		"broker_enqueue_rate":                          KindArrivalRate,
		"message_age_seconds":                          KindResidenceTime,
		"http_request_rate":                            "",
		"builtin:service.response.time":                "",
		"flag_count":                                   "",
	} {
		if got := Classify(name); got != want {
			t.Errorf("%q: %q, want %q", name, got, want)
		}
	}
}
