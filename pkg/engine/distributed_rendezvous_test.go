package engine

import (
	"context"
	"github.com/jasonczech/apexload/pkg/distributed/rendezvous"
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
	"testing"
	"time"
)

func TestDistributedRendezvousVUserRoundsAndCancellation(t *testing.T) {
	c, err := rendezvous.New([]rendezvous.MemberRange{{Count: 2}}, []rendezvous.Contract{{Key: "checkout", Target: 2, TimeoutMS: 30}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := rendezvous.NewAggregator(c, 2)
	first := &VUser{ID: 0, DistributedRendezvous: a}
	second := &VUser{ID: 1, DistributedRendezvous: a}
	spec := dsl.DistributedRendezvousSpec{Key: "checkout", TimeoutMS: 30, OnTimeout: "fail"}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		done := make(chan error, 1)
		go func() { done <- first.waitAtDistributedRendezvous(ctx, spec) }()
		if err = second.waitAtDistributedRendezvous(ctx, spec); err != nil {
			t.Fatal(err)
		}
		if err = <-done; err != nil {
			t.Fatal(err)
		}
	}
	if first.rendezvousRounds["checkout"] != 2 {
		t.Fatal("round identity lost")
	}
	if err = first.waitAtDistributedRendezvous(ctx, spec); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("lost member released early: %v", err)
	}
	spec.OnTimeout = "continue"
	if err = second.waitAtDistributedRendezvous(ctx, spec); err != nil {
		t.Fatal("late continue policy", err)
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if err = first.waitAtDistributedRendezvous(canceled, spec); err == nil {
		t.Fatal("cancellation invented release")
	}
}
